#!/usr/bin/env python3
"""
docs-status: a read-only report over docs/**/*.md frontmatter.

The pre-commit hook (scripts/docs_frontmatter_hook.py) and the CI floor
check keep the *mechanical* fields honest — frontmatter present, with a
parseable `status:` and `last-verified:`. This tool reports on the
*judgement* layer the hook deliberately won't touch: freshness drift,
supersession links that don't agree with each other, two docs claiming
authority on the same topic, and docs the README index never routes to.

It never mutates anything. It is the "agent skill to drive it" follow-up
named in docs/frontmatter-hook.md: an agent (or a human) runs it, reads
the findings, and applies judgement — the tool does not decide for them.

Findings, by category:

  stale-last-verified   last-verified older than --stale-days.
  aged-human-verified   human-verified present but older than
                        --human-stale-days (a human vouched, but long ago).
  supersession          superseded-by/supersedes that dangles (target not
                        found) or isn't mirrored on the other doc; status
                        that disagrees with the links.
  authoritative-clash   the same topic slug claimed authoritative by >1 doc.
  missing-index         a tracked doc the README index body never mentions.

`human-verified:` blank (never vouched) is reported only as a summary
count, not per-doc noise — see docs/frontmatter-hook.md for why that
field is special. The tool never writes it; nothing should.

Exit status: 0, or 2 if a file is unreadable or malformed (or --root is
missing). Findings never fail it — this is a report, not a gate. Stdlib only.
"""

from __future__ import annotations

import argparse
import datetime
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

TODAY = datetime.date.today()
DELIM = "---"

# A top-level frontmatter key: no leading whitespace, `name:` then rest of line.
KEY_RE = re.compile(r"^([A-Za-z][\w-]*)\s*:\s?(.*)$")
# Leading filename token in a supersedes/superseded-by entry like
# "master-plan.md (on direction — ...)". The parenthetical is human prose.
REF_FILE_RE = re.compile(r"^(\S+\.md)")
DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")


class MalformedFrontmatter(Exception):
    pass


@dataclass
class Doc:
    path: Path
    rel: str
    fields: dict  # key -> str | list[str]

    def scalar(self, key: str) -> str | None:
        v = self.fields.get(key)
        return v if isinstance(v, str) else None

    def seq(self, key: str) -> list[str]:
        v = self.fields.get(key)
        if isinstance(v, list):
            return v
        return [v] if isinstance(v, str) and v else []


def split_frontmatter(text: str) -> list[str]:
    """Return the frontmatter lines (between the delimiters), or raise/None."""
    lines = text.split("\n")
    if not lines or lines[0] != DELIM:
        return []
    for i in range(1, len(lines)):
        if lines[i] == DELIM:
            return lines[1:i]
    raise MalformedFrontmatter("opening `---` present but no closing delimiter")


def parse_frontmatter(block: list[str]) -> dict:
    """
    Minimal YAML-ish parse, enough for this convention's shapes:
      key: scalar
      key: [a, b]          (inline sequence)
      key:                 (block sequence on following indented `- ` lines)
        - item
    Deeper nesting is ignored. Stdlib only — no yaml dependency.
    """
    out: dict = {}
    i = 0
    n = len(block)
    while i < n:
        line = block[i]
        m = KEY_RE.match(line)
        if not m:
            i += 1
            continue
        key, rest = m.group(1), m.group(2).strip()
        if rest.startswith("[") and rest.endswith("]"):
            inner = rest[1:-1].strip()
            out[key] = [s.strip() for s in inner.split(",") if s.strip()] if inner else []
            i += 1
            continue
        if rest:
            out[key] = rest
            i += 1
            continue
        # Empty value: look ahead for an indented block sequence.
        items: list[str] = []
        j = i + 1
        while j < n:
            nxt = block[j]
            if nxt.strip() == "":
                j += 1
                continue
            if not nxt.startswith((" ", "\t")):
                break
            item = nxt.strip()
            if item.startswith("- "):
                items.append(item[2:].strip())
            j += 1
        out[key] = items if items else ""
        i = j if items else i + 1
    return out


def parse_date(value: str | None) -> datetime.date | None:
    if not value or not DATE_RE.match(value.strip()):
        return None
    try:
        return datetime.date.fromisoformat(value.strip())
    except ValueError:
        return None


def load_docs(root: Path) -> tuple[list[Doc], list[str]]:
    docs: list[Doc] = []
    errors: list[str] = []
    for path in sorted(root.rglob("*.md")):
        if path.is_symlink() or not path.is_file():
            continue
        rel = str(path.relative_to(root.parent) if root.parent != path else path)
        try:
            block = split_frontmatter(path.read_text(encoding="utf-8-sig"))
        except (OSError, UnicodeDecodeError) as e:
            errors.append(f"{rel}: {e}")
            continue
        except MalformedFrontmatter as e:
            errors.append(f"{rel}: {e}")
            continue
        if not block:
            errors.append(f"{rel}: no frontmatter")
            continue
        docs.append(Doc(path=path, rel=rel, fields=parse_frontmatter(block)))
    return docs, errors


@dataclass
class Report:
    stale_last_verified: list[dict] = field(default_factory=list)
    aged_human_verified: list[dict] = field(default_factory=list)
    supersession: list[dict] = field(default_factory=list)
    authoritative_clash: list[dict] = field(default_factory=list)
    missing_index: list[dict] = field(default_factory=list)
    never_human_verified: int = 0
    total_docs: int = 0
    errors: list[str] = field(default_factory=list)

    def finding_count(self) -> int:
        return sum(
            len(x)
            for x in (
                self.stale_last_verified,
                self.aged_human_verified,
                self.supersession,
                self.authoritative_clash,
                self.missing_index,
            )
        )


def first_token(ref: str) -> str:
    m = REF_FILE_RE.match(ref.strip())
    return m.group(1) if m else ref.strip()


def analyze(docs: list[Doc], root: Path, stale_days: int, human_stale_days: int) -> Report:
    rep = Report(total_docs=len(docs))
    by_basename: dict[str, list[Doc]] = {}
    for d in docs:
        by_basename.setdefault(d.path.name, []).append(d)

    # Freshness.
    for d in docs:
        lv = parse_date(d.scalar("last-verified"))
        if lv is not None:
            age = (TODAY - lv).days
            if age > stale_days:
                rep.stale_last_verified.append(
                    {"doc": d.rel, "last_verified": lv.isoformat(), "age_days": age}
                )
        hv = parse_date(d.scalar("human-verified"))
        if hv is None:
            rep.never_human_verified += 1
        else:
            age = (TODAY - hv).days
            if age > human_stale_days:
                rep.aged_human_verified.append(
                    {"doc": d.rel, "human_verified": hv.isoformat(), "age_days": age}
                )

    # Supersession integrity. Resolve refs by basename within the tree.
    def resolve(ref: str, source: Doc) -> Doc | None:
        name = first_token(ref)
        cands = by_basename.get(name)
        return cands[0] if cands and len(cands) == 1 else None

    for d in docs:
        status = (d.scalar("status") or "").strip()
        sup_by = d.seq("superseded-by")
        sup = d.seq("supersedes")
        for ref in sup_by:
            target = resolve(ref, d)
            name = first_token(ref)
            if target is None:
                rep.supersession.append(
                    {"doc": d.rel, "issue": f"superseded-by '{name}' not found (or ambiguous)"}
                )
                continue
            mirror = {first_token(r) for r in target.seq("supersedes")}
            if d.path.name not in mirror:
                rep.supersession.append(
                    {
                        "doc": d.rel,
                        "issue": f"superseded-by '{target.rel}', but it does not list this doc under supersedes:",
                    }
                )
        for ref in sup:
            target = resolve(ref, d)
            name = first_token(ref)
            if target is None:
                rep.supersession.append(
                    {"doc": d.rel, "issue": f"supersedes '{name}' not found (or ambiguous)"}
                )
                continue
            mirror = {first_token(r) for r in target.seq("superseded-by")}
            if d.path.name not in mirror:
                rep.supersession.append(
                    {
                        "doc": d.rel,
                        "issue": f"supersedes '{target.rel}', but it does not list this doc under superseded-by:",
                    }
                )
        if status == "superseded" and not sup_by:
            rep.supersession.append(
                {"doc": d.rel, "issue": "status: superseded but no superseded-by: link"}
            )

    # Authoritative-for collisions: same topic claimed by >1 doc.
    topic_owners: dict[str, list[str]] = {}
    for d in docs:
        for topic in d.seq("authoritative-for"):
            topic_owners.setdefault(topic.strip(), []).append(d.rel)
    for topic, owners in sorted(topic_owners.items()):
        if len(owners) > 1:
            rep.authoritative_clash.append({"topic": topic, "docs": sorted(owners)})

    # Index coverage: docs whose filename never appears in README.md body.
    readme = root / "README.md"
    if readme.is_file():
        body = readme.read_text(encoding="utf-8-sig")
        for d in docs:
            if d.path == readme:
                continue
            if d.path.name not in body:
                rep.missing_index.append({"doc": d.rel})

    return rep


def render_text(rep: Report) -> str:
    out: list[str] = [
        f"docs-status: {rep.total_docs} docs scanned, {rep.finding_count()} findings",
        "",
    ]

    def section(title: str, rows: list[str]) -> None:
        if not rows:
            return
        out.append(f"## {title} ({len(rows)})")
        out.extend(rows)
        out.append("")
    section(
        "stale last-verified",
        [f"  {r['doc']} — {r['last_verified']} ({r['age_days']}d)" for r in rep.stale_last_verified],
    )
    section(
        "aged human-verified",
        [f"  {r['doc']} — {r['human_verified']} ({r['age_days']}d)" for r in rep.aged_human_verified],
    )
    section("supersession", [f"  {r['doc']}: {r['issue']}" for r in rep.supersession])
    section(
        "authoritative-for clashes",
        [f"  '{r['topic']}' claimed by: {', '.join(r['docs'])}" for r in rep.authoritative_clash],
    )
    section("missing from README index", [f"  {r['doc']}" for r in rep.missing_index])
    out.append(
        f"note: {rep.never_human_verified}/{rep.total_docs} docs have a blank human-verified "
        "(never vouched by a human). This is informational — the field is set only by "
        "explicit human action; do not stamp it."
    )
    if rep.errors:
        out.append("")
        out.append("## errors")
        out.extend(f"  {e}" for e in rep.errors)
    return "\n".join(out)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[1])
    parser.add_argument("--root", default="docs", help="docs root to scan (default: docs)")
    parser.add_argument("--stale-days", type=int, default=180)
    parser.add_argument("--human-stale-days", type=int, default=365)
    args = parser.parse_args(argv)

    root = Path(args.root)
    if not root.is_dir():
        print(f"docs-status: root not found: {root}", file=sys.stderr)
        return 2

    docs, errors = load_docs(root)
    rep = analyze(docs, root, args.stale_days, args.human_stale_days)
    rep.errors = errors

    print(render_text(rep))
    return 2 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
