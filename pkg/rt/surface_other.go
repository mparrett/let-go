//go:build !js || !wasm

/*
 * Copyright (c) 2026 Matt Parrett
 * SPDX-License-Identifier: MIT
 *
 * Non-WASM binding for the surface capability. No host surface yet on native
 * (a window or /dev/fb0 binding is the follow-on per #255), so present is a
 * silent no-op and the capability reports unavailable — the zero-value inert
 * binding, like the other host seams. The guest's (surface/available?) guard
 * therefore falls through to its other render path on native/headless.
 */

package rt

func hostPresentSurface(data []byte, w, h int) {}

func hostSurfaceAvailable() bool { return false }
