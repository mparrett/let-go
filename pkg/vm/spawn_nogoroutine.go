//go:build nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package vm

import "time"

// spawn (nogoroutine): run fn synchronously to completion. On the wasm MVP
// target the only goroutine mechanism is asyncify, so this variant executes
// spawned work eagerly instead — fine for fork/join spawn+await; a
// free-running task would block here. Pairs with TinyGo -scheduler=none.
func spawn(fn func()) { fn() }

// awaitWithTimeout (nogoroutine): all spawned work already completed
// synchronously, so the wait returns immediately; no timer goroutine needed.
func awaitWithTimeout(wait func(), timeout time.Duration) bool {
	_ = timeout
	wait()
	return true
}
