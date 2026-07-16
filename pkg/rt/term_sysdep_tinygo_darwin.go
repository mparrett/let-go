//go:build tinygo && darwin

/*
 * Copyright (c) 2021-2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

// TinyGo-on-darwin terminal sysdep: x/term and x/sys *functions* don't link
// under TinyGo (libSystem trampolines live in .s files it skips), but their
// constants and types are compile-time and remain usable. So this lane keeps
// unix.Termios/unix.PollFd and the TIOC*/flag constants, and performs the
// actual ioctl/poll through syscall.Syscall — which syscall_tinygo_darwin.go
// provides on top of libc syscall(2).

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Raw darwin syscall numbers (sys/syscall.h). x/sys' SYS_* constants aren't
// generated for darwin anymore, so spell them here.
const (
	sysIoctl = 54
	sysPoll  = 230
)

type termState = unix.Termios

func ioctlTermPtr(fd int, req uint, p unsafe.Pointer) error {
	_, _, e := syscall.Syscall(sysIoctl, uintptr(fd), uintptr(req), uintptr(p))
	if e != 0 {
		return e
	}
	return nil
}

// termMakeRaw mirrors x/term.MakeRaw's flag edits exactly (BSD termios).
func termMakeRaw(fd int) (*termState, error) {
	var old unix.Termios
	if err := ioctlTermPtr(fd, unix.TIOCGETA, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := ioctlTermPtr(fd, unix.TIOCSETA, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &old, nil
}

func termRestore(fd int, st *termState) error {
	return ioctlTermPtr(fd, unix.TIOCSETA, unsafe.Pointer(st))
}

func termGetSize(fd int) (width, height int, err error) {
	var ws winsize
	if err := ioctlTermPtr(fd, unix.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return -1, -1, err
	}
	return int(ws.cols), int(ws.rows), nil
}

func termIsTerminal(fd int) bool {
	var t unix.Termios
	return ioctlTermPtr(fd, unix.TIOCGETA, unsafe.Pointer(&t)) == nil
}

func termPoll(fds []unix.PollFd, timeout int) (int, error) {
	var p unsafe.Pointer
	if len(fds) > 0 {
		p = unsafe.Pointer(&fds[0])
	}
	r, _, e := syscall.Syscall(sysPoll, uintptr(p), uintptr(len(fds)), uintptr(timeout))
	if e != 0 {
		return int(r), e
	}
	return int(r), nil
}
