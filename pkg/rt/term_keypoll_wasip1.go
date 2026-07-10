//go:build wasip1

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Non-blocking stdin, the WASI way: poll_oneoff with an fd_read subscription
// on fd 0 plus a zero-timeout clock subscription returns immediately, telling
// us whether a read would block. Only then do we fd_read — so a blocking host
// (wasmtime on a TTY) never parks the instance, and no pump goroutine is
// needed, which keeps -scheduler=none builds goroutine-free too.

//go:wasmimport wasi_snapshot_preview1 poll_oneoff
//go:noescape
func wasiPollOneoff(in, out unsafe.Pointer, nsubscriptions uint32, nevents unsafe.Pointer) uint32

var keyDbg = os.Getenv("LETGO_KEYDBG") != ""

// stdinReady reports whether fd 0 has bytes available right now.
func stdinReady() bool {
	// subscription: userdata u64 | tag u8 (+7 pad) | contents (32B, 8-aligned)
	var subs [2][48]byte
	// sub 0: fd_read on fd 0 (eventtype 1); userdata 0; fd at contents+0
	subs[0][8] = 1 // tag = eventtype_fd_read
	// fd 0 → contents already zero
	// sub 1: clock (eventtype 0), timeout 0 → immediate return even if no input
	binary.LittleEndian.PutUint64(subs[1][0:], 1) // userdata 1 (distinguish)
	subs[1][8] = 0                                // tag = eventtype_clock
	// clock id monotonic(1) at contents+0; timeout/precision 0
	binary.LittleEndian.PutUint32(subs[1][16:], 1)

	// event: userdata u64 | errno u16 | type u8 (+pad) | fd_readwrite{nbytes u64, flags u16}
	var evs [2][32]byte
	var nevents uint32
	errno := wasiPollOneoff(unsafe.Pointer(&subs[0][0]), unsafe.Pointer(&evs[0][0]), 2, unsafe.Pointer(&nevents))
	if errno != 0 {
		if keyDbg {
			fmt.Fprintf(os.Stderr, "[keydbg] poll_oneoff errno=%d\n", errno)
		}
		return false
	}
	for i := 0; i < int(nevents) && i < len(evs); i++ {
		evErrno := binary.LittleEndian.Uint16(evs[i][8:])
		evType := evs[i][10]
		if evType == 1 && evErrno == 0 { // fd_read ready
			return true
		}
	}
	return false
}

// drainStdin pulls whatever stdin has ready into keyBuf without ever issuing
// a read that would block. Called from read-key / key-pending?.
func drainStdin() {
	keyMu.Lock()
	defer keyMu.Unlock()
	if keyEOF {
		return
	}
	var b [256]byte
	for stdinReady() {
		n, err := syscall.Read(0, b[:])
		if keyDbg {
			fmt.Fprintf(os.Stderr, "[keydbg] read n=%d err=%v\n", n, err)
		}
		if n > 0 {
			keyBuf = append(keyBuf, b[:n]...)
		}
		if err != nil {
			if err == syscall.EAGAIN {
				return
			}
			keyEOF = true
			return
		}
		if n == 0 { // EOF
			keyEOF = true
			return
		}
		if n < len(b) {
			return // took everything that was ready
		}
	}
}
