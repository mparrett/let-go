//go:build wasip1 && nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"errors"
	"syscall"
)

var keyReadBuf [256]byte

// drainStdin (nogoroutine): one synchronous non-blocking drain pass into
// keyBuf. Requires a host whose fd_read returns EAGAIN when no input is ready
// (paserati's interactive stdin); under a genuinely blocking host the first
// read parks the single task until a key arrives — the accepted limitation of
// the -scheduler=none build.
func drainStdin() {
	keyMu.Lock()
	defer keyMu.Unlock()
	if keyEOF {
		return
	}
	for {
		n, err := syscall.Read(0, keyReadBuf[:])
		if n > 0 {
			keyBuf = append(keyBuf, keyReadBuf[:n]...)
		}
		if err != nil {
			if !errors.Is(err, syscall.EAGAIN) {
				keyEOF = true
			}
			return
		}
		if n == 0 { // EOF
			keyEOF = true
			return
		}
		if n < len(keyReadBuf) {
			return // drained what was available
		}
	}
}
