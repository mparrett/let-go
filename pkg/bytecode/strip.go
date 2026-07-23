/*
 * Copyright (c) 2026 Matt Parrett <matt.parrett@gmail.com>
 * Part of the let-go project; see CONTRIBUTORS for full list of authors.
 * SPDX-License-Identifier: MIT
 */

package bytecode

import (
	"bytes"

	"github.com/nooga/let-go/pkg/vm"
)

// StripDebug re-encodes an .lgb without its debug sections: per-chunk source
// maps and local-variable tables. Execution is unaffected — the VM consults
// both only for error reporting and debugging — so a stripped bundle trades
// source-located errors for size (about 20% on the core bundle).
//
// Rebuilds via DecodeToExecUnit + ModuleBuilder so Func→chunk bindings stay
// pointer-accurate. A structural Decode→Encode round-trip would re-resolve
// Funcs by code content and can collapse distinct chunks that share a body.
func StripDebug(data []byte) ([]byte, error) {
	unit, err := DecodeToExecUnitBytes(data, func(ns, name string) *vm.Var { return nil })
	if err != nil {
		return nil, err
	}
	clearUnitDebug(unit)

	var buf bytes.Buffer
	if len(unit.NSChunks) > 0 {
		err = EncodeBundleOrdered(&buf, unit.Consts, unit.NSChunks, unit.NSOrder)
	} else {
		err = EncodeCompilation(&buf, unit.Consts, unit.MainChunk)
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func clearUnitDebug(unit *ExecUnit) {
	seen := map[*vm.CodeChunk]struct{}{}
	visit := func(c *vm.CodeChunk) {
		if c == nil {
			return
		}
		if _, ok := seen[c]; ok {
			return
		}
		seen[c] = struct{}{}
		c.ClearDebugInfo()
	}
	visit(unit.MainChunk)
	for _, c := range unit.NSChunks {
		visit(c)
	}
	for _, v := range unit.Consts.Values() {
		if fn, ok := v.(*vm.Func); ok {
			visit(fn.Chunk())
		}
	}
}
