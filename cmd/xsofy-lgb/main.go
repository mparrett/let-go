// Command xsofy-lgb compiles a let-go entry file and all namespaces it
// requires into a single program.lgb bundle — the same artifact `lg -w`
// embeds, but emitted standalone so it can be go:embed'd into a wasip1 main.
// Usage: xsofy-lgb <entry.lg> <search-root> <out.lgb>
package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"

	"github.com/nooga/let-go/pkg/bytecode"
	"github.com/nooga/let-go/pkg/compiler"
	"github.com/nooga/let-go/pkg/resolver"
	"github.com/nooga/let-go/pkg/rt"
	"github.com/nooga/let-go/pkg/vm"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: xsofy-lgb <entry.lg> <search-root> <out.lgb>")
		os.Exit(2)
	}
	entry, root, out := os.Args[1], os.Args[2], os.Args[3]

	consts := vm.NewConsts()
	ns := rt.NS("user")
	ctx := compiler.NewCompiler(consts, ns)
	nsRes := resolver.NewNSResolver(ctx, []string{root, "."})
	rt.SetNSLoader(nsRes)

	// Signal AOT so guarded top-level boot forms — e.g. xsofy's
	// (when-not *compiling-aot* (-main)) — don't execute at compile time.
	rt.CoreNS.Lookup("*compiling-aot*").(*vm.Var).SetRoot(vm.TRUE)

	ctx.SetSource(entry)
	f, err := os.Open(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	chunk, _, err := ctx.CompileMultiple(f)
	f.Close()
	if err != nil {
		fmt.Fprint(os.Stderr, vm.FormatError(err))
		os.Exit(1)
	}

	var buf bytes.Buffer
	if len(nsRes.LoadedChunks) > 0 {
		mainNS := ctx.CurrentNS().Name()
		nsChunks := make(map[string]*vm.CodeChunk, len(nsRes.LoadedChunks)+1)
		maps.Copy(nsChunks, nsRes.LoadedChunks)
		nsChunks[mainNS] = chunk
		nsOrder := append(nsRes.LoadOrder, mainNS)
		if err := bytecode.EncodeBundleOrdered(&buf, ctx.Consts(), nsChunks, nsOrder); err != nil {
			fmt.Fprintf(os.Stderr, "encode bundle: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := bytecode.EncodeCompilation(&buf, ctx.Consts(), chunk); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes, %d namespaces)\n", out, buf.Len(), len(nsRes.LoadOrder)+1)
}
