/*
 * Copyright (c) 2021 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package vm

import (
	"fmt"
	"sync/atomic"
)

// multiCacheEntry is one monomorphic dispatch-value cache slot: the last
// dispatch value seen and the method it resolved to (already accounting for the
// default fallback). Behind an atomic.Pointer so a concurrent invoke sees a
// consistent (dv, method) pair. No generation tag is needed — a MultiFn's
// method table is immutable for its lifetime (AddMethod/RemoveMethod build a
// fresh MultiFn), so an entry can never go stale for the instance holding it.
type multiCacheEntry struct {
	dv     Value
	method Fn
}

// MultiFn implements Clojure-style multimethods.
// It holds a dispatch function and a map of dispatch-value → method.
type MultiFn struct {
	name       string
	dispatchFn Fn
	methods    *PersistentMap
	defaultVal Value // dispatch value for the default method
	// frozen marks a MultiFn whose method set was captured as the baseline for
	// generated native dispatch arms (gogen_ir). AddMethod/RemoveMethod build a
	// fresh struct, so the flag never propagates to an extended MultiFn — a late
	// defmethod replaces the var's value with an unfrozen one, which is the
	// signal the generated guard uses to fall back to runtime dispatch.
	frozen bool

	// cache is a monomorphic dispatch-value cache: it skips the method-table
	// probe (and the default-fallback probe) when the dispatch value repeats.
	// It cannot skip the dispatch function itself — that computes dv from args
	// and must run every call. Only scalar (== -comparable) dispatch values are
	// cached; see multiCacheableKey.
	cache atomic.Pointer[multiCacheEntry]

	// mega latches when a second live dispatch value is seen. A single slot
	// can't serve a polymorphic site, and storing on every miss would allocate
	// an entry per call — worse than no cache. Once latched we stop caching and
	// fall back to the table probe. (N-way cache is the fix — future work.)
	mega atomic.Bool
}

func NewMultiFn(name string, dispatchFn Fn, defaultVal Value) *MultiFn {
	return &MultiFn{
		name:       name,
		dispatchFn: dispatchFn,
		methods:    EmptyPersistentMap,
		defaultVal: defaultVal,
	}
}

func (m *MultiFn) Type() ValueType { return MultiFnType }
func (m *MultiFn) Unbox() any      { return m }

func (m *MultiFn) String() string {
	return fmt.Sprintf("<multifn %s>", m.name)
}

// AddMethod registers an implementation for a dispatch value.
func (m *MultiFn) AddMethod(dispatchVal Value, method Fn) *MultiFn {
	return &MultiFn{
		name:       m.name,
		dispatchFn: m.dispatchFn,
		methods:    m.methods.Assoc(dispatchVal, method).(*PersistentMap),
		defaultVal: m.defaultVal,
	}
}

// RemoveMethod unregisters an implementation.
func (m *MultiFn) RemoveMethod(dispatchVal Value) *MultiFn {
	return &MultiFn{
		name:       m.name,
		dispatchFn: m.dispatchFn,
		methods:    m.methods.Dissoc(dispatchVal).(*PersistentMap),
		defaultVal: m.defaultVal,
	}
}

// Arity returns -1 (variadic — arity depends on the method).
func (m *MultiFn) Arity() int { return -1 }

// Invoke calls the dispatch function, looks up the method, and calls it.
func (m *MultiFn) Invoke(args []Value) (Value, error) {
	return m.invokeIn(RootExecContext, args)
}

// invokeIn runs both the dispatch function and the selected method under ec, so
// dynamic vars (and *out*/*err*/scope) read inside either resolve against the
// caller's context rather than the root. Mirrors the Closure/MultiArityFn ec
// threading.
func (m *MultiFn) invokeIn(ec *ExecContext, args []Value) (Value, error) {
	// Call dispatch function. Unavoidable: dv is computed from args by user
	// code, so no cache can skip this — only the method resolution below.
	dv, err := ec.Invoke(m.dispatchFn, args)
	if err != nil {
		return NIL, fmt.Errorf("multimethod %s dispatch failed: %w", m.name, err)
	}

	// Monomorphic cache, active only while the site stays monomorphic. The
	// guard is a Value == compare; it never panics because entries are only
	// stored for comparable-typed dv (an uncomparable dv has a different dynamic
	// type from the stored key, so == short-circuits to false without comparing
	// values).
	if !m.mega.Load() {
		if e := m.cache.Load(); e != nil {
			if e.dv == dv {
				if multiCacheStats.Load() {
					multiCacheHits.Add(1)
				}
				return ec.Invoke(e.method, args)
			}
			// A second live dispatch value: give up on the single slot.
			m.mega.Store(true)
		}
	}

	// Look up method for dispatch value
	method := m.methods.ValueAt(dv)
	if method == NIL {
		// Try default
		method = m.methods.ValueAt(m.defaultVal)
		if method == NIL {
			return NIL, fmt.Errorf("no method in multimethod '%s' for dispatch value: %s", m.name, dv)
		}
	}

	fn, ok := method.(Fn)
	if !ok {
		return NIL, fmt.Errorf("multimethod '%s' method is not a function", m.name)
	}

	if !m.mega.Load() && multiCacheableKey(dv) {
		m.cache.Store(&multiCacheEntry{dv: dv, method: fn})
	}
	if multiCacheStats.Load() {
		multiCacheMisses.Add(1)
	}
	return ec.Invoke(fn, args)
}

// multiCacheableKey reports whether dv can key the inline cache: only scalar
// Value types whose Go == matches the method table's structural equality, plus
// ValueType singletons (the (defmulti f class) case). Vectors/maps and other
// composite or uncomparable dispatch values fall through to the table probe.
func multiCacheableKey(dv Value) bool {
	switch dv.(type) {
	case Int, Float, Boolean, String, Keyword, Symbol, Char:
		return true
	}
	_, ok := dv.(ValueType)
	return ok
}

// Multimethod inline-cache instrumentation. Off by default (one predictable
// branch on the hot path); a test flips it to measure hit rate. Spike-only.
var (
	multiCacheStats  atomic.Bool
	multiCacheHits   atomic.Uint64
	multiCacheMisses atomic.Uint64
)

// Methods returns the method map.
func (m *MultiFn) Methods() *PersistentMap {
	return m.methods
}

// FreezeNative marks this MultiFn as the captured baseline for generated
// native dispatch arms. Done once, in place, at namespace-load completion
// (rt.ApplyGoOverrides) — the var still points at this exact pointer, and any
// later AddMethod/RemoveMethod yields a fresh, unfrozen MultiFn.
func (m *MultiFn) FreezeNative() { m.frozen = true }

// IsNativeFrozen reports whether this MultiFn is still the native baseline
// (no defmethod has replaced it since FreezeNative). The generated type-switch
// only trusts its baked arms while this holds.
func (m *MultiFn) IsNativeFrozen() bool { return m.frozen }

type theMultiFnType struct{}

func (t *theMultiFnType) String() string  { return t.Name() }
func (t *theMultiFnType) Type() ValueType { return TypeType }
func (t *theMultiFnType) Unbox() any      { return nil }
func (t *theMultiFnType) Name() string    { return "let-go.lang.MultiFn" }
func (t *theMultiFnType) Box(bare any) (Value, error) {
	return NIL, NewTypeError(bare, "can't be boxed as", t)
}

var MultiFnType *theMultiFnType = &theMultiFnType{}
