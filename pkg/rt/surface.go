/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * The surface namespace — a thin "screen buffer" host capability (let-go #255,
 * Graphics Model B). The guest hands the host a raw RGBA pixel buffer and a size;
 * the host blits it (a <canvas> putImageData in WASM). Unlike js/emit this carries
 * a *buffer*, not a JSON event — an RGBA frame is too big to escape-encode through
 * the text/output seam.
 *
 * Bound at the *surface* root by the host, the same binding discipline as *emit* /
 * *keys* / *storage*: HostSurface in the WASM bundle (surface_js_wasm.go), a
 * Surface via api.WithSurface for Go embedders/tests, nopSurface otherwise.
 * Resolution rides the *surface* dynamic var, so (binding [*surface* ...]) works,
 * per-Run api bindings stay isolated, and the zero value is a silent no-op on
 * every host at once — native graphics (a window / fb0 / terminal-image sink,
 * #255) becomes another Surface to bind, not a new build-tag branch.
 *
 * This is a spike seam: present-on-call, no retained scene. The rich layer (dirty
 * rects, double-buffering) stays above it / TBD.
 */

package rt

import (
	"fmt"

	"github.com/nooga/let-go/pkg/vm"
)

// surfaceMaxDim caps frame width/height. 16384 matches common browser canvas
// limits; anything larger is a geometry bug, not a real frame.
const surfaceMaxDim = 16384

// Surface is the host seam for (surface/present ...): a sink for RGBA frames the
// guest blits to its host. The binary dual of the *emit* Emitter — like it,
// fire-and-forget; unlike it, carries a buffer, not a JSON string.
type Surface interface {
	// Present blits an RGBA frame (width*height*4 bytes, row-major, top-left
	// origin). Fire-and-forget: a frame with no sink attached is dropped.
	Present(rgba []byte, w, h int)
	// Available reports whether a real sink is wired (a canvas in the browser
	// shell, an embedder-supplied Surface). Lets the guest choose canvas vs
	// another render path.
	Available() bool
}

// nopSurface drops frames and reports unavailable. Root binding of *surface*
// until a host installs one (WASM boot, api.WithSurface, or a native binding).
type nopSurface struct{}

func (nopSurface) Present([]byte, int, int) {}
func (nopSurface) Available() bool          { return false }

// resolveSurfaceVar unwraps the current dynamic binding of varName (e.g.
// "*surface*") to a Surface, mirroring resolveEmitterVar. nil if the var isn't
// installed yet or its binding doesn't unwrap to a Surface.
func resolveSurfaceVar(ec *vm.ExecContext, varName string) Surface {
	ns := lookupNSCached(NameCoreNS)
	if ns == nil {
		return nil
	}
	v := ns.LookupLocal(vm.Symbol(varName))
	if v == nil {
		return nil
	}
	if b, ok := ec.Deref(v).(*vm.Boxed); ok {
		if s, ok := b.Unbox().(Surface); ok {
			return s
		}
	}
	return nil
}

// PresentVia dispatches through the current *surface* binding. No-op when
// unbound (early boot, or a host that never installed a surface). A panicking
// sink drops the frame instead of unwinding into the guest: frames are
// fire-and-forget, so silent drop is the correct failure mode — this is what
// keeps a throwing JS callback (via HostSurface) or a faulty embedder Surface
// from taking down the runtime.
func PresentVia(ec *vm.ExecContext, rgba []byte, w, h int) {
	defer func() { _ = recover() }()
	if s := resolveSurfaceVar(ec, "*surface*"); s != nil {
		s.Present(rgba, w, h)
	}
}

// SurfaceAvailableVia reports the current *surface* binding's availability.
// False when unbound or bound to a nop.
func SurfaceAvailableVia(ec *vm.ExecContext) bool {
	if s := resolveSurfaceVar(ec, "*surface*"); s != nil {
		return s.Available()
	}
	return false
}

func init() { RegisterInstaller(installSurfaceNS) }

func installSurfaceNS() {
	ns := vm.NewNamespace("surface")

	// (surface/present rgba width height) -> nil. Ctx-aware so it respects the
	// current (binding [*surface* ...]) / api.WithSurface. Validation runs on
	// every platform so type bugs surface in native dev, not just in the browser.
	presentFn := vm.NewCtxNativeFn("present", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if len(vs) != 3 {
			return vm.NIL, fmt.Errorf("surface/present expects 3 args (rgba width height), got %d", len(vs))
		}
		// Strictly a byte-array: a String would coerce through asBytes but
		// contradicts the documented contract (and ImageData semantics), so it
		// is rejected rather than reinterpreted.
		ta, ok := vs[0].(*vm.TypedArray)
		if !ok || ta.Kind() != vm.ArrayByte {
			return vm.NIL, fmt.Errorf("surface/present expects a byte-array for rgba")
		}
		data := ta.Unbox().([]byte)
		w, ok1 := vs[1].(vm.Int)
		h, ok2 := vs[2].(vm.Int)
		if !ok1 || !ok2 {
			return vm.NIL, fmt.Errorf("surface/present width and height must be Int")
		}
		// Bounding the dimensions before the multiply keeps w*h*4 far from
		// overflow (surfaceMaxDim²·4 = 2³⁰) and rejects zero/negative sizes.
		if w <= 0 || h <= 0 || w > surfaceMaxDim || h > surfaceMaxDim {
			return vm.NIL, fmt.Errorf("surface/present dimensions out of range: %dx%d (must be 1..%d)", int64(w), int64(h), surfaceMaxDim)
		}
		// Exact, not minimum: an RGBA frame with explicit width and height is
		// w*h*4 bytes (ImageData semantics); a mismatch means the caller's
		// geometry is wrong, and blitting it would render garbage or throw
		// host-side.
		if need := int(w) * int(h) * 4; len(data) != need {
			return vm.NIL, fmt.Errorf("surface/present rgba length %d does not match %dx%d (need exactly %d)", len(data), int(w), int(h), need)
		}
		PresentVia(ec, data, int(w), int(h))
		return vm.NIL, nil
	})
	ns.Def("present", presentFn)

	// (surface/available?) -> bool. True when a real sink is bound — a canvas is
	// attached, not merely "running in a browser bundle."
	availFn := vm.NewCtxNativeFn("available?", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.Boolean(SurfaceAvailableVia(ec)), nil
	})
	ns.Def("available?", availFn)

	RegisterNS(ns)
}
