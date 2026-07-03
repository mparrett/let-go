/*
 * Copyright (c) 2026 let-go contributors
 * SPDX-License-Identifier: MIT
 */

package vm

import "testing"

// multiSink defeats dead-code elimination on the dispatch result.
var multiSink Value

// buildKwMulti returns a multimethod dispatching on args[0] (a keyword), with
// methods for :a and :b. The dispatch fn is trivial (returns args[0]) so the
// benchmark exposes the *maximum* achievable cache win — the method-table probe
// as a large fraction of per-call cost. A heavier dispatch fn dilutes the win,
// since the cache can never skip the (unavoidable) dispatch call.
func buildKwMulti() *MultiFn {
	dispatch, _ := NativeFnType.Wrap(func(a []Value) (Value, error) { return a[0], nil })
	m := NewMultiFn("kind", dispatch.(Fn), NIL)
	ma, _ := NativeFnType.Wrap(func(a []Value) (Value, error) { return Int(1), nil })
	mb, _ := NativeFnType.Wrap(func(a []Value) (Value, error) { return Int(2), nil })
	m = m.AddMethod(Keyword("a"), ma.(Fn))
	m = m.AddMethod(Keyword("b"), mb.(Fn))
	return m
}

// Monomorphic: every call dispatches to :a — the case the cache targets.
func BenchmarkMultiDispatchMono(b *testing.B) {
	m := buildKwMulti()
	args := []Value{Keyword("a")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := m.invokeIn(RootExecContext, args)
		if err != nil {
			b.Fatal(err)
		}
		multiSink = v
	}
}

// Bimorphic: alternate :a / :b. A single-slot cache latches megamorphic and
// falls back to the table probe — bounds the downside at ~baseline.
func BenchmarkMultiDispatchBimorphic(b *testing.B) {
	m := buildKwMulti()
	aArgs := []Value{Keyword("a")}
	bArgs := []Value{Keyword("b")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args := aArgs
		if i&1 == 1 {
			args = bArgs
		}
		v, err := m.invokeIn(RootExecContext, args)
		if err != nil {
			b.Fatal(err)
		}
		multiSink = v
	}
}
