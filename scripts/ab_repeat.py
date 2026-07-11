#!/usr/bin/env python3
"""Interleaved repeated A/B for perf-pr variance reduction.

Runs N interleaved base/head benchmark snapshots across two pre-built worktrees
and aggregates per-family deltas two ways:

  strategy 1 (median-of-N): gate if |median delta| > budget
  strategy 2 (confirm):     gate if >= K of N runs exceed +budget (a regression
                            must REPRODUCE, not just spike once)

Motivation: a single-shot base-vs-head A/B on shared CI runners is too heavy-
tailed to gate on — ~1 in 4 runs blows a memory-bound cluster to 25-40% while
the (register-only) anchor stays flat. Interleaving + repetition suppresses that.

Each side is snapshotted with `bench-ratchet -profile <p> ... snapshot`, which
emits ratio_to_anchor per benchmark; delta = head_ratio / base_ratio - 1.
"""
import argparse, json, os, statistics, subprocess, sys


def snapshot(worktree, profile, out, timeout):
    """Run one bench-ratchet snapshot inside a worktree; return {family: ratio}."""
    subprocess.run(
        ["go", "run", "./cmd/bench-ratchet", "-profile", profile,
         "-timeout", timeout, "-baseline", out, "snapshot"],
        cwd=worktree, check=True,
    )
    d = json.load(open(out))
    (_, m), = d["machines"].items()
    return {k: e["ratio_to_anchor"] for k, e in m["benchmarks"].items()}, \
        m["anchor"]["ns_per_op"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="pre-built base worktree")
    ap.add_argument("--head", required=True, help="pre-built head worktree")
    ap.add_argument("--n", type=int, default=5, help="repeat count")
    ap.add_argument("--profile", default="pr-fast")
    ap.add_argument("--budget", type=float, default=8.0, help="gate budget percent")
    ap.add_argument("--confirm-k", type=int, default=2,
                    help="runs that must exceed budget to gate (strategy 2)")
    ap.add_argument("--timeout", default="15m")
    ap.add_argument("--out", default="ab-out")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    deltas = {}          # family -> [delta% per cycle]
    anchors = []         # (base_anchor, head_anchor) per cycle
    for i in range(1, args.n + 1):
        print(f"::group::cycle {i}/{args.n}", flush=True)
        # Counterbalance the measurement order (ABBA): odd cycles bench base
        # first, even cycles head first. Base and head are identical code, so a
        # fixed base-then-head order lets any first-vs-second-position drift
        # (warmup, cache, thermal) masquerade as a consistent head regression —
        # a systematic bias the median cannot remove. Alternating cancels it.
        if i % 2 == 1:
            b, ba = snapshot(args.base, args.profile,
                             f"{args.out}/base_{i}.json", args.timeout)
            h, ha = snapshot(args.head, args.profile,
                             f"{args.out}/head_{i}.json", args.timeout)
        else:
            h, ha = snapshot(args.head, args.profile,
                             f"{args.out}/head_{i}.json", args.timeout)
            b, ba = snapshot(args.base, args.profile,
                             f"{args.out}/base_{i}.json", args.timeout)
        anchors.append((ba, ha))
        for fam in set(b) & set(h):
            if b[fam]:
                deltas.setdefault(fam, []).append((h[fam] / b[fam] - 1.0) * 100)
        print("::endgroup::", flush=True)

    rows = []
    for fam, ds in deltas.items():
        med = statistics.median(ds)
        exceed = sum(1 for d in ds if d > args.budget)      # regressions only
        rows.append({
            "fam": fam.replace("github.com/nooga/let-go/pkg/vm.Benchmark", ""),
            "n": len(ds), "median": med, "worst": max(ds, key=abs),
            "exceed": exceed, "ds": ds,
            "gate_median": med > args.budget,
            "gate_confirm": exceed >= args.confirm_k,
        })
    rows.sort(key=lambda r: r["median"], reverse=True)

    json.dump({"budget": args.budget, "confirm_k": args.confirm_k,
               "anchors": anchors, "rows": rows},
              open(f"{args.out}/aggregate.json", "w"), indent=2)

    print(f"\n=== interleaved A/B, N={args.n}, budget={args.budget}%, "
          f"confirm K={args.confirm_k} ===")
    print("anchor stability per cycle (base/head ns/op):")
    for i, (ba, ha) in enumerate(anchors, 1):
        print(f"  cycle {i}: base {ba:.3f}  head {ha:.3f}  "
              f"Δ{(ha/ba-1)*100:+.1f}%")
    print(f"\n{'family':40} {'median%':>8} {'worst%':>7} {'exc':>3}  verdict")
    print("-" * 78)
    gated = []
    for r in rows[:12]:
        v = []
        if r["gate_median"]: v.append("MEDIAN")
        if r["gate_confirm"]: v.append("CONFIRM")
        tag = ",".join(v) if v else "ok"
        if v: gated.append(r["fam"])
        print(f"{r['fam']:40} {r['median']:+8.2f} {r['worst']:+7.2f} "
              f"{r['exceed']:3d}  {tag}")
    med_hits = [r["fam"] for r in rows if r["gate_median"]]
    conf_hits = [r["fam"] for r in rows if r["gate_confirm"]]
    print("-" * 78)
    print(f"strategy 1 (median>{args.budget}%): "
          f"{len(med_hits)} families gate {med_hits or ''}")
    print(f"strategy 2 (>={args.confirm_k}/{args.n} exceed {args.budget}%): "
          f"{len(conf_hits)} families gate {conf_hits or ''}")
    # This is a same-code probe → any gate is a FALSE POSITIVE.
    print(f"\nFALSE-POSITIVE gates at budget {args.budget}%: "
          f"median={len(med_hits)}  confirm={len(conf_hits)}  "
          f"(both should be 0 on a no-op PR)")


if __name__ == "__main__":
    main()
