package vm

import (
	"os"
	"strconv"
)

// perfInjectSpin adds a calibrated, env-scaled busy-loop to a benchmark's timed
// region, for the injected-regression power study (nooga/let-go#445). It exists
// to measure the repeat-A/B harness's detection rate against a KNOWN regression:
// inject a known +X% into a known family, then check whether median-of-N flags
// it, at what budget, and whether the measured median recovers X.
//
// PERF_INJECT_SPIN=N spins N times per call; unset/0 is a true no-op, so a single
// build serves both the null A/B (spin 0) and any magnitude. The injected cost
// flows through the same snapshot/median/anchor pipeline as a real regression.
//
// Test-only: it never touches shipped code. The spin is a compute loop, so on a
// memory-bound family it is a proxy for a real memory regression (it rides on the
// family's noise distribution but adds compute, not bandwidth) — good enough to
// measure detection against that family's noise tail; note the caveat when
// reading memory-bound results.
var perfInjectN = func() int {
	n, _ := strconv.Atoi(os.Getenv("PERF_INJECT_SPIN"))
	return n
}()

//go:noinline
func perfInjectSpin() {
	s := 0
	for i := 0; i < perfInjectN; i++ {
		s += i
	}
	perfInjectSink = s
}

var perfInjectSink int
