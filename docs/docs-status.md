---
status: active
last-verified: 2026-06-23
authoritative-for:
  - docs-status-report
human-verified:
---

# docs-status — the judgement-layer report

[`scripts/docs_status.py`](../scripts/docs_status.py) is a read-only
report over `docs/**/*.md` frontmatter. It is the driver named as a
follow-up in [`frontmatter-hook.md`](frontmatter-hook.md): the
pre-commit hook and CI floor check keep the *mechanical* fields honest;
this tool surfaces the *judgement* layer they deliberately leave to a
human or agent.

It never mutates anything. It reports; you apply judgement.

## Running it

```
python3 scripts/docs_status.py            # text report over docs/
python3 scripts/docs_status.py --stale-days 90 --human-stale-days 180
```

Stdlib-only Python, like the hook — no dependency to install. Run it
from the repo root (defaults to scanning `docs/`); override with
`--root`.

## What it checks

- **stale-last-verified** — `last-verified:` older than `--stale-days`
  (default 180). The mechanical hook bumps this on every commit that
  touches a doc, so a stale value means the doc hasn't been edited in a
  while — a prompt to re-read it, not an error.
- **aged-human-verified** — `human-verified:` is present but older than
  `--human-stale-days` (default 365). A human vouched once, but long
  enough ago that the attestation is worth refreshing.
- **supersession** — a `superseded-by:` / `supersedes:` link that
  dangles (target file not found, or an ambiguous basename), or that the
  other doc doesn't mirror, or a `status: superseded` doc with no
  `superseded-by:` link. Supersession is supposed to be stated in both
  directions; this catches the half-done ones.
- **authoritative-for clashes** — the same topic slug claimed by more
  than one doc. Two docs can't both be *the* authority on a topic; one
  should yield or the topic should be split.
- **missing from README index** — a tracked doc whose filename never
  appears in [`README.md`](README.md). The index is how readers are
  routed; a doc absent from it is effectively unfindable.

`human-verified:` being **blank** (never vouched) is reported only as a
summary count, never per-doc. That field is set *only* by explicit human
action — the tool will never write it, and neither should any other
automation. See [`frontmatter-hook.md`](frontmatter-hook.md#the-humanagent-asymmetry).

## How to use the output

The tool is a checklist, not a gate. It exits 0 even with findings (only
an unreadable or malformed doc exits non-zero), so it never blocks a
workflow on its own.

CI runs it as a **non-blocking report**: the `docs-status` job in
[`.github/workflows/go.yml`](../.github/workflows/go.yml) runs on any PR
that touches `docs/`, scans the whole tree, and writes the findings to
the run's step summary (visible in the PR Checks tab). It never fails
the build. The blocking gate is the separate `docs-frontmatter` floor
check (frontmatter present and parseable); staleness and the judgement
fields are reviewer signals, not blockers. A doc can sit stale across
dates and still be correct.

For an agent contributor: run it, read the findings, and propose the
fixes (mirror a supersession link, add a README row, split a clashing
`authoritative-for:`). Apply the freshness and `status:` judgement the
same way a human would — and never touch `human-verified:`.

## As an agent skill

The triage is also packaged as an Agent Skill in
`.agents/skills/docs-status/` — the [Agent Skills](https://agentskills.io)
cross-client convention, discovered automatically by compatible
assistants.

Claude Code does not scan `.agents/` natively yet, and `.claude/` is
gitignored here, so Claude Code discovery is **opt-in per user**. Link it
into your skills directory once:

```
ln -s ../../.agents/skills/docs-status .claude/skills/docs-status   # this project
# or, for all projects:
ln -s "$PWD/.agents/skills/docs-status" ~/.claude/skills/docs-status
```

The symlink stays local (gitignored); the skill itself ships in
`.agents/`. Remove the symlink if Claude Code adds native `.agents/`
scanning.
