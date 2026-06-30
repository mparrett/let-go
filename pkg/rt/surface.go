/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * The surface namespace — a thin "screen buffer" host capability (let-go #255,
 * Graphics Model B). The guest hands the host a raw RGBA pixel buffer and a size;
 * the host blits it (a <canvas> putImageData in WASM). Unlike js/emit this carries
 * a *buffer*, not a JSON event — an RGBA frame is too big to escape-encode through
 * the text/output seam. The platform binding lives in surface_js_wasm.go (WASM ->
 * window.LetGoHost.onSurface) and surface_other.go (native no-op for now).
 *
 * This is a spike seam: present-on-call, no retained scene. The rich layer (dirty
 * rects, double-buffering, native window/fb0 binding) stays above it / TBD.
 */

package rt

import (
	"fmt"

	"github.com/nooga/let-go/pkg/vm"
)

func init() { RegisterInstaller(installSurfaceNS) }

func installSurfaceNS() {
	ns := vm.NewNamespace("surface")

	// (surface/present rgba width height) -> nil. rgba is a byte-array of
	// width*height*4 bytes (R,G,B,A row-major, top-left origin). Fire-and-forget:
	// if no host surface is wired the frame is dropped, not an error.
	presentFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 3 {
			return vm.NIL, fmt.Errorf("surface/present expects 3 args (rgba width height), got %d", len(vs))
		}
		data, ok := asBytes(vs[0])
		if !ok {
			return vm.NIL, fmt.Errorf("surface/present expects a byte-array for rgba")
		}
		w, ok1 := vs[1].(vm.Int)
		h, ok2 := vs[2].(vm.Int)
		if !ok1 || !ok2 {
			return vm.NIL, fmt.Errorf("surface/present width and height must be Int")
		}
		if len(data) < int(w)*int(h)*4 {
			return vm.NIL, fmt.Errorf("surface/present rgba too small: have %d, need %d", len(data), int(w)*int(h)*4)
		}
		hostPresentSurface(data, int(w), int(h))
		return vm.NIL, nil
	})
	ns.Def("present", presentFn)

	// (surface/available?) -> bool. True when the host has wired a surface sink
	// (a canvas in the browser shell); lets the guest pick canvas vs another path.
	availFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.Boolean(hostSurfaceAvailable()), nil
	})
	ns.Def("available?", availFn)

	RegisterNS(ns)
}
