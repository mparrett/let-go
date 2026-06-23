---
name: docs-status
description: Audit documentation hygiene for the let-go docs/ tree — find stale or unverified docs, dangling or one-sided supersession links, duplicate authoritative-for claims, and docs missing from the README index. Use when asked to check doc freshness, run doc triage, audit docs/, find stale docs, or verify documentation hygiene before a docs PR.
---

# docs-status — documentation hygiene triage

Drives `scripts/docs_status.py`, the read-only judgement-layer report over
`docs/**/*.md` frontmatter. The pre-commit hook and CI floor check keep the
*mechanical* fields honest (frontmatter present, `status:`/`last-verified:`
parseable); this surfaces the *judgement* layer they leave to a human or
agent. Full reference: `docs/docs-status.md`.

## How to run

From the repository root:

```
python3 scripts/docs_status.py            # text report over docs/
```

Stdlib-only Python; no install step. Override the scan root with `--root`
and the thresholds with `--stale-days` / `--human-stale-days`.

## What the findings mean, and what to do

- **stale-last-verified** — the doc hasn't been touched in a while. Re-read
  it; if still accurate, a trivial edit (or the pre-commit hook) refreshes
  `last-verified:`. Don't bump the date without actually reviewing.
- **aged-human-verified** — a human vouched once, long ago. Flag it for a
  human to re-attest. Do not refresh it yourself.
- **supersession** — mirror the missing half (`supersedes:` ↔
  `superseded-by:`), fix a dangling target, or add the `superseded-by:`
  link a `status: superseded` doc is missing.
- **authoritative-for clashes** — two docs claim the same topic. Propose
  which one yields, or split the topic. This is a judgement call — surface
  it, don't silently rewrite.
- **missing-index** — add a row to `docs/README.md` routing the doc.

## The one hard rule

**Never write `human-verified:`.** It records that a *human* reader vouched
for a doc, and is set only by explicit human action — not by this tool, not
by you, even when a human asks you to edit the doc's other fields. A blank
`human-verified:` is reported only as a count, never as a per-doc task. See
`docs/frontmatter-hook.md` for why this field is special.

Apply the freshness, `status:`, and link fixes the way a human reviewer
would — the tool reports; you exercise the judgement.
