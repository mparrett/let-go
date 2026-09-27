"""pipelines/wiki_check.py: the wiki validator as a gate inside the loop."""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import backends  # noqa: E402
import runner  # noqa: E402

_spec = importlib.util.spec_from_file_location(
    "wiki_check", HERE.parent / "pipelines" / "wiki_check.py"
)
assert _spec and _spec.loader
wiki_check = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(wiki_check)

# Stands in for the wiki's validator: a page containing BAD is invalid, and a
# page index.md does not mention is an orphan. Same output shape and exit codes
# as the real tools/check_wiki.py, including its quirk of naming orphans by
# absolute path when given `.` as the root.
STUB_CHECKER = '''
import sys
from pathlib import Path
root = Path(sys.argv[1])
index = (root / "index.md").read_text()
out = []
for p in sorted(root.rglob("*.md")):
    rel = p.relative_to(root).as_posix()
    if rel == "index.md":
        continue
    if "BAD" in p.read_text():
        out.append(f"{rel}: bad content")
    if rel not in index:
        out.append(f"{p.resolve()}: orphan page (no inbound links; add it to index.md)")
print("\\n".join(out) if out else "check_wiki: OK")
sys.exit(1 if out else 0)
'''


@pytest.fixture
def wiki(tmp_path):
    root = tmp_path / "wiki"
    (root / "tools").mkdir(parents=True)
    (root / "tools" / "check_wiki.py").write_text(STUB_CHECKER)
    (root / "concepts").mkdir()
    (root / "concepts" / "vm.md").write_text("---\ntitle: vm\n---\nThe VM.\n")
    (root / "index.md").write_text("- concepts/vm.md\n")
    return root


@pytest.fixture
def run(tmp_path, monkeypatch, wiki):
    """Run the gate in a fresh page directory, as the graph does."""
    work = tmp_path / "page"
    (work / "state").mkdir(parents=True)
    monkeypatch.chdir(work)

    def go(answer: str, target: str = "", bind: bool = True) -> tuple[int, str]:
        if bind:
            (work / "task.wiki").write_text(str(wiki))
            (work / "task.target").write_text(target)
        (work / "state" / "answer.md").write_text(answer)
        import contextlib
        import io
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            code = wiki_check.main()
        return code, buf.getvalue()

    go.work = work  # type: ignore[attr-defined]
    return go


def test_skips_when_the_backend_was_given_no_wiki(run):
    assert run("---\ntitle: x\n---\nBAD\n", bind=False) == (0, "skip")


def test_a_declined_edit_passes(run):
    assert run("NO CHANGE", target="concepts/vm.md") == (0, "green")
    assert run("NO PAGE") == (0, "green")


def test_a_clean_update_passes(run):
    answer = "---\ntitle: vm\n---\nStill the VM.\n"
    assert run(answer, target="concepts/vm.md") == (0, "green")


def test_a_rejected_update_goes_back_with_the_complaint(run, wiki):
    code, out = run("---\ntitle: vm\n---\nBAD\n", target="concepts/vm.md")
    assert (code, out) == (1, "red")
    log = (run.work / "state" / "contract.log").read_text()
    assert "- concepts/vm.md: bad content" in log
    # Judged on the copy: the checkout runner.py commits from is untouched.
    assert "BAD" not in (wiki / "concepts" / "vm.md").read_text()


def test_complaints_the_wiki_already_had_do_not_count(run, wiki):
    (wiki / "concepts" / "old.md").write_text("BAD from before\n")
    assert run("---\ntitle: vm\n---\nFine.\n", target="concepts/vm.md") == (0, "green")


def test_a_new_page_is_not_an_orphan_before_runner_indexes_it(run):
    answer = "PATH: concepts/new.md\nSUMMARY: new\n---\ntitle: new\n---\nBody.\n"
    assert run(answer) == (0, "green")


def test_a_new_page_under_a_changed_path_does_not_leave_the_old_one(run):
    run("PATH: concepts/first.md\nSUMMARY: s\n---\ntitle: a\n---\nBAD\n")
    second = "PATH: concepts/second.md\nSUMMARY: s\n---\ntitle: a\n---\nFine.\n"
    assert run(second) == (0, "green")
    assert not (run.work / "state" / "wiki" / "concepts" / "first.md").exists()


def test_a_path_outside_the_wiki_is_refused(run):
    code, out = run("PATH: ../escape.md\nSUMMARY: s\n---\ntitle: a\n---\nBody.\n")
    assert (code, out) == (1, "red")
    log = (run.work / "state" / "contract.log").read_text()
    assert "not a path inside the wiki" in log


def test_the_backend_finds_the_target_in_the_real_user_prompt():
    """TARGET_LINE and runner.USER_PROMPT must agree; nothing else links them."""
    prompt = runner.USER_PROMPT.format(
        sha="s", repo="r", message="m", changed="- c", diff="d",
        today="t", page="concepts/vm.md", body="b",
    )
    match = backends.AttractorBackend.TARGET_LINE.search(prompt)
    assert match and match.group(1) == "concepts/vm.md"


def test_a_bound_backend_hands_the_gate_its_inputs(monkeypatch, tmp_path, wiki):
    seen = tmp_path / "seen"
    fake = tmp_path / "fake-attractor"
    fake.write_text(
        "#!/bin/sh\n"
        f'cat task.wiki > "{seen}.wiki"; cat task.target > "{seen}.target"\n'
        f'echo "$DOCS_WIKI_CHECK" > "{seen}.check"\n'
        "echo 'NO CHANGE' > state/answer.md\n"
    )
    fake.chmod(0o755)
    monkeypatch.setenv("ATTRACTOR_BIN", str(fake))
    backend = backends.AttractorBackend()
    backend.bind_wiki(wiki)
    user = runner.USER_PROMPT.format(
        sha="s", repo="r", message="m", changed="- c", diff="d",
        today="t", page="concepts/vm.md", body="b",
    )
    assert backend.complete(system="s", user=user) == "NO CHANGE\n"
    assert Path(f"{seen}.wiki").read_text() == str(wiki.resolve())
    assert Path(f"{seen}.target").read_text() == "concepts/vm.md"
    assert Path(f"{seen}.check").read_text().strip().endswith("pipelines/wiki_check.py")


def test_a_successful_run_logs_its_path_and_whether_the_gate_validated(
    monkeypatch, tmp_path, wiki, capsys
):
    fake = tmp_path / "fake-attractor"
    fake.write_text(
        "#!/bin/sh\n"
        "echo \"  ✓ Stage 'start' completed -> :success\"\n"
        "echo \"  ✓ Stage 'draft' completed -> :success\"\n"
        "echo \"  ✗ Stage 'contract' failed!\"\n"
        "echo \"  ✓ Stage 'repair' completed -> :success\"\n"
        "echo \"  ✓ Stage 'wiki_check' completed -> :success\"\n"
        "printf 'a: old complaint\\n' > state/wiki-baseline.txt\n"
        "printf -- '---\\ntitle: vm\\n---\\nBody.\\n' > state/answer.md\n"
    )
    fake.chmod(0o755)
    monkeypatch.setenv("ATTRACTOR_BIN", str(fake))
    backend = backends.AttractorBackend()
    backend.bind_wiki(wiki)
    backend.complete(system="s", user="u")
    out = capsys.readouterr().out
    assert "attractor path: draft > contract! > repair > wiki_check" in out
    assert "wiki_check: validator ran (1 pre-existing complaint(s) ignored)" in out


def test_an_abandoned_page_reports_why(monkeypatch, tmp_path):
    fake = tmp_path / "fake-attractor"
    fake.write_text(
        "#!/bin/sh\n"
        "echo 'the reply begins with neither --- nor PATH:' > state/contract.log\n"
        "printf 'NO CHANGE.\\n' > state/answer.md\n"
        "echo 'ABANDONED'\n"
        "exit 1\n"
    )
    fake.chmod(0o755)
    monkeypatch.setenv("ATTRACTOR_BIN", str(fake))
    with pytest.raises(RuntimeError) as err:
        backends.AttractorBackend().complete(system="s", user="u")
    message = str(err.value)
    assert "last gate complaint: the reply begins with neither" in message
    assert "answer.md: 11 bytes, begins 'NO CHANGE.'" in message
