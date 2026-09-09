"""Update the let-go wiki from a let-go commit, then open a pull request.

Run-to-completion batch job. Everything it needs arrives as environment
variables; everything it leaves behind is a pull request and this log.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import textwrap
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from datetime import date
from pathlib import Path

import yaml

import backends

# A commit touching more than this many source files is a merge or a sweeping
# refactor; page-by-page rewriting is the wrong response, so the run reports a
# `skipped` status and stops. It is not an error - see emit_status.
MAX_CHANGED_FILES = 40
# Keep the diff well inside the context window even before page bodies are added.
MAX_DIFF_BYTES = 200_000
MAX_PAGES_PER_RUN = int(os.environ.get("MAX_PAGES_PER_RUN", "5"))

FRONTMATTER = re.compile(r"\A---\n(.*?)\n---\n", re.DOTALL)

# Only these directories hold wiki pages. An allowlist, not a denylist: the repo
# also tracks Markdown under tools/ (agent prompts) and grows support
# directories over time, none of which are pages to be edited.
CONTENT_DIRS = frozenset(
    {"concepts", "entities", "ideas", "projects", "sources", "references"}
)

# Literal secret values to strip from anything printed. Populated at startup.
# On an ephemeral machine the logs are the only artifact that outlives the run,
# so a credential reaching them is a credential published.
_REDACTIONS: list[str] = []


def register_secret(value: str | None) -> None:
    if value and len(value) >= 8:
        _REDACTIONS.append(value)


def redact(text: str) -> str:
    for secret in _REDACTIONS:
        text = text.replace(secret, "***")
    return text


def log(msg: str) -> None:
    # Unbuffered: the logs are the only artifact that survives the machine.
    print(redact(msg), flush=True)


# Reasons this run documented less than the commit contained. Collected as the
# run goes and reported once, on the terminal status line.
_PARTIAL_REASONS: list[str] = []


def note_partial(reason: str, **fields: object) -> None:
    """Record that the run is incomplete, without ending it."""
    _PARTIAL_REASONS.append(reason)
    detail = " ".join(f"{k}={v}" for k, v in fields.items())
    log(f"  incomplete: {reason}{' (' + detail + ')' if detail else ''}")


def emit_status(action: str, **fields: object) -> None:
    """Emit THE machine-readable status line. Call once, at the end of a run.

    Skipping is a legitimate outcome - a 60-file merge commit is not something
    this job should document - so skips exit 0 rather than failing the
    pipeline. That makes the exit code alone too coarse to answer "was this
    commit actually documented?", which is what the caller needs to know.

    Exactly one line is emitted per run, so a caller can read the last one
    without it contradicting an earlier one. Incompleteness rides along on that
    single line as `completeness`, rather than being a separate status that a
    later success would silently overwrite.
    """
    completeness = "partial" if _PARTIAL_REASONS else "complete"
    parts = [f"action={action}", f"completeness={completeness}"]
    if _PARTIAL_REASONS:
        parts.append(f"partial_reasons={','.join(_PARTIAL_REASONS)}")
    parts.extend(f"{k}={v}" for k, v in fields.items())
    log("DOCS_RUNNER_STATUS " + " ".join(parts))


def run(cmd: list[str], cwd: Path | None = None, check: bool = True) -> str:
    result = subprocess.run(
        cmd, cwd=cwd, check=False, capture_output=True, text=True
    )
    if check and result.returncode != 0:
        # Both the command and git's own stderr can carry a tokenised remote
        # URL, so redact both rather than trusting either.
        raise RuntimeError(
            redact(
                f"command failed ({result.returncode}): {' '.join(cmd)}\n"
                f"{result.stderr}"
            )
        )
    return result.stdout


@dataclass(frozen=True)
class Config:
    source_repo: str
    source_sha: str
    wiki_repo: str
    wiki_base: str
    github_token: str
    workdir: Path
    dry_run: bool

    @classmethod
    def from_env(cls) -> "Config":
        dry_run = os.environ.get("DRY_RUN", "").lower() in ("1", "true", "yes")
        required = ["SOURCE_REPO", "SOURCE_SHA", "WIKI_REPO"]
        # A dry run neither pushes nor opens a pull request, and both repos are
        # cloned anonymously, so it has no use for a credential. Requiring one
        # would only invite passing a real token to a run that should never
        # touch GitHub authenticated.
        if not dry_run:
            required.append("GITHUB_TOKEN")
        missing = [k for k in required if not os.environ.get(k)]
        if missing:
            raise RuntimeError(f"missing required env vars: {', '.join(missing)}")
        return cls(
            source_repo=os.environ["SOURCE_REPO"],
            source_sha=os.environ["SOURCE_SHA"],
            wiki_repo=os.environ["WIKI_REPO"],
            wiki_base=os.environ.get("WIKI_BASE_BRANCH", "main"),
            github_token=os.environ.get("GITHUB_TOKEN", ""),
            workdir=Path(os.environ.get("WORKDIR", "/work")),
            dry_run=dry_run,
        )

    def clone_url(self, repo: str) -> str:
        return f"https://x-access-token:{self.github_token}@github.com/{repo}.git"

    @property
    def may_authenticate(self) -> bool:
        """Dry runs stay unauthenticated even if a token happens to be present."""
        return bool(self.github_token) and not self.dry_run


def clone_source(cfg: Config) -> Path:
    """Clone the source repo deep enough to diff the commit against its parent.

    Read-only, and the source repo is normally public, so this tries
    unauthenticated first. That keeps GITHUB_TOKEN scoped to the one repo the
    job actually writes to - the wiki - instead of needing read on this one too.
    """
    dest = cfg.workdir / "source"
    log(f"cloning {cfg.source_repo} @ {cfg.source_sha[:12]}")
    run(["git", "init", "--quiet", str(dest)])

    # depth=2 gives us the commit and its parent, which is all a diff needs.
    fetch = ["git", "fetch", "--quiet", "--depth=2", "origin", cfg.source_sha]
    run(["git", "remote", "add", "origin",
         f"https://github.com/{cfg.source_repo}.git"], cwd=dest)
    anonymous = subprocess.run(
        fetch, cwd=dest, check=False, capture_output=True, text=True
    )
    if anonymous.returncode != 0:
        if not cfg.may_authenticate:
            raise RuntimeError(
                redact(
                    f"could not fetch {cfg.source_repo} anonymously and this run "
                    f"may not authenticate (dry_run={cfg.dry_run}, "
                    f"token={'set' if cfg.github_token else 'unset'}): "
                    f"{anonymous.stderr.strip()}"
                )
            )
        log("  anonymous fetch failed; retrying with GITHUB_TOKEN")
        run(["git", "remote", "set-url", "origin",
             cfg.clone_url(cfg.source_repo)], cwd=dest)
        run(fetch, cwd=dest)

    run(["git", "checkout", "--quiet", "FETCH_HEAD"], cwd=dest)
    return dest


def clone_wiki(cfg: Config) -> Path:
    """Clone the wiki anonymously; the token is attached only to push.

    Cloning is a read, and the wiki is public. Fetching it unauthenticated
    means a dry run touches no credential at all, and the token exists in a
    remote URL for exactly one command (see open_pull_request) instead of for
    the whole run.
    """
    dest = cfg.workdir / "wiki"
    log(f"cloning {cfg.wiki_repo} ({cfg.wiki_base})")
    url = f"https://github.com/{cfg.wiki_repo}.git"
    clone = [
        "git", "clone", "--quiet", "--depth=1",
        "--branch", cfg.wiki_base, url, str(dest),
    ]
    anonymous = subprocess.run(
        clone, check=False, capture_output=True, text=True
    )
    if anonymous.returncode != 0:
        if not cfg.may_authenticate:
            raise RuntimeError(
                redact(
                    f"could not clone {cfg.wiki_repo} anonymously and this run "
                    f"may not authenticate (dry_run={cfg.dry_run}, "
                    f"token={'set' if cfg.github_token else 'unset'}): "
                    f"{anonymous.stderr.strip()}"
                )
            )
        log("  anonymous clone failed; retrying with GITHUB_TOKEN")
        clone[-2] = cfg.clone_url(cfg.wiki_repo)
        run(clone)
    return dest


def commit_context(source: Path, sha: str) -> tuple[list[str], str, str]:
    """Return (changed files, commit subject+body, truncated diff)."""
    has_parent = (
        subprocess.run(
            ["git", "rev-parse", "--verify", "--quiet", f"{sha}^"],
            cwd=source, capture_output=True, text=True,
        ).returncode
        == 0
    )
    range_args = [f"{sha}^", sha] if has_parent else ["--root", sha]

    changed = [
        line for line in run(
            ["git", "diff", "--name-only", *range_args], cwd=source
        ).splitlines() if line.strip()
    ]
    message = run(["git", "log", "-1", "--format=%s%n%n%b", sha], cwd=source).strip()
    diff = run(["git", "diff", *range_args], cwd=source)

    if len(diff.encode()) > MAX_DIFF_BYTES:
        diff = diff.encode()[:MAX_DIFF_BYTES].decode(errors="ignore")
        diff += "\n\n[diff truncated]\n"
    return changed, message, diff


def parse_frontmatter(text: str) -> dict[str, object]:
    """Parse a page's YAML frontmatter.

    Real YAML, not a line scan. A line scan reads `sources:` as an empty string
    whenever the value is a block list on the following lines, which is how a
    good portion of this wiki is written - so genuine citations went missing and
    changed paths were routed to new-page creation instead of to the page that
    already documented them.
    """
    match = FRONTMATTER.match(text)
    if not match:
        return {}
    try:
        data = yaml.safe_load(match.group(1))
    except yaml.YAMLError:
        return {}
    return data if isinstance(data, dict) else {}


def citation_text(fields: dict[str, object]) -> str:
    """Flatten the citation fields to one searchable string.

    `sources` is a list, `resource` a scalar, and either may be absent.
    """
    parts: list[str] = []
    for key in ("resource", "sources", "title"):
        value = fields.get(key)
        if value is None:
            continue
        if isinstance(value, (list, tuple)):
            parts.extend(str(item) for item in value)
        else:
            parts.append(str(value))
    return " ".join(parts)


def is_wiki_content(wiki: Path, page: Path) -> bool:
    """Is this file a wiki page the job may edit?

    Allowlisted by directory. The previous denylist admitted anything it had
    not been told to exclude - tracked prompts under tools/, plus .enrich/ and
    .venv/ in a working checkout - which made non-content Markdown eligible for
    rewriting.
    """
    rel = page.relative_to(wiki)
    # Reserved files live at the root; every real page sits under a content dir.
    return len(rel.parts) > 1 and rel.parts[0] in CONTENT_DIRS


# A path reference has to start and end at a boundary. Without the left one,
# `scripts` matches the tail of `transcripts`; without the right one, `pkg/vm`
# matches `pkg/vmtest`. Both sides are separators, quotes, or string edges.
_LEFT_BOUNDARY = r"(?:^|[\s\"'(,:/])"
_RIGHT_BOUNDARY = r"(?:$|[\s\"'),:])"


def mentions_path(citations: str, candidate: str) -> bool:
    """Is `candidate` named as a path in `citations`, at both boundaries?"""
    return re.search(
        rf"{_LEFT_BOUNDARY}{re.escape(candidate)}/?{_RIGHT_BOUNDARY}", citations
    ) is not None


def page_covers(citations: str, path: str) -> bool:
    """Does a page's citations claim this source path?

    The full path is the reliable signal. A bare ancestor directory is not:
    `docs/design` appears in every citation naming anything under it, which
    would make one new design doc look like it changed every page in the
    directory. An ancestor only counts when the citation names it as the
    subject - i.e. the reference ends there (a page about `pkg/vm`).
    """
    if mentions_path(citations, path):
        return True
    parts = Path(path).parts
    return any(
        mentions_path(citations, "/".join(parts[:i]))
        for i in range(1, len(parts))
    )


def map_pages(wiki: Path, changed: list[str]) -> tuple[list[Path], list[str]]:
    """Split the commit into (pages to edit, source paths nothing documents).

    The wiki's own schema requires every page to cite where its claims come
    from (`resource:` / `sources:`), so those citations are the mapping - no
    separate index to keep in sync.
    """
    hits: dict[Path, int] = {}
    covered: set[str] = set()

    for page in sorted(wiki.rglob("*.md")):
        if not is_wiki_content(wiki, page):
            continue
        try:
            text = page.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue
        citations = citation_text(parse_frontmatter(text))
        matched = [p for p in changed if page_covers(citations, p)]
        if matched:
            hits[page] = len(matched)
            covered.update(matched)

    # Second chance by filename: a page about WASM may cite `wasm/` while the
    # commit touches `wasm.go`. Citation matching misses that, and creating a
    # second WASM page is worse than editing the one that exists.
    leftover = [p for p in changed if p not in covered and is_documentable(p)]
    uncovered: list[str] = []
    for path in leftover:
        by_topic = page_by_topic(wiki, path)
        if by_topic is None:
            uncovered.append(path)
        else:
            hits[by_topic] = hits.get(by_topic, 0) + 1

    ranked = sorted(hits.items(), key=lambda pair: (-pair[1], str(pair[0])))
    to_edit = [page for page, _ in ranked[:MAX_PAGES_PER_RUN]]
    return to_edit, uncovered


# Two- and three-letter tokens ("ir", "vm") collide with too many slugs to be
# evidence of anything.
MIN_TOPIC_TOKEN = 4


def topic_tokens(name: str) -> set[str]:
    return {t for t in re.split(r"[-_. ]+", name.lower()) if len(t) >= MIN_TOPIC_TOKEN}


def page_by_topic(wiki: Path, path: str) -> Path | None:
    """Find an existing page whose slug names the same subject as this file."""
    tokens = topic_tokens(Path(path).stem)
    if not tokens:
        return None
    for page in sorted(wiki.rglob("*.md")):
        if not is_wiki_content(wiki, page):
            continue
        if tokens & topic_tokens(page.stem):
            return page
    return None


# Files that describe architecture. Everything else in a commit (CI config,
# lockfiles, fixtures) is noise for a wiki whose purpose is documenting design.
DOCUMENTABLE_SUFFIXES = (".go", ".md", ".lg", ".clj")
SKIP_PATH_PARTS = (
    ".github", "testdata", "vendor", "node_modules", "bench", "examples",
)


def is_documentable(path: str) -> bool:
    parts = Path(path).parts
    if any(part in SKIP_PATH_PARTS for part in parts):
        return False
    if path.endswith(("_test.go", ".sum", ".mod", ".lock")):
        return False
    # Navigation and project meta, not architecture.
    if Path(path).name.upper() in (
        "README.MD", "CHANGELOG.MD", "CONTRIBUTING.MD", "LICENSE.MD", "AGENTS.MD",
    ):
        return False
    if parts and parts[0] in ("docs",) and not path.endswith(".md"):
        return False
    return path.endswith(DOCUMENTABLE_SUFFIXES)


SYSTEM_PROMPT = """\
You maintain an LLM wiki about let-go, a Clojure dialect on a Go bytecode VM.

The repository's AGENTS.md is the canonical schema and is reproduced below. \
Follow it exactly: frontmatter fields, file-relative cross-links, citation \
requirements, and status values.

Rules for this task:
- Update the page ONLY where the commit actually changes what the page says. \
  An unchanged page is a valid outcome.
- Never drop existing citations or whole sections.
- Keep `status` as-is unless you verified the change against the code shown.
- Bump `updated` to the date given.
- Tags MUST come from the tag taxonomy reproduced below. Max 5.
- Return the COMPLETE page, starting with the `---` frontmatter delimiter. \
  Return nothing else: no preamble, no explanation, no code fence around it.
- If no update is warranted, return exactly: NO CHANGE

AGENTS.md follows.
---
{agents}

_meta/taxonomy.md (the ONLY permitted tags) follows.
---
{taxonomy}
"""

USER_PROMPT = """\
Commit {sha} in {repo}.

Commit message:
{message}

Changed files:
{changed}

Diff:
```
{diff}
```

Today's date: {today}

Current contents of `{page}`:
```
{body}
```

Update that page for this commit."""


def update_page(
    backend: backends.Backend,
    page: Path,
    wiki: Path,
    agents: str,
    taxonomy: str,
    cfg: Config,
    changed: list[str],
    message: str,
    diff: str,
) -> bool:
    rel = page.relative_to(wiki)
    body = page.read_text(encoding="utf-8")
    log(f"  asking {backend.name} about {rel}")

    reply = backend.complete(
        system=SYSTEM_PROMPT.format(agents=agents, taxonomy=taxonomy),
        user=USER_PROMPT.format(
            sha=cfg.source_sha, repo=cfg.source_repo, message=message,
            changed="\n".join(f"- {c}" for c in changed), diff=diff,
            today=date.today().isoformat(), page=rel, body=body,
        ),
    ).strip()

    if reply == "NO CHANGE" or not reply:
        log(f"  {rel}: no change needed")
        return False
    if not reply.startswith("---"):
        log(f"  {rel}: SKIPPED, reply did not start with frontmatter")
        return False
    if reply == body.strip():
        log(f"  {rel}: identical to current content")
        return False

    page.write_text(reply + "\n", encoding="utf-8")
    log(f"  {rel}: updated")
    return True


CREATE_SYSTEM_PROMPT = """\
You maintain an LLM wiki about let-go, a Clojure dialect on a Go bytecode VM.
Its purpose is documenting the architecture.

The repository's AGENTS.md is the canonical schema and is reproduced below. \
Follow it exactly: frontmatter fields, directory choice, file-relative \
cross-links, citation requirements, and status values.

You are deciding whether a source file introduced by a commit deserves its own \
new wiki page, and if so, writing it.

Rules:
- Only propose a page for something architecturally meaningful: a subsystem, a \
  design decision, a concept. Not for a bug fix, a CI tweak, or a test.
- New pages are drafted from a diff, so `status` MUST be `speculative`.
- Cite the source file in `resource` and `sources`.
- Set both `created` and `updated` to the date given.
- Choose the directory per AGENTS.md: concepts/ (how it works), entities/ (the \
  things), projects/, ideas/, references/.
- Tags MUST come from the tag taxonomy reproduced below. Max 5.
- Respond with EXACTLY this shape and nothing else:

PATH: <directory>/<slug>.md
SUMMARY: <one sentence, used verbatim in the index; no trailing period needed>
<full page content, beginning with its own `---` frontmatter delimiter on the \
very next line. Do NOT put a separator line before it.>

- If no page is warranted, respond with exactly: NO PAGE

AGENTS.md follows.
---
{agents}

_meta/taxonomy.md (the ONLY permitted tags) follows.
---
{taxonomy}
"""

CREATE_USER_PROMPT = """\
Commit {sha} in {repo}.

Commit message:
{message}

This file was added or changed and no existing wiki page cites it:
`{path}`

Its current contents:
```
{contents}
```

Diff for the whole commit, for context:
```
{diff}
```

Existing wiki pages (do not duplicate one of these):
{existing}

Today's date: {today}

Decide whether `{path}` warrants a new wiki page, and write it if so."""

CREATE_REPLY = re.compile(
    r"\APATH:\s*(?P<path>\S+)\s*\nSUMMARY:\s*(?P<summary>.+?)\s*\n(?P<body>---\n.*)\Z",
    re.DOTALL,
)
# Reading a whole source file into the prompt is fine for design docs but not
# for a 5k-line generated file; cap it.
MAX_SOURCE_BYTES = 60_000
MAX_NEW_PAGES_PER_RUN = int(os.environ.get("MAX_NEW_PAGES_PER_RUN", "2"))


def create_page(
    backend: backends.Backend,
    path: str,
    source: Path,
    wiki: Path,
    agents: str,
    taxonomy: str,
    cfg: Config,
    message: str,
    diff: str,
) -> Path | None:
    """Draft a brand-new wiki page for an undocumented source path."""
    source_file = source / path
    if not source_file.exists():
        log(f"  {path}: deleted in this commit, no page to create")
        return None
    try:
        contents = source_file.read_text(encoding="utf-8")
    except (UnicodeDecodeError, OSError):
        log(f"  {path}: unreadable, skipping")
        return None
    if len(contents.encode()) > MAX_SOURCE_BYTES:
        contents = contents.encode()[:MAX_SOURCE_BYTES].decode(errors="ignore")
        contents += "\n\n[file truncated]\n"

    existing = "\n".join(
        f"- {p.relative_to(wiki)}"
        for p in sorted(wiki.rglob("*.md"))
        if is_wiki_content(wiki, p)
    )

    log(f"  asking {backend.name} whether {path} needs a new page")
    reply = backend.complete(
        system=CREATE_SYSTEM_PROMPT.format(agents=agents, taxonomy=taxonomy),
        user=CREATE_USER_PROMPT.format(
            sha=cfg.source_sha, repo=cfg.source_repo, message=message,
            path=path, contents=contents, diff=diff, existing=existing,
            today=date.today().isoformat(),
        ),
    ).strip()

    if reply == "NO PAGE" or not reply:
        log(f"  {path}: no new page warranted")
        return None

    match = CREATE_REPLY.match(reply)
    if not match:
        log(f"  {path}: SKIPPED, reply did not match the PATH/INDEX/body shape")
        return None

    rel = Path(match.group("path").strip().lstrip("/"))
    # The model chooses the path; make sure its choice stays inside the wiki.
    target = (wiki / rel).resolve()
    if not target.is_relative_to(wiki.resolve()) or target.suffix != ".md":
        log(f"  {path}: SKIPPED, refused page path {rel}")
        return None
    if target.exists():
        log(f"  {path}: SKIPPED, {rel} already exists")
        return None

    body = match.group("body").strip()
    # Belt and braces: an empty leading frontmatter block means the model still
    # emitted a separator ahead of the page's own `---`.
    if body.startswith("---\n---\n"):
        body = body[4:]

    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(body + "\n", encoding="utf-8")
    add_index_entry(wiki, rel, match.group("summary").strip())
    log(f"  {path}: created {rel}")
    return target


def add_index_entry(wiki: Path, rel: Path, summary: str) -> None:
    """Add the page's catalog line to index.md, under its category if we can find it.

    AGENTS.md documents this line as `[[path/slug]] — summary`, but index.md is
    written with ordinary markdown links and check_wiki.py's orphan detector
    only counts those - a page added in the documented form is reported as an
    orphan. Follow the file, not the doc.
    """
    index = wiki / "index.md"
    if not index.exists():
        return
    slug = rel.with_suffix("").as_posix()
    entry = f"- [{slug}]({rel.as_posix()}) — {summary}"
    lines = index.read_text(encoding="utf-8").splitlines()
    category = rel.parts[0].rstrip("s") if rel.parts else ""

    # Find the heading for this category, then the last list item beneath it.
    start = next(
        (
            i for i, line in enumerate(lines)
            if line.startswith("#") and category and category in line.lower()
        ),
        None,
    )
    if start is None:
        lines.append(entry)
    else:
        insert = start + 1
        for i in range(start + 1, len(lines)):
            if lines[i].startswith("#"):
                break
            if lines[i].strip():
                insert = i + 1
        lines.insert(insert, entry)

    index.write_text("\n".join(lines) + "\n", encoding="utf-8")


def append_log(wiki: Path, cfg: Config, updated: list[Path]) -> None:
    """The wiki keeps an append-only log.md; every op is expected to land there."""
    log_file = wiki / "log.md"
    if not log_file.exists():
        return
    pages = ", ".join(str(p.relative_to(wiki)) for p in updated)
    entry = (
        f"\n## [{date.today().isoformat()}] docs-runner | {cfg.source_sha[:12]}\n\n"
        f"Updated from `{cfg.source_repo}@{cfg.source_sha[:12]}`: {pages}\n"
    )
    with log_file.open("a", encoding="utf-8") as handle:
        handle.write(entry)


# check_wiki.py exits 0 when clean and 1 when it has complaints. Any other
# code, or a traceback, means the validator itself broke.
VALIDATOR_EXIT_CODES = (0, 1)


class ValidatorUnavailable(RuntimeError):
    """The validator could not produce a usable verdict.

    Distinct from "the validator found problems". Unavailable is NOT a pass:
    treating it as one is how invalid pages reach a pull request.
    """


def validate(wiki: Path) -> list[str]:
    """Run the wiki's own validator and return its complaint lines.

    Raises ValidatorUnavailable if it could not run or its result cannot be
    trusted. Callers must not read that as a clean pass.
    """
    checker = wiki / "tools" / "check_wiki.py"
    if not checker.exists():
        raise ValidatorUnavailable(f"{checker.relative_to(wiki)} is not in the wiki")

    # Takes the wiki root as argv[1]; see its main().
    result = subprocess.run(
        [sys.executable, str(checker), "."],
        cwd=wiki, check=False, capture_output=True, text=True,
    )
    if result.returncode not in VALIDATOR_EXIT_CODES or "Traceback" in result.stderr:
        tail = (result.stderr.strip().splitlines() or ["no stderr"])[-1]
        raise ValidatorUnavailable(f"exited {result.returncode}: {tail}")

    complaints = [
        line for line in result.stdout.splitlines()
        if line.strip() and line.strip() != "check_wiki: OK"
    ]
    # A nonzero exit with nothing to show for it means the verdict is not
    # something we can compare against a baseline.
    if result.returncode != 0 and not complaints:
        raise ValidatorUnavailable(
            f"exited {result.returncode} with no parseable output"
        )
    return complaints


def new_validation_errors(before: list[str], after: list[str]) -> list[str]:
    """Complaints this run introduced, ignoring ones the wiki arrived with.

    Blocking on the wiki's total validator state would make the job hostage to
    pre-existing breakage it did not cause and cannot fix.
    """
    baseline = set(before)
    return [line for line in after if line not in baseline]


def commit_branch(cfg: Config, wiki: Path) -> str:
    """Commit the generated changes to a local branch. No network.

    Separate from open_pull_request so a run that fails validation still leaves
    an inspectable branch behind instead of discarding the work.
    """
    branch = f"docs-runner/{cfg.source_sha[:12]}"
    run(["git", "config", "user.name", "let-go docs runner"], cwd=wiki)
    run(["git", "config", "user.email", "docs-runner@users.noreply.github.com"], cwd=wiki)
    run(["git", "checkout", "--quiet", "-b", branch], cwd=wiki)
    run(["git", "add", "--all"], cwd=wiki)
    run(
        ["git", "commit", "--quiet", "-m",
         f"docs: update wiki for {cfg.source_repo}@{cfg.source_sha[:12]}"],
        cwd=wiki,
    )
    return branch


def github_api(
    cfg: Config, method: str, path: str, payload: dict | None = None
) -> object:
    """Call the GitHub REST API and return the decoded body."""
    request = urllib.request.Request(
        f"https://api.github.com{path}",
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={
            "Authorization": f"Bearer {cfg.github_token}",
            "Accept": "application/vnd.github+json",
            "Content-Type": "application/json",
            "User-Agent": "let-go-docs-runner",
        },
        method=method,
    )
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.load(response)


def parse_ls_remote_sha(output: str) -> str | None:
    """First column of `git ls-remote` output, or None when nothing matched."""
    for line in output.splitlines():
        parts = line.split()
        if len(parts) >= 2:
            return parts[0]
    return None


# How many branch names to try before giving up. Each failed run that pushed
# but could not open a pull request consumes one.
MAX_BRANCH_ATTEMPTS = 5


def branch_candidates(base_name: str) -> list[str]:
    return [base_name] + [
        f"{base_name}-{n}" for n in range(2, MAX_BRANCH_ATTEMPTS + 1)
    ]


def find_open_pr(cfg: Config, base_name: str) -> str | None:
    """URL of an open PR for this commit's branch, or any retry of it.

    Matches the whole `docs-runner/<sha>` family, not one exact ref, because a
    previous run that could not open a pull request may have published from a
    suffixed branch. Filtered to our own base: the same head can have a pull
    request open against a different base branch, and adopting that one would
    report success for something we are not proposing.
    """
    try:
        results = github_api(
            cfg,
            "GET",
            f"/repos/{cfg.wiki_repo}/pulls"
            f"?state=open&base={urllib.parse.quote(cfg.wiki_base)}&per_page=100",
        )
    except urllib.error.HTTPError as err:
        # Not being able to check is not the same as there being none; say so
        # rather than letting the caller open a second pull request.
        raise RuntimeError(
            f"could not check for an existing pull request ({err.code})"
        ) from err
    if not isinstance(results, list):
        return None

    wanted = set(branch_candidates(base_name))
    for pull in results:
        head = pull.get("head") or {}
        base = pull.get("base") or {}
        # The branch name alone does not identify a pull request. The wiki is
        # public, so anyone may open `theirfork:docs-runner/<sha>` against our
        # base; adopting that would report success and quietly never publish
        # this run's work. `head.repo` is null when the fork has been deleted,
        # which is also not ours.
        head_repo = (head.get("repo") or {}).get("full_name")
        if (
            head.get("ref") in wanted
            and base.get("ref") == cfg.wiki_base
            and head_repo == cfg.wiki_repo
        ):
            return str(pull.get("html_url", ""))
    return None


def remote_branch_exists(cfg: Config, wiki: Path, name: str) -> bool:
    return parse_ls_remote_sha(
        run(["git", "ls-remote", "--heads", "origin", name], cwd=wiki)
    ) is not None


def choose_remote_branch(cfg: Config, wiki: Path, base_name: str) -> str:
    """First branch name in the family that does not exist on the remote.

    Never reuses an occupied name. An earlier design force-pushed over an
    existing branch under a lease, but a lease only checks the ref's SHA, and
    opening a pull request does not change a SHA - so a branch that acquired a
    pull request between the check and the push would still be overwritten
    while under review. Publishing to a fresh name cannot do that to anyone.
    """
    for candidate in branch_candidates(base_name):
        if not remote_branch_exists(cfg, wiki, candidate):
            return candidate
    raise RuntimeError(
        f"all {MAX_BRANCH_ATTEMPTS} branch names for {base_name} are taken on "
        "the remote and none has an open pull request; clean them up by hand"
    )


def open_pull_request(
    cfg: Config, wiki: Path, updated: list[Path], branch: str
) -> str:
    """Push the branch and open a pull request, safely on a re-run.

    Re-running the same commit used to collide: a run that pushed and then
    failed to open the PR left a branch behind that the next push rejected.
    Both steps are now idempotent - an existing PR is adopted rather than
    duplicated, and a leftover branch with no PR is updated rather than fought.
    """
    pages = "\n".join(f"- `{p.relative_to(wiki)}`" for p in updated)

    if cfg.dry_run:
        log(f"DRY_RUN set: built branch {branch} but not pushing")
        log(run(["git", "show", "--stat", "HEAD"], cwd=wiki))
        return ""

    # The remote was cloned anonymously; attach the token only now, so it lives
    # in a remote URL for the push alone rather than for the whole run.
    run(["git", "remote", "set-url", "origin",
         cfg.clone_url(cfg.wiki_repo)], cwd=wiki)

    # A pull request already open for this commit means a previous run got this
    # far. Adopt it: opening a second pull request for the same commit, or
    # touching the branch behind one, are both worse than doing nothing.
    existing = find_open_pr(cfg, branch)
    if existing:
        log(f"pull request already open for {branch}; leaving it alone")
        return existing

    # No pull request, but the branch may still exist from a run that pushed and
    # then failed. Publish to the first free name instead of overwriting it.
    remote_branch = choose_remote_branch(cfg, wiki, branch)
    if remote_branch != branch:
        log(f"branch {branch} is taken; publishing as {remote_branch}")
    run(
        ["git", "push", "--quiet", "origin",
         f"HEAD:refs/heads/{remote_branch}"],
        cwd=wiki,
    )
    branch = remote_branch

    body = textwrap.dedent(f"""\
        Generated by the let-go docs runner from `{cfg.source_repo}@{cfg.source_sha[:12]}`.

        Pages updated:
        {pages}

        Review before merging: pages are drafted from the commit diff and may
        overstate what the change actually does. Check the citations.
    """)
    try:
        created = github_api(
            cfg, "POST", f"/repos/{cfg.wiki_repo}/pulls",
            {
                "title": f"docs: wiki update for {cfg.source_sha[:12]}",
                "head": branch,
                "base": cfg.wiki_base,
                "body": body,
            },
        )
    except urllib.error.HTTPError as err:
        # 422 here is usually "a pull request already exists for this head" -
        # something opened one between the check above and now. Adopt it rather
        # than failing a run whose work is already proposed.
        if err.code == 422:
            adopted = find_open_pr(cfg, branch)
            if adopted:
                log(f"pull request appeared while opening one; adopting {adopted}")
                return adopted
        raise RuntimeError(
            f"could not open pull request ({err.code}): "
            f"{err.read().decode(errors='ignore')}"
        ) from err

    return str(created["html_url"]) if isinstance(created, dict) else ""


def main() -> int:
    cfg = Config.from_env()
    register_secret(cfg.github_token)
    register_secret(os.environ.get("ANTHROPIC_API_KEY"))
    register_secret(os.environ.get("MODEL_API_KEY"))
    cfg.workdir.mkdir(parents=True, exist_ok=True)

    source = clone_source(cfg)
    changed, message, diff = commit_context(source, cfg.source_sha)
    log(f"commit touches {len(changed)} file(s)")
    if not changed:
        emit_status("skipped", reason="no-files-changed", sha=cfg.source_sha[:12])
        return 0
    if len(changed) > MAX_CHANGED_FILES:
        emit_status(
            "skipped", reason="too-many-files", sha=cfg.source_sha[:12],
            changed=len(changed), limit=MAX_CHANGED_FILES,
        )
        return 0
    if "[diff truncated]" in diff:
        # The model saw a partial commit, so the pages it wrote were informed by
        # a partial commit. Say so rather than letting the PR imply otherwise.
        note_partial("diff-truncated", limit_bytes=MAX_DIFF_BYTES)

    wiki = clone_wiki(cfg)
    agents_file = wiki / "AGENTS.md"
    if not agents_file.exists():
        raise RuntimeError(f"{cfg.wiki_repo} has no AGENTS.md; refusing to guess the schema")
    agents = agents_file.read_text(encoding="utf-8")

    taxonomy_file = wiki / "_meta" / "taxonomy.md"
    if not taxonomy_file.exists():
        raise RuntimeError(
            f"{cfg.wiki_repo} has no _meta/taxonomy.md; tags would fail validation"
        )
    taxonomy = taxonomy_file.read_text(encoding="utf-8")

    # Baseline the validator before touching anything, so the comparison later
    # can tell "we broke this" apart from "it arrived broken".
    try:
        baseline = validate(wiki)
    except ValidatorUnavailable as err:
        # Fail closed. An unverifiable page is not a verified one, and this job
        # exists to open pull requests other people will trust.
        log(f"validator unavailable: {err}")
        emit_status(
            "failed", reason="validator-unavailable", sha=cfg.source_sha[:12],
        )
        return 1
    if baseline:
        log(f"wiki has {len(baseline)} pre-existing validator complaint(s)")

    to_edit, uncovered = map_pages(wiki, changed)
    log(
        f"pages citing changed paths: "
        f"{', '.join(str(p.relative_to(wiki)) for p in to_edit) or '(none)'}"
    )
    log(f"changed paths no page documents: {', '.join(uncovered) or '(none)'}")
    if not to_edit and not uncovered:
        emit_status("skipped", reason="nothing-to-document", sha=cfg.source_sha[:12])
        return 0

    if len(to_edit) == MAX_PAGES_PER_RUN:
        note_partial("page-cap-reached", limit=MAX_PAGES_PER_RUN)
    if len(uncovered) > MAX_NEW_PAGES_PER_RUN:
        note_partial(
            "new-page-cap-reached",
            candidates=len(uncovered), limit=MAX_NEW_PAGES_PER_RUN,
        )

    backend = backends.load()

    updated = [
        page for page in to_edit
        if update_page(
            backend, page, wiki, agents, taxonomy, cfg, changed, message, diff
        )
    ]

    for path in uncovered[:MAX_NEW_PAGES_PER_RUN]:
        created = create_page(
            backend, path, source, wiki, agents, taxonomy, cfg, message, diff
        )
        if created:
            updated.append(created)

    if not updated:
        emit_status("no-change", reason="model-proposed-nothing",
                    sha=cfg.source_sha[:12])
        return 0

    append_log(wiki, cfg, updated)

    # Commit first: whatever the validator says, the work stays inspectable on
    # a local branch.
    branch = commit_branch(cfg, wiki)

    try:
        after = validate(wiki)
    except ValidatorUnavailable as err:
        log(f"validator unavailable after generating pages: {err}")
        emit_status(
            "failed", reason="validator-unavailable", sha=cfg.source_sha[:12],
            branch=branch,
        )
        return 1

    introduced = new_validation_errors(baseline, after)
    if introduced:
        # Do not propose pages the wiki's own validator rejects.
        for line in introduced:
            log(f"  validator: {line}")
        emit_status(
            "failed", reason="validation-regressed", sha=cfg.source_sha[:12],
            branch=branch, new_errors=len(introduced),
        )
        return 1

    url = open_pull_request(cfg, wiki, updated, branch)
    if url:
        emit_status("documented", sha=cfg.source_sha[:12],
                    pages=len(updated), pr=url)
        log(f"opened {url}")
    else:
        emit_status("dry-run", sha=cfg.source_sha[:12], pages=len(updated))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as err:  # noqa: BLE001 - top-level job boundary
        log(f"FAILED: {err}")
        sys.exit(1)
