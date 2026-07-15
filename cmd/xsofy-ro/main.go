//go:build runtime_only

// Command xsofy-ro runs the embedded xsofy bundle with no compiler/resolver
// linked (runtime_only). Mirrors lg_runtime.go but go:embeds program.lgb so
// stdin stays free for term/read-key. Built with -tags runtime_only, for
// GOOS=wasip1 (standard Go) or TinyGo -target=wasi.
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"

	"github.com/nooga/let-go/pkg/bytecode"
	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"
)

//go:embed program.lgb
var lgbData []byte

func runChunk(c *vm.CodeChunk) error {
	f := vm.NewFrame(c, nil)
	_, err := f.RunProtected()
	vm.ReleaseFrame(f)
	return err
}

func main() {
	if err := rt.LoadCore(); err != nil {
		fmt.Fprintln(os.Stderr, "boot:", err)
		os.Exit(1)
	}
	rt.UseBytecodeNSLoader()
	resolve := func(nsName, name string) *vm.Var {
		n := rt.DefNSBare(nsName)
		if v := n.LookupLocal(vm.Symbol(name)); v != nil {
			return v
		}
		return n.DefStub(name)
	}
	unit, err := bytecode.DecodeToExecUnit(bytes.NewReader(lgbData), resolve)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decode:", err)
		os.Exit(1)
	}
	for _, name := range unit.NSOrder {
		c := unit.NSChunks[name]
		if c == nil || c == unit.MainChunk {
			continue
		}
		if err := runChunk(c); err != nil {
			fmt.Fprintln(os.Stderr, "load "+name+":", err)
			os.Exit(1)
		}
	}
	if err := runChunk(unit.MainChunk); err != nil {
		fmt.Fprint(os.Stderr, vm.FormatError(err))
		os.Exit(1)
	}
}
