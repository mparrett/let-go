/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package vm

import (
	"sync"
	"sync/atomic"
)

// bindingStack is the dynamic-var binding state owned by a single
// ExecContext. Unlike the process-global store it replaces, it is keyed by
// nothing — it belongs to one execution, so per-goroutine isolation comes from
// each goroutine carrying its own ExecContext, with no goroutine-id lookup and
// no cross-goroutine reuse hazard.
//
// The binding map is held behind an atomic pointer and is immutable once
// published: reads (current/hasBinding/snapshot) load it lock-free, so the hot
// deref path of every ^:dynamic var — which checks the stack on each read
// because the var is *declared* dynamic whether or not it currently has a
// binding — stays off the mutex. Writers (push/pop/setCurrent/installSnapshot)
// serialize on mu, copy the map, and atomically swap the new one in, so a
// concurrent reader sees either the old map or the new one, never a torn state.
// The mutex only serializes the rare case of two writers sharing one context (a
// value escaping to a helper goroutine).
type bindingStack struct {
	mu  sync.Mutex // serializes writers
	cur atomic.Pointer[BindingSnapshot]
}

func newBindingStack() *bindingStack {
	b := &bindingStack{}
	m := make(BindingSnapshot)
	b.cur.Store(&m)
	return b
}

// load returns the current immutable binding map. Lock-free; the returned map
// must never be mutated in place — writers publish a fresh copy instead.
func (b *bindingStack) load() BindingSnapshot {
	return *b.cur.Load()
}

// cloneLocked shallow-copies the current map. Caller holds mu. The per-var
// slices are shared with the old map; a mutator that changes one var's stack
// must replace that var's entry with a freshly allocated slice (never append
// into the shared one) so the published old map is never observed changing.
func (b *bindingStack) cloneLocked() BindingSnapshot {
	old := b.load()
	m := make(BindingSnapshot, len(old))
	for k, s := range old {
		m[k] = s
	}
	return m
}

func (b *bindingStack) push(v *Var, val Value) {
	b.mu.Lock()
	m := b.cloneLocked()
	s := m[v]
	ns := make([]Value, len(s)+1)
	copy(ns, s)
	ns[len(s)] = val
	m[v] = ns
	b.cur.Store(&m)
	b.mu.Unlock()
}

func (b *bindingStack) pop(v *Var) {
	b.mu.Lock()
	s := b.load()[v]
	if n := len(s); n > 0 {
		m := b.cloneLocked()
		if n == 1 {
			delete(m, v)
		} else {
			ns := make([]Value, n-1)
			copy(ns, s[:n-1])
			m[v] = ns
		}
		b.cur.Store(&m)
	}
	b.mu.Unlock()
}

func (b *bindingStack) current(v *Var) (Value, bool) {
	if stack := b.load()[v]; len(stack) > 0 {
		return stack[len(stack)-1], true
	}
	return nil, false
}

// setCurrent replaces the value of v's top dynamic binding in place, returning
// true if a binding existed. This is the (set! *v* val) primitive: it mutates
// only THIS context's top frame (thread-local in Clojure terms) and never the
// root. A child context's frame is its own copy (Child snapshots it), so the
// mutation stays isolated to this execution and does not leak to siblings or
// the parent.
func (b *bindingStack) setCurrent(v *Var, val Value) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.load()[v]
	n := len(s)
	if n == 0 {
		return false
	}
	m := b.cloneLocked()
	ns := make([]Value, n)
	copy(ns, s)
	ns[n-1] = val
	m[v] = ns
	b.cur.Store(&m)
	return true
}

func (b *bindingStack) hasBinding(v *Var) bool {
	return len(b.load()[v]) > 0
}

func (b *bindingStack) snapshot() BindingSnapshot {
	old := b.load()
	out := make(BindingSnapshot, len(old))
	for v, stack := range old {
		out[v] = append([]Value(nil), stack...)
	}
	return out
}

func (b *bindingStack) installSnapshot(snap BindingSnapshot) {
	out := make(BindingSnapshot, len(snap))
	for v, stack := range snap {
		out[v] = append([]Value(nil), stack...)
	}
	b.mu.Lock()
	b.cur.Store(&out)
	b.mu.Unlock()
}
