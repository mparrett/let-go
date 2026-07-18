//go:build js && wasm

/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * HostSurface is the WASM surface sink: (surface/present ...) routes to the
 * bundle's JS host via the global _lgSurface(uint8array, w, h) callback — the
 * binary dual of HostWriter / HostEmitter. The bundle bootstrap
 * (lg-host-core.js) defines _lgSurface per mode: the worker version transfers
 * the pixel buffer to the main thread (zero-copy, via postMessage
 * transferables); the main-thread version calls LetGoHost.onSurface directly.
 * Resolved per call, so boot order doesn't matter and a frame before the shell
 * wires up is dropped.
 *
 * Bound at the *surface* root during WASM boot (see wasm/rendermain.go), exactly
 * where HostEmitter / HostKeySource install.
 */

package rt

import "syscall/js"

// HostSurface forwards frames to the JS _lgSurface global and reports
// availability from the real sink (_lgSurfaceReady), not merely "_lgSurface is
// defined" (which the bundle defines unconditionally).
type HostSurface struct{}

// NewHostSurface returns a HostSurface suitable for the *surface* root binding.
func NewHostSurface() *HostSurface { return &HostSurface{} }

func (HostSurface) Present(rgba []byte, w, h int) {
	fn := js.Global().Get("_lgSurface")
	if fn.IsUndefined() {
		return
	}
	// Copy the Go RGBA bytes into a JS Uint8Array. js.CopyBytesToJS is the one
	// copy on this path; _lgSurface then transfers the underlying ArrayBuffer to
	// the main thread without a second copy. Still vastly cheaper than encoding a
	// PNG: this is the throughput reason graphics needs a buffer seam (#255).
	arr := js.Global().Get("Uint8Array").New(len(rgba))
	js.CopyBytesToJS(arr, rgba)
	fn.Invoke(arr, w, h)
}

// Available reports whether a canvas sink is actually attached. The bundle sets
// globalThis._lgSurfaceReady when the shell calls LetGoHost.onSurface (in worker
// mode via a main->worker message, so the worker's globalThis reflects the
// main-thread sink). Fixes the "browser?" vs "sink?" overpromise.
func (HostSurface) Available() bool {
	return js.Global().Get("_lgSurfaceReady").Truthy()
}
