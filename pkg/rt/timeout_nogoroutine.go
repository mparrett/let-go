//go:build nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"time"

	"github.com/nooga/let-go/pkg/vm"
)

// timeoutChan (nogoroutine): the scheduler build's timer daemon is a
// free-running goroutine, which the synchronous nogoroutine spawn would run
// inline forever. This variant keeps the lane's historical semantics instead:
// the old per-call goroutine ran synchronously under this tag, so (timeout ms)
// blocked for ms and returned an already-closed channel. Do that directly.
func timeoutChan(ms int) vm.Chan {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	ch := make(vm.Chan)
	close(ch)
	return ch
}
