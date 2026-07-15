// Command xsofy-wasip1 is a self-contained wasip1 host for the embedded xsofy
// bundle: it decodes program.lgb, runs each namespace chunk, then the main
// chunk (which boots xsofy via its (when-not *compiling-aot* (-main)) form).
//
// Unlike the `lg -w` generated main it does NOT route output to a JS host —
// *out*/*err* stay on os.Stdout/Stderr (fd_write), which the WASI term backend
// (term_wasip1.go) renders through, and stdin stays free for term/read-key.
// Built with GOOS=wasip1 GOARCH=wasm, it runs under wasmtime or the paserati
// wasm transpiler.
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"

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
	// The bundle carries every namespace, so the loader should never hit disk;
	// wire it anyway to match the generated main (a runtime require of an
	// unbundled ns would fail cleanly rather than nil-panic).
	rt.SetNSLoader(resolver.NewNSResolver(ctx, []string{"."}))

	resolve := func(nsName, name string) *vm.Var {
		n := rt.DefNSBare(nsName)
		if v := n.LookupLocal(vm.Symbol(name)); v != nil {
			return v
		}
		return n.DefStub(name)
	}

	fmt.Fprintln(os.Stderr, "[xsofy-wasip1] decoding bundle...")
	unit, err := bytecode.DecodeToExecUnit(bytes.NewReader(lgbData), resolve)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[xsofy-wasip1] decoded, %d namespaces to load\n", len(unit.NSOrder))

	// Define every namespace by running its chunk in load order.
	for _, name := range unit.NSOrder {
		chunk := unit.NSChunks[name]
		if chunk == nil || chunk == unit.MainChunk {
			continue
		}
		fmt.Fprintf(os.Stderr, "[xsofy-wasip1] load %s\n", name)
		f := vm.NewFrame(chunk, nil)
		_, err := f.RunProtected()
		vm.ReleaseFrame(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load %s: %v\n", name, err)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "[xsofy-wasip1] namespaces loaded, running main...")

	// Run main — xsofy.main's top-level (-main) boots the game here (runtime,
	// where *compiling-aot* is false).
	f := vm.NewFrame(unit.MainChunk, nil)
	_, err = f.RunProtected()
	vm.ReleaseFrame(f)
	if err != nil {
		fmt.Fprint(os.Stderr, vm.FormatError(err))
	}
}
