"""The wiki's own validator, as a gate inside the attractor page loop.

The contract gate in page-update.dot checks the reply's shape. This checks what
it says, with the one verifier in the whole system that is not a model:
the wiki's tools/check_wiki.py. runner.py already runs it, but once, after every
page is written, and a complaint there fails the whole run. Here a complaint
goes back to the model as a repair request and costs one iteration instead.

Run by the graph from the page's working directory. Inputs, all written by
backends.AttractorBackend:
  task.wiki    the wiki checkout's root (absent: the gate is skipped)
  task.target  the page being updated, relative to that root; empty when the
               task is creating a page, whose path comes from its PATH: line
Prints `green`, `red` or `skip` for the graph's edge conditions, and on red
exits 1 with the complaints in state/contract.log, which the repair stage reads.

The validator runs against a private copy of the wiki, taken once per page, so
nothing here can leave a stray edit in the checkout runner.py commits from.
Only complaints the copy did not already have count: the wiki's pre-existing
state is not this page's problem, the same rule runner.py applies.
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path, PurePosixPath

STATE = Path("state")
COPY = STATE / "wiki"
BASELINE = STATE / "wiki-baseline.txt"
WRITTEN = STATE / "wiki-written"
CONTRACT_LOG = STATE / "contract.log"

# Everything check_wiki.py never reads. The container's clone has no .venv or
# built site, but a local run against a developer's checkout does.
IGNORE = shutil.ignore_patterns(".git", ".venv", "site", "__pycache__")


def complaints(root: Path) -> list[str] | None:
    """check_wiki.py's complaint lines, or None when it could not judge.

    Advisories are not complaints; runner.py ignores them too.
    """
    result = subprocess.run(
        [sys.executable, "tools/check_wiki.py", "."],
        cwd=root, capture_output=True, text=True, check=False,
    )
    if result.returncode not in (0, 1) or "Traceback" in result.stderr:
        return None
    # Given `.` as its root, check_wiki.py names orphans by absolute path
    # (Path(".") is never among a resolved file's parents). Relative names keep
    # the complaints comparable and keep a scratch path out of the model's
    # repair request.
    prefix = f"{root.resolve()}/"
    return [
        line.replace(prefix, "") for line in result.stdout.splitlines()
        if line.strip()
        and line.strip() != "check_wiki: OK"
        and not line.startswith("advisory:")
    ]


def target_and_page(answer: str, target: str) -> tuple[str, str] | None:
    """Where the answer would land, and the page text that would land there."""
    lines = answer.splitlines()
    if lines and lines[0].startswith("PATH:"):
        rel = lines[0].removeprefix("PATH:").strip()
        try:
            start = lines.index("---")
        except ValueError:
            return None  # the contract gate reports a missing page body
        return rel, "\n".join(lines[start:]) + "\n"
    if not target:
        return None
    return target, answer.rstrip("\n") + "\n"


def safe_relative(rel: str) -> bool:
    path = PurePosixPath(rel)
    return bool(rel) and not path.is_absolute() and ".." not in path.parts


def main() -> int:
    wiki_file = Path("task.wiki")
    if not wiki_file.is_file() or not wiki_file.read_text().strip():
        print("skip", end="")
        return 0
    wiki = Path(wiki_file.read_text().strip())
    target_file = Path("task.target")
    target = target_file.read_text().strip() if target_file.is_file() else ""

    answer = (STATE / "answer.md").read_text(encoding="utf-8")
    if answer.strip() in ("NO CHANGE", "NO PAGE"):
        print("green", end="")
        return 0

    found = target_and_page(answer, target)
    if found is None:
        print("skip", end="")
        return 0
    rel, page = found
    if not safe_relative(rel):
        CONTRACT_LOG.write_text(
            f"PATH: {rel} is not a path inside the wiki; give one relative to "
            "its root, like concepts/example.md\n"
        )
        print("red", end="")
        return 1

    if not COPY.exists():
        shutil.copytree(wiki, COPY, ignore=IGNORE)
        before = complaints(COPY)
        if before is None:
            # The validator cannot run here at all. runner.py will say so
            # after the loop, where it fails the run; blocking the loop on it
            # would spend the budget on repairs nothing can satisfy.
            print("skip", end="")
            return 0
        BASELINE.write_text("\n".join(before))

    # A new page written by an earlier iteration under a different PATH would
    # otherwise stay in the copy and be judged alongside this one.
    if WRITTEN.exists():
        previous = WRITTEN.read_text().strip()
        if previous and previous != rel and not (wiki / previous).exists():
            (COPY / previous).unlink(missing_ok=True)
    WRITTEN.write_text(rel)

    dest = COPY / rel
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(page, encoding="utf-8")

    after = complaints(COPY)
    if after is None:
        print("skip", end="")
        return 0
    baseline = set(BASELINE.read_text().splitlines())
    new = [c for c in after if c not in baseline]
    if not (wiki / rel).exists():
        # runner.py adds a new page's index entry after the loop, so an orphan
        # complaint about it here is expected rather than something to repair.
        orphan = f"{rel}: orphan page (no inbound links; add it to index.md)"
        new = [c for c in new if c != orphan]
    if not new:
        print("green", end="")
        return 0

    CONTRACT_LOG.write_text(
        "The wiki's own validator (tools/check_wiki.py) rejects this page. "
        "Fix exactly these complaints; they are about the page's frontmatter, "
        "tags and links, not its substance:\n"
        + "\n".join(f"- {c}" for c in new) + "\n"
    )
    print("red", end="")
    return 1


if __name__ == "__main__":
    sys.exit(main())
