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
