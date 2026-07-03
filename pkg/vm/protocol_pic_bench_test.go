/*
 * Copyright (c) 2026 let-go contributors
 * SPDX-License-Identifier: MIT
 */

package vm

import "testing"

// protoSink defeats dead-code elimination on the dispatch result.
var protoSink Value

// buildSizedProto returns a ProtocolFn `sz` with impls for Int and String.
// Int → identity, String → its length. The method fns are trivial so the
// benchmark isolates *dispatch* cost (type lookup) rather than method work.
func buildSizedProto() *ProtocolFn {
	method := Symbol("sz")
	p := NewProtocol("Sized", []Symbol{method})

	intFn, _ := NativeFnType.Wrap(func(a []Value) (Value, error) { return a[0], nil })
	p.Extend(IntType, NewPersistentMap([]Value{Keyword(method), intFn}))

	strFn, _ := NativeFnType.Wrap(func(a []Value) (Value, error) {
		return Int(len(string(a[0].(String)))), nil
	})
	p.Extend(StringType, NewPersistentMap([]Value{Keyword(method), strFn}))

	return NewProtocolFn(p, method)
}

// Monomorphic: every call sees Int. This is the hot-loop case a monomorphic
// inline cache is built for — after warmup it should hit every time.
func BenchmarkProtocolDispatchMono(b *testing.B) {
	pf := buildSizedProto()
	args := []Value{Int(7)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := pf.invokeIn(RootExecContext, args)
		if err != nil {
			b.Fatal(err)
		}
		protoSink = v
	}
}

// Bimorphic: alternate Int / String every call. A single-entry monomorphic
// cache thrashes here (miss every call) — this is the case that a 2+-way PIC
// would need; it bounds how much the mono cache can regress a polymorphic site.
func BenchmarkProtocolDispatchBimorphic(b *testing.B) {
	pf := buildSizedProto()
	intArgs := []Value{Int(7)}
	strArgs := []Value{String("hello")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var args []Value
		if i&1 == 0 {
			args = intArgs
		} else {
			args = strArgs
		}
		v, err := pf.invokeIn(RootExecContext, args)
		if err != nil {
			b.Fatal(err)
		}
		protoSink = v
	}
}
