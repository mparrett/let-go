//go:build !nogoroutine

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"context"
	"sync"
	"time"

	"github.com/nooga/let-go/pkg/vm"
)

// --- Shared timeout daemon ---
//
// All (timeout ms) channels are closed by ONE long-lived goroutine instead of
// a goroutine per call. A per-call goroutine is cheap on stock Go, but under
// TinyGo every spawn allocates a full -stack-size goroutine stack (16MB in the
// xsofy wasm lane) and the allocation drives an interpreted conservative-GC
// cycle — measured at ~38s per spawn under the paserati interpreter, i.e. one
// frame of a (<!! (timeout N)) pacing loop. The daemon makes timeout calls
// allocation-free apart from the channel itself.
//
// The daemon runs in the root scope and exits on scope cancellation. Each
// entry carries the Done channel of the scope context that was current when
// its timeout was created, preserving the old per-call semantics: a timeout
// closes early only when ITS OWN context generation is cancelled. A stopping
// daemon therefore closes just the entries whose context is done and hands
// any survivors — timeouts created after a CancelAll installed a fresh
// generation — to a respawned daemon in that new generation.

type timerEntry struct {
	deadline time.Time
	ch       vm.Chan
	done     <-chan struct{} // Done of the scope context at creation time
}

// cancelled reports whether the entry's own context generation is done.
func (e *timerEntry) cancelled() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

var (
	timerMu      sync.Mutex
	timerQueue   []timerEntry
	timerKick    = make(chan struct{}, 1)
	timerRunning bool
)

// timeoutChan returns a channel that the timer daemon closes after ms.
func timeoutChan(ms int) vm.Chan {
	ch := make(vm.Chan)
	if ms <= 0 {
		close(ch)
		return ch
	}
	entry := timerEntry{
		deadline: time.Now().Add(time.Duration(ms) * time.Millisecond),
		ch:       ch,
		done:     vm.Goroutines.Context().Done(),
	}
	timerMu.Lock()
	timerQueue = append(timerQueue, entry)
	start := !timerRunning
	if start {
		timerRunning = true
	}
	timerMu.Unlock()
	if start {
		vm.Goroutines.Go(timerDaemon)
	} else {
		select {
		case timerKick <- struct{}{}:
		default:
		}
	}
	return ch
}

func timerDaemon(ctx context.Context) {
	for {
		timerMu.Lock()
		now := time.Now()
		var due []vm.Chan
		keep := timerQueue[:0]
		var next time.Time
		for _, e := range timerQueue {
			if !e.deadline.After(now) || e.cancelled() {
				due = append(due, e.ch)
			} else {
				keep = append(keep, e)
				if next.IsZero() || e.deadline.Before(next) {
					next = e.deadline
				}
			}
		}
		timerQueue = keep
		timerMu.Unlock()
		for _, ch := range due {
			close(ch)
		}

		if next.IsZero() {
			// Idle: park until a new timeout arrives or the scope cancels.
			select {
			case <-timerKick:
				continue
			case <-ctx.Done():
				timerDaemonStop()
				return
			}
		}
		t := time.NewTimer(time.Until(next))
		select {
		case <-t.C:
		case <-timerKick: // an earlier deadline may have been queued
		case <-ctx.Done():
			t.Stop()
			timerDaemonStop()
			return
		}
		t.Stop()
	}
}

// timerDaemonStop runs when the daemon's own context generation is cancelled.
// It closes the entries belonging to cancelled generations (unblocking their
// takers, matching the old per-goroutine ctx.Done behavior) but NOT entries
// created after a CancelAll installed a fresh generation — those keep their
// deadlines under a respawned daemon. Without the split, a timeout created in
// the race window between CancelAll and the old daemon noticing would be
// closed immediately by a cancellation that predates it.
func timerDaemonStop() {
	timerMu.Lock()
	var closeNow []vm.Chan
	keep := timerQueue[:0]
	for _, e := range timerQueue {
		if e.cancelled() {
			closeNow = append(closeNow, e.ch)
		} else {
			keep = append(keep, e)
		}
	}
	timerQueue = keep
	respawn := len(keep) > 0
	if !respawn {
		timerRunning = false
	}
	timerMu.Unlock()
	for _, ch := range closeNow {
		close(ch)
	}
	if respawn {
		// Survivors belong to a newer generation; serve them from a fresh
		// daemon spawned under the current scope context. If yet another
		// CancelAll raced in, the new daemon's first pass closes the newly
		// cancelled entries via the per-entry check.
		vm.Goroutines.Go(timerDaemon)
	}
}
