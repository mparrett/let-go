"""Tests for the docs runner's decision logic.

Everything here is offline: no clones, no GitHub, no model calls. The cases are
drawn from defects found in review, so each one names the behaviour it locks
down rather than just exercising a function.
"""

from __future__ import annotations

import io
import sys
import urllib.error
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import runner


@pytest.fixture(autouse=True)
def _reset_run_state(monkeypatch):
    """The status contract is module state; keep tests independent of order."""
    monkeypatch.setattr(runner, "_PARTIAL_REASONS", [])
    monkeypatch.setattr(runner, "_REDACTIONS", [])


# --------------------------------------------------------------------------
# Citation matching
# --------------------------------------------------------------------------

@pytest.mark.parametrize(
    "citations,path",
    [
        # A page that cites the exact file.
        ("sources: pkg/vm/stack.go", "pkg/vm/stack.go"),
        # A page about a directory covers files inside it.
        ("https://github.com/o/r/tree/main/pkg/vm", "pkg/vm/stack.go"),
        # Trailing slash and a comma-separated citation list.
        ('"repo: nooga/let-go wasm/, docs/guide"', "wasm/loader.go"),
    ],
)
def test_page_covers_accepts_real_references(citations, path):
    assert runner.page_covers(citations, path)


@pytest.mark.parametrize(
    "citations,path,why",
    [
        # No left boundary: `scripts` must not match the tail of `transcripts`.
        ("see the transcripts", "scripts/x.md", "substring at word end"),
        ("postscripts and things", "scripts/x.md", "substring mid-word"),
        (
            "repo: nooga/let-go transcripts/session.md",
            "scripts/ir-stress.md",
            "substring before a separator",
        ),
        # No right boundary: `pkg/vm` must not match `pkg/vmtest`.
        (
            "https://github.com/o/r/tree/main/pkg/vmtest",
            "pkg/vm/stack.go",
            "directory is a prefix of another",
        ),
        # An ancestor directory shared with unrelated pages is not evidence:
        # every page citing anything under docs/design would otherwise match.
        (
            "sources: docs/design/pods.md",
            "docs/design/ir-dynamic-vars.md",
            "shared ancestor only",
        ),
    ],
)
def test_page_covers_rejects_false_positives(citations, path, why):
    assert not runner.page_covers(citations, path), why


# --------------------------------------------------------------------------
# Frontmatter
# --------------------------------------------------------------------------

INLINE_PAGE = """\
---
title: "Stack VM"
resource: "https://github.com/o/r/tree/main/pkg/vm"
sources: ["repo: o/r pkg/vm, 2026-07-01"]
---

# Stack VM
"""

BLOCK_PAGE = """\
---
title: "Concurrency Model"
resource: "https://github.com/o/r/blob/main/pkg/vm/exec_context.go"
sources:
  - "repo: o/r pkg/vm/exec_context.go"
  - design: docs/design/exec-context-threading.md
---

# Concurrency Model
"""


def test_frontmatter_reads_inline_scalars_and_lists():
    fields = runner.parse_frontmatter(INLINE_PAGE)
    assert fields["title"] == "Stack VM"
    assert "pkg/vm" in runner.citation_text(fields)


def test_frontmatter_reads_block_style_lists():
    """The original line-scanning parser returned '' for these.

    A good part of the wiki writes `sources:` as a block list, so this is the
    difference between seeing a page's citations and thinking it has none.
    """
    fields = runner.parse_frontmatter(BLOCK_PAGE)
    citations = runner.citation_text(fields)
    assert "pkg/vm/exec_context.go" in citations
    # A list entry may itself be a mapping; it must still be searchable.
    assert "exec-context-threading" in citations


def test_frontmatter_survives_malformed_input():
    assert runner.parse_frontmatter("no frontmatter here") == {}
    assert runner.parse_frontmatter("---\n: : :\nnot yaml\n---\n") == {}


# --------------------------------------------------------------------------
# What counts as an editable page
# --------------------------------------------------------------------------

@pytest.mark.parametrize(
    "relpath,expected",
    [
        ("concepts/stack-vm.md", True),
        ("references/lgx-edn.md", True),
        ("sources/design-pods.md", True),
        # Tracked agent prompts are not wiki pages.
        ("tools/enrich/prompts/reference_instruction.md", False),
        # Reserved root files.
        ("index.md", False),
        ("log.md", False),
        ("AGENTS.md", False),
        # Working-checkout noise the old denylist admitted.
        (".enrich/enhance/brief.md", False),
        (".venv/lib/thing.md", False),
        ("docs/superpowers/specs/design.md", False),
    ],
)
def test_is_wiki_content_allowlists_content_dirs(tmp_path, relpath, expected):
    page = tmp_path / relpath
    page.parent.mkdir(parents=True, exist_ok=True)
    page.write_text("x")
    assert runner.is_wiki_content(tmp_path, page) is expected


@pytest.mark.parametrize(
    "path,expected",
    [
        ("docs/design/ir-dynamic-vars.md", True),
        ("wasm.go", True),
        ("docs/README.md", False),
        (".github/workflows/ci.yml", False),
        ("pkg/vm/stack_test.go", False),
        ("go.sum", False),
    ],
)
def test_is_documentable(path, expected):
    assert runner.is_documentable(path) is expected


# --------------------------------------------------------------------------
# Validation must fail closed
# --------------------------------------------------------------------------

def _wiki_with_checker(tmp_path: Path, body: str) -> Path:
    checker = tmp_path / "tools" / "check_wiki.py"
    checker.parent.mkdir(parents=True, exist_ok=True)
    checker.write_text(body)
    return tmp_path


def test_validate_returns_complaints(tmp_path):
    wiki = _wiki_with_checker(
        tmp_path,
        "import sys\nprint('page.md: unknown tag')\nsys.exit(1)\n",
    )
    assert runner.validate(wiki) == ["page.md: unknown tag"]


def test_validate_returns_empty_when_clean(tmp_path):
    wiki = _wiki_with_checker(tmp_path, "print('check_wiki: OK')\n")
    assert runner.validate(wiki) == []


def test_validate_raises_when_checker_absent(tmp_path):
    with pytest.raises(runner.ValidatorUnavailable):
        runner.validate(tmp_path)


def test_validate_raises_when_checker_crashes(tmp_path):
    """A crashed validator is not a clean validator.

    This is the case that previously read as a pass and let unverified pages
    through to a pull request.
    """
    wiki = _wiki_with_checker(tmp_path, "import yaml_missing_module\n")
    with pytest.raises(runner.ValidatorUnavailable):
        runner.validate(wiki)


def test_validate_raises_on_unexpected_exit_code(tmp_path):
    wiki = _wiki_with_checker(tmp_path, "import sys\nsys.exit(7)\n")
    with pytest.raises(runner.ValidatorUnavailable):
        runner.validate(wiki)


def test_new_validation_errors_ignores_pre_existing(tmp_path):
    before = ["a.md: orphan page", "b.md: unknown tag"]
    after = ["a.md: orphan page", "b.md: unknown tag", "c.md: unknown tag"]
    assert runner.new_validation_errors(before, after) == ["c.md: unknown tag"]


def test_new_validation_errors_empty_when_nothing_introduced():
    shared = ["a.md: orphan page"]
    assert runner.new_validation_errors(shared, shared) == []


# --------------------------------------------------------------------------
# Status contract
# --------------------------------------------------------------------------

def test_status_is_a_single_line_marked_complete(capsys):
    runner.emit_status("documented", sha="abc123", pages=2)
    lines = [
        line for line in capsys.readouterr().out.splitlines()
        if line.startswith("DOCS_RUNNER_STATUS")
    ]
    assert len(lines) == 1
    assert "completeness=complete" in lines[0]
    assert "action=documented" in lines[0]


def test_partial_reasons_ride_on_the_terminal_status(capsys):
    """Incompleteness must not be its own status line.

    A separate `action=partial` was later overwritten by `action=documented`,
    so a caller reading the final status saw partial work as complete.
    """
    runner.note_partial("diff-truncated", limit_bytes=1)
    runner.note_partial("page-cap-reached", limit=5)
    runner.emit_status("documented", sha="abc123")

    out = capsys.readouterr().out
    status = [ln for ln in out.splitlines() if ln.startswith("DOCS_RUNNER_STATUS")]
    assert len(status) == 1, "exactly one status line per run"
    assert "completeness=partial" in status[0]
    assert "partial_reasons=diff-truncated,page-cap-reached" in status[0]


# --------------------------------------------------------------------------
# Credentials
# --------------------------------------------------------------------------

BASE_ENV = {
    "SOURCE_REPO": "o/r",
    "SOURCE_SHA": "deadbeef",
    "WIKI_REPO": "o/w",
    "WORKDIR": "/tmp/x",
}


def _set_env(monkeypatch, **overrides):
    for key in (*BASE_ENV, "GITHUB_TOKEN", "DRY_RUN"):
        monkeypatch.delenv(key, raising=False)
    for key, value in {**BASE_ENV, **overrides}.items():
        monkeypatch.setenv(key, value)


def test_dry_run_does_not_require_a_token(monkeypatch):
    _set_env(monkeypatch, DRY_RUN="true")
    cfg = runner.Config.from_env()
    assert cfg.dry_run
    assert cfg.github_token == ""


def test_real_run_requires_a_token(monkeypatch):
    _set_env(monkeypatch)
    with pytest.raises(RuntimeError, match="GITHUB_TOKEN"):
        runner.Config.from_env()


def test_dry_run_never_authenticates_even_with_a_token(monkeypatch):
    """The dry-run contract is 'touches no GitHub credential'.

    A token present in the environment must not tempt the anonymous-clone
    fallback into using it.
    """
    _set_env(monkeypatch, DRY_RUN="true", GITHUB_TOKEN="ghp_realtoken123456")
    assert runner.Config.from_env().may_authenticate is False


def test_real_run_may_authenticate(monkeypatch):
    _set_env(monkeypatch, GITHUB_TOKEN="ghp_realtoken123456")
    assert runner.Config.from_env().may_authenticate is True


# --------------------------------------------------------------------------
# Retry safety
# --------------------------------------------------------------------------

def _cfg(monkeypatch, **overrides):
    _set_env(monkeypatch, GITHUB_TOKEN="ghp_realtoken123456", **overrides)
    return runner.Config.from_env()


@pytest.mark.parametrize(
    "output,expected",
    [
        ("abc123def\trefs/heads/docs-runner/x\n", "abc123def"),
        ("", None),
        ("\n", None),
    ],
)
def test_parse_ls_remote_sha(output, expected):
    assert runner.parse_ls_remote_sha(output) == expected


def test_existing_pr_is_adopted_not_duplicated(monkeypatch, tmp_path, capsys):
    """A re-run must not open a second PR for the same commit.

    It must also not push over a branch that already has a PR open, since
    someone may be reviewing it.
    """
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(
        runner, "find_open_pr", lambda c, b: "https://github.com/o/w/pull/7"
    )

    pushed: list[list[str]] = []

    def _record(cmd, cwd=None, check=True):
        pushed.append(cmd)
        return ""

    monkeypatch.setattr(runner, "run", _record)

    url = runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")

    assert url == "https://github.com/o/w/pull/7"
    assert not any("push" in cmd for cmd in pushed), "must not push over a live PR"


def test_occupied_branch_is_never_overwritten(monkeypatch, tmp_path):
    """A branch that already exists is left alone; we publish to a new name.

    Force-pushing under a lease was not safe enough: a lease only checks the
    ref's SHA, and opening a pull request does not change a SHA. A branch that
    acquired a pull request between the lookup and the push would still have
    been overwritten while under review.
    """
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(runner, "find_open_pr", lambda c, b: None)
    monkeypatch.setattr(runner, "github_api", lambda *a, **k: {"html_url": "url"})

    taken = {"docs-runner/abc123", "docs-runner/abc123-2"}
    commands: list[list[str]] = []

    def _fake_run(cmd, cwd=None, check=True):
        commands.append(cmd)
        if "ls-remote" in cmd and cmd[-1] in taken:
            return f"deadbeef\trefs/heads/{cmd[-1]}\n"
        return ""

    monkeypatch.setattr(runner, "run", _fake_run)

    runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")

    push = next(cmd for cmd in commands if "push" in cmd)
    assert not any(arg.startswith("--force") for arg in push), "never force-push"
    # First free name in the family, skipping both occupied ones.
    assert "HEAD:refs/heads/docs-runner/abc123-3" in push


def test_fresh_branch_uses_the_canonical_name(monkeypatch, tmp_path):
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(runner, "find_open_pr", lambda c, b: None)
    monkeypatch.setattr(runner, "github_api", lambda *a, **k: {"html_url": "url"})

    commands: list[list[str]] = []

    def _fake_run(cmd, cwd=None, check=True):
        commands.append(cmd)
        return ""  # ls-remote finds nothing

    monkeypatch.setattr(runner, "run", _fake_run)

    runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")

    push = next(cmd for cmd in commands if "push" in cmd)
    assert "HEAD:refs/heads/docs-runner/abc123" in push


def test_exhausted_branch_names_fail_loudly(monkeypatch, tmp_path):
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(runner, "find_open_pr", lambda c, b: None)
    monkeypatch.setattr(
        runner, "run",
        lambda cmd, cwd=None, check=True: f"deadbeef\trefs/heads/{cmd[-1]}\n",
    )
    with pytest.raises(RuntimeError, match="branch names"):
        runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")


def _pull(ref="docs-runner/abc123", base="main", repo="o/w", url="PR"):
    return {
        "head": {"ref": ref, "repo": {"full_name": repo}},
        "base": {"ref": base},
        "html_url": url,
    }


def test_open_pr_lookup_matches_the_whole_branch_family(monkeypatch):
    """A retry may have published from a suffixed branch; adopt that too."""
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(
        runner, "github_api",
        lambda c, m, p, payload=None: [_pull(ref="docs-runner/abc123-2")],
    )
    assert runner.find_open_pr(cfg, "docs-runner/abc123") == "PR"


def test_open_pr_lookup_ignores_a_different_base(monkeypatch):
    """Same head, different base, is a different proposal.

    Adopting it would report success for a pull request we are not making.
    """
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(
        runner, "github_api",
        lambda c, m, p, payload=None: [_pull(base="some-other-branch")],
    )
    assert runner.find_open_pr(cfg, "docs-runner/abc123") is None


def test_open_pr_lookup_ignores_a_pr_from_another_repo(monkeypatch):
    """A fork can open the same branch name against our base.

    The wiki is public, so `theirfork:docs-runner/<sha>` is anyone's to create.
    Adopting it would report success and silently never publish this run's
    work — so the head repository has to be checked, not just the branch name.
    """
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(
        runner, "github_api",
        lambda c, m, p, payload=None: [_pull(repo="attacker/w")],
    )
    assert runner.find_open_pr(cfg, "docs-runner/abc123") is None


def test_open_pr_lookup_ignores_a_deleted_fork(monkeypatch):
    """`head.repo` is null once a fork is deleted. Null is not ours either."""
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(
        runner, "github_api",
        lambda c, m, p, payload=None: [
            {"head": {"ref": "docs-runner/abc123", "repo": None},
             "base": {"ref": "main"},
             "html_url": "PR"},
        ],
    )
    assert runner.find_open_pr(cfg, "docs-runner/abc123") is None


def test_422_on_create_adopts_the_pr_that_appeared(monkeypatch, tmp_path):
    """Something opened a PR between our check and our create. Adopt it."""
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(runner, "run", lambda cmd, cwd=None, check=True: "")

    lookups = iter([None, "https://github.com/o/w/pull/11"])
    monkeypatch.setattr(runner, "find_open_pr", lambda c, b: next(lookups))

    def _conflict(*args, **kwargs):
        raise urllib.error.HTTPError(
            "https://api.github.com", 422, "Unprocessable", {}, io.BytesIO(b"{}")
        )

    monkeypatch.setattr(runner, "github_api", _conflict)

    url = runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")
    assert url == "https://github.com/o/w/pull/11"


def test_create_failure_that_is_not_a_conflict_raises(monkeypatch, tmp_path):
    cfg = _cfg(monkeypatch)
    monkeypatch.setattr(runner, "run", lambda cmd, cwd=None, check=True: "")
    monkeypatch.setattr(runner, "find_open_pr", lambda c, b: None)

    def _server_error(*args, **kwargs):
        raise urllib.error.HTTPError(
            "https://api.github.com", 500, "Server Error", {}, io.BytesIO(b"{}")
        )

    monkeypatch.setattr(runner, "github_api", _server_error)

    with pytest.raises(RuntimeError, match="could not open pull request"):
        runner.open_pull_request(cfg, tmp_path, [], "docs-runner/abc123")


def test_secrets_are_redacted_from_output():
    runner.register_secret("ghp_supersecrettoken12345")
    leaked = "fatal: https://x-access-token:ghp_supersecrettoken12345@github.com/o/r"
    assert "ghp_supersecrettoken12345" not in runner.redact(leaked)
    assert "***" in runner.redact(leaked)


def test_short_values_are_not_registered_as_secrets():
    """Redacting a 3-character value would mangle unrelated output."""
    runner.register_secret("abc")
    assert runner.redact("abcdef") == "abcdef"


# --------------------------------------------------------------------------
# Evaluation backend must name its model
# --------------------------------------------------------------------------

@pytest.mark.parametrize(
    "model_name,why",
    [
        (None, "absent"),
        ("", "empty"),
        ("   ", "whitespace only"),
    ],
)
def test_openai_compat_refuses_an_unnamed_model(monkeypatch, model_name, why):
    """An unnamed model must fail before a request is ever sent.

    `model` is optional for OpenAI-compatible routers, so an empty value can
    resolve to an account default: another model runs and the harness credits
    the candidate it meant to score. Neither the deploy config nor the remote
    API can be relied on to catch that, so the process boundary does.
    """
    import backends

    monkeypatch.setenv("MODEL_BASE_URL", "https://openrouter.ai/api/v1")
    monkeypatch.delenv("MODEL_NAME", raising=False)
    if model_name is not None:
        monkeypatch.setenv("MODEL_NAME", model_name)

    with pytest.raises(RuntimeError, match="MODEL_NAME"):
        backends.OpenAICompatBackend()


def test_openai_compat_accepts_a_named_model(monkeypatch):
    import backends

    monkeypatch.setenv("MODEL_BASE_URL", "https://openrouter.ai/api/v1")
    monkeypatch.setenv("MODEL_NAME", "  qwen/qwen3-235b-a22b-instruct  ")
    # The client refuses to construct without a credential. That is its own
    # fail-closed behaviour, and not what this test is about.
    monkeypatch.setenv("MODEL_API_KEY", "sk-or-test")
    backend = backends.OpenAICompatBackend()
    assert backend.model == "qwen/qwen3-235b-a22b-instruct", "surrounding space trimmed"
    assert backend.resolved_model is None, "nothing served yet"
