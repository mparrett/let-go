/*
 * Copyright (c) 2026 Norman Nunley, Jr <nnunley@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package vm

// ExecContext is the per-execution state that the eval loop and builtins need
// to resolve dynamic state, threaded explicitly through execution rather than
// looked up by goroutine id. It currently carries the dynamic-var binding
// stack; the structured-concurrency Scope folds in later (see
// docs/design/exec-context-threading.md).
//
// Threading model: the root frame of a Run holds an ExecContext; each Fn call
// propagates it to the callee's frame via InvokeCtx; a spawned goroutine seeds
// a fresh child ExecContext from a snapshot of its parent's. A nil ExecContext
// means "no execution context installed" — callers fall back to the process
// state, which is how the system behaves until the binding path is migrated
// onto ec.
type ExecContext struct {
	bindings *bindingStack
}

// NewExecContext returns an ExecContext with an empty binding stack.
func NewExecContext() *ExecContext {
	return &ExecContext{bindings: newBindingStack()}
}

// child returns a fresh ExecContext seeded from a snapshot of this one's
// bindings — the explicit-propagation primitive used at goroutine boundaries.
// A nil receiver yields a fresh empty context.
func (ec *ExecContext) child() *ExecContext {
	c := NewExecContext()
	if ec != nil && ec.bindings != nil {
		c.bindings.installSnapshot(ec.bindings.snapshot())
	}
	return c
}

// CtxFn is the optional, additive calling convention for functions that need
// the ExecContext: closures use it to propagate ec to their child frame;
// builtins that read dynamic vars use it to resolve against ec. Functions that
// don't implement it are invoked context-free via Invoke, unchanged.
type CtxFn interface {
	InvokeCtx(ec *ExecContext, args []Value) (Value, error)
}

// InvokeWith calls fn with the ExecContext when fn opts into CtxFn, else falls
// back to the plain context-free Invoke. This is the single chokepoint the
// eval loop uses at every Fn call site.
func InvokeWith(ec *ExecContext, fn Fn, args []Value) (Value, error) {
	if cf, ok := fn.(CtxFn); ok {
		return cf.InvokeCtx(ec, args)
	}
	return fn.Invoke(args)
}
