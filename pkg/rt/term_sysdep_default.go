//go:build !js && !plan9 && !wasip1 && !(tinygo && darwin)

/*
 * Copyright (c) 2021-2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

// Terminal sysdep seam: raw-mode/size/poll primitives behind five small
// functions so term.go stays platform-agnostic. This default lane wraps
// golang.org/x/term and unix.Poll as before; the tinygo && darwin lane
// (term_sysdep_tinygo_darwin.go) reimplements them over raw syscalls
// because TinyGo can't link x/sys' libSystem trampolines (#4794).

import (
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type termState = term.State

func termMakeRaw(fd int) (*termState, error) { return term.MakeRaw(fd) }

func termRestore(fd int, st *termState) error { return term.Restore(fd, st) }

func termGetSize(fd int) (width, height int, err error) { return term.GetSize(fd) }

func termIsTerminal(fd int) bool { return term.IsTerminal(fd) }

func termPoll(fds []unix.PollFd, timeout int) (int, error) { return unix.Poll(fds, timeout) }
