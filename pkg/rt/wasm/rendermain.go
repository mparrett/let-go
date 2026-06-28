/*
 * Copyright (c) 2021-2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package wasm

import (
	"strconv"
	"strings"
)

// wasmMainTmpl is the generated main.go for a `lg -w` bundle: it decodes the
// embedded program.lgb, runs each namespace chunk, then the main chunk. The
// __LG_* markers are filled by RenderMain.
const wasmMainTmpl = `package main

import (
	_ "embed"
	"bytes"
	"fmt"
	"os"
__LG_HOST_EVAL_IMPORTS__

	"github.com/nooga/let-go/pkg/bytecode"
	"github.com/nooga/let-go/pkg/compiler"
	"github.com/nooga/let-go/pkg/resolver"
	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"
)

//go:embed program.lgb
var lgbData []byte

func main() {
	consts := vm.NewConsts()
	ns := rt.NS("user")
	ctx := compiler.NewCompiler(consts, ns)
	nsResolver := resolver.NewNSResolver(ctx, []string{"."})
	rt.SetNSLoader(nsResolver)

	// Route *out*/*err* to the JS host via _lgOutput (HostWriter), instead of
	// os.Stdout/Stderr + the bundle's fs.writeSync fd interception. SetRoot,
	// not a per-Run binding, because this generated main drives bytecode
	// directly rather than through pkg/api. Guarded: if the core I/O vars
	// aren't installed yet, output falls back to os.Stdout.
	hostWriter := rt.NewHostWriter()
	if v := rt.LookupCoreVar("*out*"); v != nil {
		v.SetRoot(vm.NewBoxed(rt.NewWriterHandle("host-stdout", hostWriter)))
	}
	if v := rt.LookupCoreVar("*err*"); v != nil {
		v.SetRoot(vm.NewBoxed(rt.NewWriterHandle("host-stderr", hostWriter)))
	}

	// Route (js/emit ...) to the JS host via _lgEmit (HostEmitter), the dual
	// of the HostWriter *out* routing above. Same SetRoot rationale.
	hostEmitter := rt.NewHostEmitter()
	if v := rt.LookupCoreVar("*emit*"); v != nil {
		v.SetRoot(vm.NewBoxed(hostEmitter))
	}

	// Route storage through browser localStorage, scoped by the bundle's
	// host-selected store id so guest keys remain app-local.
	hostStorage := rt.NewHostStorage(__LG_STORAGE_ID__)
	if v := rt.LookupCoreVar("*storage*"); v != nil {
		v.SetRoot(vm.NewBoxed(hostStorage))
	}

	resolve := func(nsName, name string) *vm.Var {
		n := rt.DefNSBare(nsName)
		v := n.LookupLocal(vm.Symbol(name))
		if v == nil {
			return n.DefStub(name)
		}
		return v
	}

	unit, err := bytecode.DecodeToExecUnit(bytes.NewReader(lgbData), resolve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %%v\n", err)
		return
	}

	for _, name := range unit.NSOrder {
		chunk := unit.NSChunks[name]
		if chunk == nil || chunk == unit.MainChunk {
			continue
		}
		f := vm.NewFrame(chunk, nil)
		_, err := f.RunProtected()
		vm.ReleaseFrame(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading %%s: %%v\n", name, err)
			return
		}
	}

	f := vm.NewFrame(unit.MainChunk, nil)
	_, err = f.RunProtected()
	vm.ReleaseFrame(f)
	if err != nil {
		fmt.Fprint(os.Stderr, vm.FormatError(err))
	}
__LG_HOST_EVAL_BODY__
}
`

// wasmHostEvalSnippet is spliced into wasmMainTmpl at __LG_HOST_EVAL_BODY__ when
// -w-host-eval is set. After the program's main chunk runs, it installs an
// internal _lgEval hook — compile + run a string in the loaded image, returning
// a stringified value (FormatError on failure) — signals readiness, then parks
// so the runtime stays callable.
//
// _lgEval is the internal hook (like _lgKey / _lgEmit); lg-host-core.js wraps it
// as the public LetGoHost.eval(code), calling it directly on the main thread or
// relaying through the worker in cross-origin-isolated mode, so one API works in
// both boot modes. The host installs _lgRuntimeReady; calling it resolves the
// LetGoHost.eval ready gate (main thread) or posts the ready message (worker),
// closing the race where a client could call eval before the runtime is up.
// Structured data returns out-of-band via (js/emit ...).
const wasmHostEvalSnippet = `	hostEval := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return "error: eval expects one string argument"
		}
		chunk, cerr := ctx.Compile(args[0].String())
		if cerr != nil {
			return vm.FormatError(cerr)
		}
		frame := vm.NewFrame(chunk, nil)
		result, rerr := frame.RunProtected()
		vm.ReleaseFrame(frame)
		if rerr != nil {
			return vm.FormatError(rerr)
		}
		return result.String()
	})
	js.Global().Set("_lgEval", hostEval)
	if ready := js.Global().Get("_lgRuntimeReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {}`

// RenderMain fills wasmMainTmpl's placeholders: the storage-id, and the
// -w-host-eval splice (the window.Eval bridge + park, plus its syscall/js
// import). With hostEval false the marker lines are removed whole, so the
// default bundle's generated main is byte-identical to the pre-flag output.
func RenderMain(storeID string, hostEval bool) string {
	s := strings.ReplaceAll(wasmMainTmpl, "__LG_STORAGE_ID__", strconv.Quote(storeID))
	if hostEval {
		s = strings.ReplaceAll(s, "__LG_HOST_EVAL_IMPORTS__", "\t\"syscall/js\"")
		s = strings.ReplaceAll(s, "__LG_HOST_EVAL_BODY__", wasmHostEvalSnippet)
	} else {
		s = strings.ReplaceAll(s, "__LG_HOST_EVAL_IMPORTS__\n", "")
		s = strings.ReplaceAll(s, "__LG_HOST_EVAL_BODY__\n", "")
	}
	return s
}
