//go:build js && wasm

/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * WASM binding for the surface capability: route (surface/present ...) to the
 * bundle's JS host via the global _lgSurface(uint8array, w, h) callback — the
 * binary dual of _lgOutput/_lgEmit. The bundle bootstrap (lg-host-core.js)
 * defines _lgSurface per mode: the worker version transfers the pixel buffer to
 * the main thread (zero-copy, via postMessage transferables); the main-thread
 * version calls LetGoHost.onSurface directly. Resolved per call, so boot order
 * doesn't matter and a frame before the shell wires up is dropped.
 */

package rt

import "syscall/js"

func hostPresentSurface(data []byte, w, h int) {
	fn := js.Global().Get("_lgSurface")
	if fn.IsUndefined() {
		return
	}
	// Copy the Go RGBA bytes into a JS Uint8Array. js.CopyBytesToJS is the one
	// copy on this path; _lgSurface then transfers the underlying ArrayBuffer to
	// the main thread without a second copy. Still vastly cheaper than encoding a
	// PNG: this is the throughput reason graphics needs a buffer seam (#255).
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	fn.Invoke(arr, w, h)
}

func hostSurfaceAvailable() bool {
	return !js.Global().Get("_lgSurface").IsUndefined()
}
