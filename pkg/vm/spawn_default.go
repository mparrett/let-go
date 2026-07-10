//go:build !nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package vm

import "time"

// spawn runs fn on its own goroutine. The nogoroutine build variant runs it
// synchronously instead so the module links with no goroutine machinery at
// all (TinyGo -scheduler=none: no asyncify gowrapper on the wasm MVP target).
func spawn(fn func()) { go fn() }

// awaitWithTimeout runs the (blocking) wait on a helper goroutine and returns
// false if it does not finish within timeout. Non-positive timeout waits
// indefinitely.
func awaitWithTimeout(wait func(), timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}
