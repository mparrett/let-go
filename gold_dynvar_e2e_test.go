/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// dynvarThreadsGold is the verified reference output of
// test/gold/dynvar_threads.cljc under Clojure 1.12 and babashka. let-go must
// emit exactly this. The scenarios cover dynamic-var (namespace variable)
// binding propagation across nested threads: future binding conveyance,
// isolation from later rebinds, nested-future inheritance, concurrent
// non-interference, bound-fn capture, and thread-local set! mutation
// (visibility within the binding, no leak past it, per-future isolation).
//
// The script deliberately stays inside the subset where let-go and Clojure
// agree. let-go is intentionally MORE PERMISSIVE than Clojure in two spots the
// script avoids: (a) set! with no active binding (Clojure throws; let-go
// mutates the root — load-bearing for test.lg), and (b) set! on a conveyed
// binding in a future that opened no binding of its own (Clojure throws; let-go
// permits it). See the script header for details.
const dynvarThreadsGold = "[:a :b :inner [:p :q] :captured :m [:fm :base] [:ma :mb :base] [:pm :pm] :root]"

const dynvarThreadsScript = "test/gold/dynvar_threads.cljc"

// TestGoldDynvarThreadsMatchesClojure is a differential gold test: the SAME
// script run under let-go must produce byte-for-byte the same output as
// Clojure. The golden constant is pinned, and when a `clojure` runtime is on
// PATH we re-derive it from real Clojure so the constant cannot silently drift.
func TestGoldDynvarThreadsMatchesClojure(t *testing.T) {
	lg := buildLG(t)

	got := lastNonEmptyLine(stdoutOf(t, lg, dynvarThreadsScript))
	if got != dynvarThreadsGold {
		t.Fatalf("let-go output = %q, want %q (dynamic-var/thread semantics diverged from Clojure)", got, dynvarThreadsGold)
	}

	// Keep the golden honest: if real Clojure is available, its output must
	// equal the pinned constant too. Otherwise the constant could rot.
	if clj, err := exec.LookPath("clojure"); err == nil {
		ref := lastNonEmptyLine(stdoutOf(t, clj, "-M", dynvarThreadsScript))
		if ref != dynvarThreadsGold {
			t.Fatalf("clojure reference drifted to %q; update dynvarThreadsGold (and re-verify let-go) if this is an intended Clojure change", ref)
		}
	} else {
		t.Log("clojure not on PATH; compared let-go against the pinned golden only")
	}
}

// nsThreadsGold is the verified Clojure 1.12 / babashka output of
// test/gold/ns_threads.cljc: the *ns* (current namespace) dynamic var conveying
// into futures, isolating across concurrent futures, inheriting through nested
// futures, and being mutated thread-locally by in-ns. The script avoids the
// in-ns-as-first-body-form case, which let-go's compiler switches globally at
// compile time (a pre-existing compiler/runtime *ns*-root conflation, separate
// from the dynamic-binding thread work).
const nsThreadsGold = "[gold.a [gold.a gold.b user] gold.b [gold.b user]]"

const nsThreadsScript = "test/gold/ns_threads.cljc"

// TestGoldNsThreadsMatchesClojure validates that *ns* isolation/conveyance
// across nested threads matches Clojure, the same differential way as
// TestGoldDynvarThreadsMatchesClojure.
func TestGoldNsThreadsMatchesClojure(t *testing.T) {
	lg := buildLG(t)

	got := lastNonEmptyLine(stdoutOf(t, lg, nsThreadsScript))
	if got != nsThreadsGold {
		t.Fatalf("let-go output = %q, want %q (*ns* thread isolation diverged from Clojure)", got, nsThreadsGold)
	}

	if clj, err := exec.LookPath("clojure"); err == nil {
		ref := lastNonEmptyLine(stdoutOf(t, clj, "-M", nsThreadsScript))
		if ref != nsThreadsGold {
			t.Fatalf("clojure reference drifted to %q; update nsThreadsGold if this is an intended Clojure change", ref)
		}
	}
}

// stdoutOf runs cmd and returns its stdout, failing the test on a non-zero exit.
// stderr is captured only to surface in failure messages.
func stdoutOf(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v\nstderr:\n%s", name, args, err, errb.String())
	}
	return out.String()
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}
