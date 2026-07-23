/*
 * Copyright (c) 2026 Matt Parrett <matt.parrett@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package bytecode

import (
	"bytes"
	"testing"

	"github.com/nooga/let-go/pkg/vm"
)

func TestStripDebugRemovesDebugSections(t *testing.T) {
	consts := vm.NewConsts()
	chunk := vm.NewCodeChunk(consts)
	chunk.Append32(int(vm.OP_LOAD_CONST))
	chunk.Append32(consts.Intern(vm.Int(42)))
	chunk.Append32(int(vm.OP_RETURN))
	chunk.SetMaxStack(1)
	chunk.AddSourceInfoAt(0, vm.SourceInfo{File: "test.lg", Line: 1, Column: 1, EndLine: 1, EndColumn: 5})
	chunk.AddLocalVar(0, "x")

	var enc bytes.Buffer
	if err := EncodeCompilation(&enc, consts, chunk); err != nil {
		t.Fatal(err)
	}
	fat := enc.Bytes()

	slim, err := StripDebug(fat)
	if err != nil {
		t.Fatal(err)
	}
	if len(slim) >= len(fat) {
		t.Fatalf("stripped bundle not smaller: %d >= %d", len(slim), len(fat))
	}

	m, err := Decode(bytes.NewReader(slim))
	if err != nil {
		t.Fatalf("stripped bundle does not decode: %v", err)
	}
	for i, c := range m.Chunks {
		if len(c.SourceMap) != 0 {
			t.Errorf("chunk %d retains %d source map entries", i, len(c.SourceMap))
		}
		if len(c.LocalVars) != 0 {
			t.Errorf("chunk %d retains %d localvar entries", i, len(c.LocalVars))
		}
	}
	if m.Flags&FlagLocalVars != 0 {
		t.Error("FlagLocalVars still set on stripped bundle")
	}

	// The stripped bundle must still execute.
	unit, err := DecodeToExecUnitBytes(slim, func(ns, name string) *vm.Var { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f := vm.NewFrame(unit.MainChunk, nil)
	out, err := f.Run()
	vm.ReleaseFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	if out != vm.Int(42) {
		t.Fatalf("stripped bundle ran to %v, want 42", out)
	}
}

func TestStripDebugBundleFormat(t *testing.T) {
	consts := vm.NewConsts()
	mk := func(v vm.Value) *vm.CodeChunk {
		c := vm.NewCodeChunk(consts)
		c.Append32(int(vm.OP_LOAD_CONST))
		c.Append32(consts.Intern(v))
		c.Append32(int(vm.OP_RETURN))
		c.SetMaxStack(1)
		c.AddSourceInfoAt(0, vm.SourceInfo{File: "ns.lg", Line: 1, Column: 1, EndLine: 1, EndColumn: 3})
		c.AddLocalVar(0, "y")
		return c
	}
	nsChunks := map[string]*vm.CodeChunk{"a.ns": mk(vm.Int(1)), "main": mk(vm.Int(7))}

	var enc bytes.Buffer
	if err := EncodeBundleOrdered(&enc, consts, nsChunks, []string{"a.ns", "main"}); err != nil {
		t.Fatal(err)
	}
	slim, err := StripDebug(enc.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	m, err := Decode(bytes.NewReader(slim))
	if err != nil {
		t.Fatalf("stripped bundle does not decode: %v", err)
	}
	if len(m.NSTable) != 2 {
		t.Fatalf("NS table lost in strip: %v", m.NSTable)
	}
	for i, c := range m.Chunks {
		if len(c.SourceMap) != 0 || len(c.LocalVars) != 0 {
			t.Errorf("chunk %d retains debug sections", i)
		}
	}

	// Idempotent: stripping a stripped bundle is a no-op.
	again, err := StripDebug(slim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(slim, again) {
		t.Errorf("strip is not idempotent: %d -> %d bytes", len(slim), len(again))
	}

	unit, err := DecodeToExecUnitBytes(slim, func(ns, name string) *vm.Var { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f := vm.NewFrame(unit.MainChunk, nil)
	out, err := f.Run()
	vm.ReleaseFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	if out != vm.Int(7) {
		t.Fatalf("stripped bundle main ran to %v, want 7", out)
	}
}

// Two funcs with identical bodies but different MaxStack must keep distinct
// chunk bindings across StripDebug (and EncodeCompilation). findChunkIndex
// used to match by code only and collapse them onto the first hit.
func TestStripDebugPreservesDistinctIdenticalCodeChunks(t *testing.T) {
	consts := vm.NewConsts()
	mk := func(maxStack int, name string) *vm.CodeChunk {
		c := vm.NewCodeChunk(consts)
		c.Append32(int(vm.OP_LOAD_CONST))
		c.Append32(consts.Intern(vm.NIL))
		c.Append32(int(vm.OP_RETURN))
		c.SetMaxStack(maxStack)
		c.AddSourceInfoAt(0, vm.SourceInfo{File: name + ".lg", Line: 1, Column: 1, EndLine: 1, EndColumn: 1})
		return c
	}
	c1 := mk(3, "fn1")
	c2 := mk(7, "fn2")
	f1 := vm.MakeFunc(0, false, c1)
	f1.SetName("fn1")
	f2 := vm.MakeFunc(0, false, c2)
	f2.SetName("fn2")
	_ = consts.Intern(f1)
	_ = consts.Intern(f2)

	main := vm.NewCodeChunk(consts)
	main.Append32(int(vm.OP_LOAD_CONST))
	main.Append32(consts.Intern(vm.Int(42)))
	main.Append32(int(vm.OP_RETURN))
	main.SetMaxStack(1)

	var enc bytes.Buffer
	if err := EncodeCompilation(&enc, consts, main); err != nil {
		t.Fatal(err)
	}
	slim, err := StripDebug(enc.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	unit, err := DecodeToExecUnitBytes(slim, func(ns, name string) *vm.Var { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, v := range unit.Consts.Values() {
		if fn, ok := v.(*vm.Func); ok {
			got[fn.FuncName()] = fn.Chunk().MaxStack()
		}
	}
	if got["fn1"] != 3 || got["fn2"] != 7 {
		t.Fatalf("StripDebug rebound identical-code funcs: %v (want fn1=3 fn2=7)", got)
	}
}
