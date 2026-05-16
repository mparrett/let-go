/*
 * Copyright (c) 2021 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package vm

import (
	"fmt"
	"sync"
)

type Var struct {
	root      Value
	bindings  []Value // dynamic binding stack (nil when unused — zero cost)
	nsref     *Namespace
	ns        string
	name      string
	isMacro   bool
	isDynamic bool
	isPrivate bool
	mu        sync.Mutex
	watches   map[Value]Fn
}

func (v *Var) Invoke(values []Value) (Value, error) {
	f, ok := v.root.(Fn)
	if !ok {
		return NIL, fmt.Errorf("%v root does not implement Fn", v.root)
	}
	return f.Invoke(values)
}

func (v *Var) Arity() int {
	f, ok := v.root.(Fn)
	if !ok {
		return 0
	}
	return f.Arity()
}

func NewVar(nsref *Namespace, ns string, name string) *Var {
	return &Var{
		nsref:     nsref,
		ns:        ns,
		name:      name,
		root:      NIL,
		isMacro:   false,
		isDynamic: false,
		isPrivate: false,
	}
}

func (v *Var) SetRoot(val Value) *Var {
	v.mu.Lock()
	v.root = val
	v.mu.Unlock()
	return v
}

func (v *Var) Deref() Value {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.bindings) > 0 {
		return v.bindings[len(v.bindings)-1]
	}
	return v.root
}

// PushBinding pushes a dynamic binding value.
func (v *Var) PushBinding(val Value) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.bindings = append(v.bindings, val)
}

// PopBinding removes the most recent dynamic binding.
func (v *Var) PopBinding() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.bindings) > 0 {
		v.bindings = v.bindings[:len(v.bindings)-1]
	}
}

func (v *Var) notifyWatches(oldVal, newVal Value) error {
	v.mu.Lock()
	if len(v.watches) == 0 {
		v.mu.Unlock()
		return nil
	}
	watches := make(map[Value]Fn, len(v.watches))
	for key, fn := range v.watches {
		watches[key] = fn
	}
	v.mu.Unlock()
	for key, fn := range watches {
		if _, err := fn.Invoke([]Value{key, v, oldVal, newVal}); err != nil {
			return err
		}
	}
	return nil
}

func (v *Var) AlterRoot(fn Fn) (Value, error) {
	old := v.Deref()
	result, err := fn.Invoke([]Value{old})
	if err != nil {
		return NIL, err
	}
	v.mu.Lock()
	v.root = result
	v.mu.Unlock()
	if err := v.notifyWatches(old, result); err != nil {
		return NIL, err
	}
	return result, nil
}

func (v *Var) AddWatch(key Value, fn Fn) {
	v.mu.Lock()
	if v.watches == nil {
		v.watches = make(map[Value]Fn)
	}
	v.watches[key] = fn
	v.mu.Unlock()
}

func (v *Var) RemoveWatch(key Value) {
	v.mu.Lock()
	delete(v.watches, key)
	v.mu.Unlock()
}

func (v *Var) Type() ValueType {
	return v.Deref().Type()
}

func (v *Var) Unbox() interface{} {
	return v.Deref().Unbox()
}

func (v *Var) String() string {
	return fmt.Sprintf("#'%s/%s", v.ns, v.name)
}

func (v *Var) IsMacro() bool {
	return v.isMacro
}

func (v *Var) IsDynamic() bool {
	return v.isDynamic
}

func (v *Var) IsPrivate() bool {
	return v.isPrivate
}

// NS returns the namespace name.
func (v *Var) NS() string { return v.ns }

// VarName returns the var name.
func (v *Var) VarName() string { return v.name }

func (v *Var) SetMacro() {
	v.isMacro = true
}

func (v *Var) SetDynamic() {
	v.isDynamic = true
}

func (v *Var) SetPrivate() {
	v.isPrivate = true
}
