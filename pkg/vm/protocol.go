/*
 * Copyright (c) 2021 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package vm

import (
	"fmt"
	"sync/atomic"
)

// Protocol defines a named set of methods with type-based dispatch.
type Protocol struct {
	name    string
	methods []Symbol                     // method names
	impls   map[ValueType]*PersistentMap // type → {method-name → fn}
	nilImpl *PersistentMap               // implementation for nil
	meta    Value                        // IMeta support

	// gen is the dispatch-table generation, bumped on every extend. It is the
	// watchpoint backing ProtocolFn's inline cache: any change to impls strands
	// every cached (type → fn) entry, so a redefinition or a newly-added impl
	// can never be served stale. Pointer-typed so a WithMeta copy (which shares
	// the dispatch tables) shares the counter, and so the struct stays copyable
	// without tripping vet's copylocks check on the atomic.
	gen *atomic.Uint64
}

func NewProtocol(name string, methods []Symbol) *Protocol {
	return &Protocol{
		name:    name,
		methods: methods,
		impls:   make(map[ValueType]*PersistentMap),
		gen:     new(atomic.Uint64),
	}
}

func (p *Protocol) Type() ValueType   { return ProtocolType }
func (p *Protocol) Unbox() any        { return p }
func (p *Protocol) String() string    { return fmt.Sprintf("<protocol %s>", p.name) }
func (p *Protocol) Name() string      { return p.name }
func (p *Protocol) Methods() []Symbol { return p.methods }

// Meta implements IMeta.
func (p *Protocol) Meta() Value {
	if p.meta == nil {
		return NIL
	}
	return p.meta
}

// WithMeta implements IMeta. Returns a copy carrying m; dispatch tables are
// shared with the original (metadata does not affect protocol dispatch).
func (p *Protocol) WithMeta(m Value) Value {
	cp := *p
	cp.meta = m
	return &cp
}

// Extend adds implementations for a type.
// implMap is a PersistentMap of {method-keyword → fn}.
func (p *Protocol) Extend(vt ValueType, implMap *PersistentMap) {
	p.impls[vt] = implMap
	p.gen.Add(1) // invalidate every ProtocolFn inline cache over this protocol
}

// ExtendNil adds implementations for nil.
func (p *Protocol) ExtendNil(implMap *PersistentMap) {
	p.nilImpl = implMap
	p.gen.Add(1)
}

// Lookup finds the implementation of a method for a given value's type.
func (p *Protocol) Lookup(methodName Symbol, target Value) (Fn, bool) {
	key := Keyword(methodName)

	if target == NIL {
		if p.nilImpl != nil {
			v := p.nilImpl.ValueAt(key)
			if v != NIL {
				if fn, ok := v.(Fn); ok {
					return fn, true
				}
			}
		}
		return nil, false
	}

	vt := target.Type()
	if fn, ok := p.lookupIn(p.impls[vt], key); ok {
		return fn, true
	}
	// Fall back to a default extended onto Object (AnyType) — Clojure's
	// (extend-type Object ...) universal default. Also covers a partial impl
	// that is missing this particular method.
	if vt != AnyType {
		if fn, ok := p.lookupIn(p.impls[AnyType], key); ok {
			return fn, true
		}
	}
	return nil, false
}

// lookupIn pulls a method fn out of one type's impl map, if present.
func (p *Protocol) lookupIn(implMap *PersistentMap, key Value) (Fn, bool) {
	if implMap == nil {
		return nil, false
	}
	v := implMap.ValueAt(key)
	if v == NIL {
		return nil, false
	}
	fn, ok := v.(Fn)
	return fn, ok
}

// Satisfies returns true if the given value's type has an implementation, or a
// universal default was extended onto Object (AnyType).
func (p *Protocol) Satisfies(target Value) bool {
	if target == NIL {
		return p.nilImpl != nil || p.impls[AnyType] != nil
	}
	if _, ok := p.impls[target.Type()]; ok {
		return ok
	}
	return p.impls[AnyType] != nil
}

// protoCacheEntry is one monomorphic inline-cache slot: the last type seen at
// this ProtocolFn and the impl it resolved to, tagged with the protocol gen it
// was resolved under. Stored behind an atomic.Pointer so a concurrent invoke
// sees a consistent (vt, fn, gen) triple, never a torn one.
type protoCacheEntry struct {
	vt  ValueType
	fn  Fn
	gen uint64
}

// Protocol inline-cache instrumentation. Off by default (one predictable-false
// branch on the hot path); a test flips it to measure hit rate. Not wired for
// production telemetry yet — this is spike scaffolding.
var (
	protoCacheStats  atomic.Bool
	protoCacheHits   atomic.Uint64
	protoCacheMisses atomic.Uint64
)

// ProtocolFn is a function that dispatches on the first arg's type via a protocol.
type ProtocolFn struct {
	protocol   *Protocol
	methodName Symbol
	name       string

	// cache is a monomorphic inline cache over this call point. Real protocol
	// sites are overwhelmingly monomorphic in steady state, so a single slot
	// captures most of the win.
	cache atomic.Pointer[protoCacheEntry]

	// mega latches when a second live type is seen at this site. A single-entry
	// cache can't serve a polymorphic site, and storing on every miss would
	// allocate an entry per call — worse than no cache. Once megamorphic we stop
	// caching and fall back to plain Lookup, capping the downside at ~baseline.
	// (A 2-/N-way PIC is the way to actually serve these sites — future work.)
	mega atomic.Bool
}

func NewProtocolFn(protocol *Protocol, methodName Symbol) *ProtocolFn {
	return &ProtocolFn{
		protocol:   protocol,
		methodName: methodName,
		name:       string(methodName),
	}
}

func (f *ProtocolFn) Type() ValueType { return FuncType }
func (f *ProtocolFn) Unbox() any      { return f }
func (f *ProtocolFn) String() string {
	return fmt.Sprintf("<protocol-fn %s/%s>", f.protocol.name, f.name)
}
func (f *ProtocolFn) Arity() int { return -1 }

func (f *ProtocolFn) Invoke(args []Value) (Value, error) {
	return f.invokeIn(RootExecContext, args)
}

// invokeIn resolves the protocol implementation for args[0]'s type and runs it
// under ec, so dynamic vars (and *out*/*err*/scope) read inside a protocol
// method resolve against the caller's context rather than the root. Mirrors the
// Closure/MultiArityFn ec threading.
func (f *ProtocolFn) invokeIn(ec *ExecContext, args []Value) (Value, error) {
	if len(args) == 0 {
		return NIL, fmt.Errorf("protocol fn %s requires at least one argument", f.name)
	}
	target := args[0]

	// Monomorphic inline cache. The guard is a ValueType identity compare —
	// types are singletons, so == is a pointer compare — plus a generation
	// check against the protocol's watchpoint. A hit skips the map indirection
	// and PersistentMap probe that Lookup would do. nil is left on the slow
	// path: it has its own resolution bucket and is rare in hot loops.
	if target != NIL {
		// Fast path only while the site is still monomorphic. Once latched
		// megamorphic we take a single mega.Load and fall straight through to
		// Lookup — no gen/cache loads, no store — so a polymorphic site pays
		// almost nothing over the un-cached baseline.
		if !f.mega.Load() {
			vt := target.Type()
			gen := f.protocol.gen.Load()
			if e := f.cache.Load(); e != nil && e.gen == gen {
				if e.vt == vt {
					if protoCacheStats.Load() {
						protoCacheHits.Add(1)
					}
					return ec.Invoke(e.fn, args)
				}
				// A second live type at this site: give up on the single slot.
				f.mega.Store(true)
			} else {
				// Cold or gen-stale slot: resolve and fill it.
				impl, ok := f.protocol.Lookup(f.methodName, target)
				if !ok {
					return f.noImplErr(target)
				}
				f.cache.Store(&protoCacheEntry{vt: vt, fn: impl, gen: gen})
				if protoCacheStats.Load() {
					protoCacheMisses.Add(1)
				}
				return ec.Invoke(impl, args)
			}
		}
		// Megamorphic: plain Lookup, no caching.
		impl, ok := f.protocol.Lookup(f.methodName, target)
		if !ok {
			return f.noImplErr(target)
		}
		if protoCacheStats.Load() {
			protoCacheMisses.Add(1)
		}
		return ec.Invoke(impl, args)
	}

	if impl, ok := f.protocol.Lookup(f.methodName, target); ok {
		return ec.Invoke(impl, args)
	}
	return f.noImplErr(target)
}

// noImplErr formats the "no implementation" dispatch failure for target.
func (f *ProtocolFn) noImplErr(target Value) (Value, error) {
	typeName := "nil"
	if target != NIL {
		typeName = target.Type().Name()
	}
	return NIL, fmt.Errorf("no implementation of protocol %s method %s for type %s",
		f.protocol.name, f.name, typeName)
}

// Protocol type metadata

type theProtocolType struct{}

func (t *theProtocolType) String() string  { return t.Name() }
func (t *theProtocolType) Type() ValueType { return TypeType }
func (t *theProtocolType) Unbox() any      { return nil }
func (t *theProtocolType) Name() string    { return "let-go.lang.Protocol" }
func (t *theProtocolType) Box(bare any) (Value, error) {
	return NIL, NewTypeError(bare, "can't be boxed as", t)
}

var ProtocolType *theProtocolType = &theProtocolType{}
