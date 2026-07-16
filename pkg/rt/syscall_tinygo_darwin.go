//go:build tinygo && darwin

/*
 * Copyright (c) 2021-2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

// TinyGo can't compile the Go assembly that implements syscall.Syscall on
// darwin (stock Go routes it through runtime libSystem trampolines in .s
// files; see tinygo-org/tinygo#4794), so any dependency touching raw
// syscalls — chzyer/readline's termios ioctls, our own FIONREAD polling —
// fails at link with "could not find symbol _syscall.Syscall".
//
// libSystem still exports the (deprecated but functional) variadic
// syscall(2) wrapper, and it IS in TinyGo's macos-minimal-sdk stub list. So
// we provide the missing symbols ourselves: a CGo gateway through libc
// syscall(), pushed onto syscall.Syscall / syscall.Syscall6 via go:linkname.
// Errno comes from __error() (darwin's errno location), matching the
// (r1, r2, errno) contract callers expect.
//
// Deliberately NOT covered: golang.org/x/sys/unix. Its darwin syscalls
// dispatch through per-function trampoline *address* variables initialized
// in .s files TinyGo skips, and go:linkname can't override an
// already-defined Go var (symbol multiply defined). Code that needs x/sys
// or x/term functionality on this lane goes through the term_sysdep seam
// instead (term_sysdep_tinygo_darwin.go), which calls syscall.Syscall with
// raw darwin syscall numbers.

/*
extern long syscall(long, ...);
extern int *__error(void);

static long lg_syscall6(long n, long a1, long a2, long a3, long a4, long a5, long a6, long *errno_out) {
	long r = syscall(n, a1, a2, a3, a4, a5, a6);
	*errno_out = (r == -1) ? *__error() : 0;
	return r;
}
*/
import "C"

import (
	"syscall"
	_ "unsafe" // for go:linkname
)

func libcSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, syscall.Errno) {
	var errno C.long
	r := C.lg_syscall6(C.long(trap),
		C.long(a1), C.long(a2), C.long(a3),
		C.long(a4), C.long(a5), C.long(a6), &errno)
	return uintptr(r), syscall.Errno(errno)
}

//go:linkname sysSyscall syscall.Syscall
func sysSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno) {
	r, e := libcSyscall(trap, a1, a2, a3, 0, 0, 0)
	return r, 0, e
}

//go:linkname sysSyscall6 syscall.Syscall6
func sysSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno) {
	r, e := libcSyscall(trap, a1, a2, a3, a4, a5, a6)
	return r, 0, e
}
