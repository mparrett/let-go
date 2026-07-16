#!/usr/bin/env python3
"""Injected-regression power study for the repeat-A/B perf gate (#445).

Measures the harness's DETECTION rate against a known regression. A test-only
env-scaled spin (perfInjectSpin, PERF_INJECT_SPIN) injects a calibrated slowdown
into known families (FrameDispatch = compute, VectorConj = memory-bound). For
each magnitude we run an interleaved median-of-N A/B where base = spin 0 and head
= spin K on the SAME compiled binary, so the only difference is the injected
cost — the cleanest possible ground truth.

Reports, per magnitude: the ground-truth % (a separate high-count pass), the
harness's recovered median, whether it would gate at 6/8/10%, and whether any
NON-injected family false-gates. Sweeping magnitude across runner draws builds
the power curve the shadow phase needs before a required gate.

Compile-once: the pkg/vm bench binary is built a single time, then invoked N×2×M
times with different -test.bench / env — no per-snapshot recompile.
"""
import argparse, json, os, re, statistics, subprocess, sys

ANCHOR = "BenchmarkRatchetAnchor"
_LINE = re.compile(r"^(Benchmark\S+?)-\d+\s+\d+\s+([\d.]+) ns/op")


def run_bench(binpath, filt, benchtime, count, spin):
    """Invoke the compiled bench binary once; return {bench_name: median_ns}."""
    env = dict(os.environ, PERF_INJECT_SPIN=str(spin))
    out = subprocess.run(
        [binpath, "-test.bench", filt, "-test.run", "x",
         "-test.benchtime", benchtime, "-test.count", str(count)],
        cwd=os.path.dirname(binpath) or ".", env=env,
        capture_output=True, text=True, check=True,
    ).stdout
    samples = {}
    for line in out.splitlines():
        m = _LINE.match(line)
        if m:
            samples.setdefault(m.group(1), []).append(float(m.group(2)))
    return {k: statistics.median(v) for k, v in samples.items()}


def ratios(binpath, filt, anchor_filt, benchtime, count, spin):
    """One snapshot: family ns normalized to the anchor.

    The anchor is run under its own filter and merged, so the family filter can
    be a depth-3 pattern that size-limits sub-benchmarks (e.g. VectorConj /10,/100
    but not the O(n²) /1000) — Go's -bench won't match the depth-1 anchor against a
    depth-3 pattern. bench-ratchet measures anchor and families sequentially too,
    so a second invocation is no less faithful.
    """
    ns = run_bench(binpath, filt, benchtime, count, spin)
    ns.update(run_bench(binpath, anchor_filt, benchtime, count, spin))
    anchor = ns.get(ANCHOR)
    if not anchor:
        raise RuntimeError(f"anchor {ANCHOR} not measured (spin={spin})")
    return {k: v / anchor for k, v in ns.items() if k != ANCHOR}, anchor


def is_injected(fam, injected):
    return any(fam.startswith(p) for p in injected)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--worktree", required=True, help="let-go tree with the injection")
    ap.add_argument("--magnitudes", default="0,75,150,230,460",
                    help="comma-separated PERF_INJECT_SPIN values (0 = null)")
    ap.add_argument("--n", type=int, default=7, help="interleaved cycles per magnitude")
    ap.add_argument("--benchtime", default="500ms")
    ap.add_argument("--count", type=int, default=3, help="samples per snapshot")
    ap.add_argument("--gt-count", type=int, default=4, help="ground-truth samples")
    ap.add_argument("--gt-benchtime", default="500ms")
    ap.add_argument("--gt-reps", type=int, default=3,
                    help="paired GT passes to median over (guards one drifty pass)")
    ap.add_argument("--budgets", default="6,8,10")
    ap.add_argument("--filter", default=r"^BenchmarkFrameDispatch$",
                    help="family benchmark regex (the anchor is added separately)")
    ap.add_argument("--anchor-filter", default=r"^BenchmarkRatchetAnchor$",
                    help="anchor benchmark regex, measured and merged per snapshot")
    ap.add_argument("--injected", default="BenchmarkFrameDispatch",
                    help="comma-separated name prefixes carrying an injection call site")
    ap.add_argument("--out", default="inject-out")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    budgets = [float(b) for b in args.budgets.split(",")]
    spins = [int(s) for s in args.magnitudes.split(",")]
    filt = args.filter
    anchor_filt = args.anchor_filter
    injected = args.injected.split(",")

    binpath = os.path.join(args.out, "vm.test")
    print("::group::compile bench binary once", flush=True)
    subprocess.run(["go", "test", "-c", "-o", os.path.abspath(binpath), "./pkg/vm"],
                   cwd=args.worktree, check=True)
    binpath = os.path.abspath(binpath)
    print("::endgroup::", flush=True)

    results = []
    for K in spins:
        print(f"::group::magnitude spin={K}", flush=True)
        # Ground truth: anchor-normalized spin0 vs spinK, median of --gt-reps paired
        # passes so a single thermal-drift pass can't skew the reference (a single
        # pass went non-monotonic across magnitudes in local testing).
        g0, gk = {}, {}
        for _ in range(args.gt_reps):
            r0, _ = ratios(binpath, filt, anchor_filt, args.gt_benchtime, args.gt_count, 0)
            rk, _ = ratios(binpath, filt, anchor_filt, args.gt_benchtime, args.gt_count, K)
            for f in set(r0) & set(rk):
                if r0[f]:
                    g0.setdefault(f, []).append(r0[f])
                    gk.setdefault(f, []).append(rk[f])
        ground = {f: (statistics.median(gk[f]) / statistics.median(g0[f]) - 1) * 100
                  for f in g0}

        # Interleaved median-of-N A/B: base=spin0, head=spinK, ABBA order.
        deltas, anchors = {}, []
        for i in range(1, args.n + 1):
            if i % 2 == 1:
                b, ba = ratios(binpath, filt, anchor_filt, args.benchtime, args.count, 0)
                h, ha = ratios(binpath, filt, anchor_filt, args.benchtime, args.count, K)
            else:
                h, ha = ratios(binpath, filt, anchor_filt, args.benchtime, args.count, K)
                b, ba = ratios(binpath, filt, anchor_filt, args.benchtime, args.count, 0)
            anchors.append((ba, ha))
            for f in set(b) & set(h):
                if b[f]:
                    deltas.setdefault(f, []).append((h[f] / b[f] - 1) * 100)
        med = {f: statistics.median(ds) for f, ds in deltas.items()}

        gated = {str(bud): [f for f in med if med[f] > bud] for bud in budgets}
        # A false gate is a non-injected family crossing budget.
        false_gates = {b: [f for f in fs if not is_injected(f, injected)] for b, fs in gated.items()}
        detected = {str(bud): [f for f in gated[str(bud)] if is_injected(f, injected)] for bud in budgets}
        results.append({
            "spin": K, "ground_truth": ground, "median": med,
            "detected": detected, "false_gates": false_gates,
        })
        print("::endgroup::", flush=True)

        # Per-magnitude line.
        gt_inj = {f: g for f, g in ground.items() if is_injected(f, injected)}
        print(f"\nspin={K:4}  ground-truth injected: "
              + ", ".join(f"{f.replace('Benchmark','')} {g:+.1f}%" for f, g in sorted(gt_inj.items()))[:120])
        for bud in budgets:
            print(f"    gate@{bud:.0f}%: detected={[f.replace('Benchmark','') for f in detected[str(bud)]] or '-'}  "
                  f"FALSE={[f.replace('Benchmark','') for f in false_gates[str(bud)]] or '-'}")

    with open(f"{args.out}/sweep.json", "w") as f:
        json.dump({"budgets": budgets, "n": args.n, "count": args.count,
                   "benchtime": args.benchtime, "results": results}, f, indent=2)
    print(f"\nwrote {args.out}/sweep.json")


if __name__ == "__main__":
    main()
