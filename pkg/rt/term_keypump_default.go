//go:build wasip1 && !nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
)

var keyOn sync.Once

// drainStdin lazily starts a background goroutine that drains stdin into
// keyBuf, then yields so it gets a chance to run. Under an interactive host
// (paserati with PASERATI_STDIN_NONBLOCK, or a real terminal under wasmtime)
// the blocking os.Stdin.Read parks on the poller between keystrokes; keyBuf
// makes key-pending? a non-blocking check. The yield matters because the
// game's poll loop is a single guest goroutine that otherwise never yields,
// so the pump would starve (wasm has no async preemption).
func drainStdin() {
	keyOn.Do(func() {
		go func() {
			b := make([]byte, 256)
			for {
				n, err := os.Stdin.Read(b)
				if n > 0 {
					keyMu.Lock()
					keyBuf = append(keyBuf, b[:n]...)
					keyMu.Unlock()
				}
				if err != nil {
					// Interactive host returns EAGAIN when no key is ready. If
					// the Go runtime surfaces it here (rather than parking on the
					// poller), don't treat it as EOF — back off and retry so the
					// pump keeps draining as keys arrive.
					if errors.Is(err, syscall.EAGAIN) {
						time.Sleep(2 * time.Millisecond)
						continue
					}
					keyMu.Lock()
					keyEOF = true
					keyMu.Unlock()
					return
				}
			}
		}()
	})
	runtime.Gosched()
}
