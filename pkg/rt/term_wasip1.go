//go:build wasip1

/*
 * Copyright (c) 2021-2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/nooga/let-go/pkg/vm"
)

// WASI terminal backend. The native (term.go) and browser (term_wasm.go)
// installs both exclude wasip1, so before this file a WASI build had no `term`
// namespace at all — the reason a game like xsofy couldn't boot under wasmtime
// or the paserati wasm transpiler.
//
// Output is free: every escape routes through WriteToOut → *out* → os.Stdout,
// which is fd_write under WASI. The gap is input. WASI has no termios, so
// stdin can't be put in raw mode and there is no SharedArrayBuffer key ring
// like the browser host — read-key falls back to a blocking fd_read that the
// host delivers line-buffered. That's enough to satisfy "press any key"
// prompts; per-keystroke and escape-sequence input need a real host channel.

func init() { RegisterInstaller(installTermNS) }

// Stdin bytes are drained into keyBuf so read-key / key-pending? never park
// the game loop. How the drain runs is a build-tagged seam (drainStdin): a
// background pump goroutine normally (term_keypump_default.go), a synchronous
// non-blocking read pass under the nogoroutine tag for TinyGo -scheduler=none
// (term_keypump_nogoroutine.go).
var (
	keyMu  sync.Mutex
	keyBuf []byte
	keyEOF bool
)

// wasiKeySource hands out keys buffered by the pump, one token per call. It
// runs keyBuf through the shared scanKey tokenizer (key_tokenizer.go), so a
// burst like "llll" yields four "l" and a multi-byte escape sequence
// ("\x1b[A" for the up arrow, SGR mouse reports) stays a single key — the same
// contract native/browser read-key honor, which is what the game's key tables
// match against.
type wasiKeySource struct{}

// completeKeyLocked reports whether keyBuf holds at least one full key token.
// A partial escape sequence (keyNeedMore) is only "complete" at EOF, where
// there are no more bytes coming and we emit best-effort. Caller holds keyMu.
func completeKeyLocked() bool {
	if len(keyBuf) == 0 {
		return false
	}
	status, n := scanKey(keyBuf)
	return keyEOF || (status == keyReady && n > 0)
}

func (wasiKeySource) ReadKey() (string, error) {
	drainStdin()
	keyMu.Lock()
	defer keyMu.Unlock()
	if keyDbg {
		fmt.Fprintf(os.Stderr, "[keydbg] ReadKey buf=%q\n", string(keyBuf))
	}
	if len(keyBuf) == 0 {
		return "", nil // nothing buffered — read-key nil contract
	}
	status, n := scanKey(keyBuf)
	if status == keyNeedMore && !keyEOF {
		return "", nil // partial escape sequence — wait for the rest
	}
	if n == 0 {
		return "", nil
	}
	s := string(keyBuf[:n])
	keyBuf = keyBuf[n:]
	return s, nil
}

var kpDbgCount = 0

func (wasiKeySource) KeyPending() bool {
	drainStdin()
	keyMu.Lock()
	defer keyMu.Unlock()
	r := completeKeyLocked()
	if keyDbg {
		kpDbgCount++
		if kpDbgCount <= 5 || r || len(keyBuf) > 0 {
			fmt.Fprintf(os.Stderr, "[keydbg] KeyPending #%d buf=%q -> %v\n", kpDbgCount, string(keyBuf), r)
		}
	}
	return r
}

// termSize returns a fixed 80x24, overridable via the COLUMNS / LINES env vars
// (WASI has no TIOCGWINSZ, so there is nothing to query).
func termSize() (int, int) {
	w, h := 80, 24
	if v, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && v > 0 {
		w = v
	}
	if v, err := strconv.Atoi(os.Getenv("LINES")); err == nil && v > 0 {
		h = v
	}
	return w, h
}

func installTermNS() {
	// Signal the WASM environment to user code, and install the stdin key
	// source at the *keys* root (the input dual of *out*).
	CoreNS.Lookup("*in-wasm*").(*vm.Var).SetRoot(vm.TRUE)
	CoreNS.Lookup("*keys*").(*vm.Var).SetRoot(vm.NewBoxed(wasiKeySource{}))

	ns := vm.NewNamespace("term")
	ns.Refer(CoreNS, "", true)

	// raw-mode! / restore-mode! — no-op: WASI stdin can't be put in raw mode.
	rawMode, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.TRUE, nil
	})
	ns.Def("raw-mode!", rawMode)
	restoreMode, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.TRUE, nil
	})
	ns.Def("restore-mode!", restoreMode)

	// read-key — blocks on the bound *keys* source (wasiKeySource → stdin).
	readKey := vm.NewCtxNativeFn("read-key", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		s, err := boundKeySource(ec).ReadKey()
		if err != nil {
			return vm.NIL, err
		}
		if s == "" {
			return vm.NIL, nil
		}
		return vm.String(s), nil
	})
	ns.Def("read-key", readKey)

	keyPending := vm.NewCtxNativeFn("key-pending?", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if boundKeySource(ec).KeyPending() {
			return vm.TRUE, nil
		}
		return vm.FALSE, nil
	})
	ns.Def("key-pending?", keyPending)

	// size — fixed / env-driven (see termSize).
	sizeFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		w, h := termSize()
		return vm.NewPersistentVector([]vm.Value{vm.MakeInt(w), vm.MakeInt(h)}), nil
	})
	ns.Def("size", sizeFn)

	// --- Output: identical to native/browser, ANSI through *out* (fd_write) ---

	moveCursor := vm.NewCtxNativeFn("move-cursor", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("move-cursor expects 2 args (col row)")
		}
		col, ok1 := vs[0].(vm.Int)
		row, ok2 := vs[1].(vm.Int)
		if !ok1 || !ok2 {
			return vm.NIL, fmt.Errorf("move-cursor expects integers")
		}
		return vm.NIL, WriteToOut(ec, fmt.Sprintf("\033[%d;%dH", int(row), int(col)))
	})
	ns.Def("move-cursor", moveCursor)

	clearFn := vm.NewCtxNativeFn("clear", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[2J")
	})
	ns.Def("clear", clearFn)

	clearLine := vm.NewCtxNativeFn("clear-line", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[2K")
	})
	ns.Def("clear-line", clearLine)

	hideCursor := vm.NewCtxNativeFn("hide-cursor", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?25l")
	})
	ns.Def("hide-cursor", hideCursor)

	showCursor := vm.NewCtxNativeFn("show-cursor", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?25h")
	})
	ns.Def("show-cursor", showCursor)

	setFg := vm.NewCtxNativeFn("set-fg", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		seq, err := colorSeq("set-fg", 38, vs)
		if err != nil {
			return vm.NIL, err
		}
		return vm.NIL, WriteToOut(ec, seq)
	})
	ns.Def("set-fg", setFg)

	setBg := vm.NewCtxNativeFn("set-bg", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		seq, err := colorSeq("set-bg", 48, vs)
		if err != nil {
			return vm.NIL, err
		}
		return vm.NIL, WriteToOut(ec, seq)
	})
	ns.Def("set-bg", setBg)

	resetStyle := vm.NewCtxNativeFn("reset-style", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[0m")
	})
	ns.Def("reset-style", resetStyle)

	bold := vm.NewCtxNativeFn("bold", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[1m")
	})
	ns.Def("bold", bold)

	underline := vm.NewCtxNativeFn("underline", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[4m")
	})
	ns.Def("underline", underline)

	inverse := vm.NewCtxNativeFn("inverse", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[7m")
	})
	ns.Def("inverse", inverse)

	writeFn := vm.NewCtxNativeFn("write", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("write expects 1 arg")
		}
		return vm.NIL, WriteToOut(ec, valueToString(vs[0]))
	})
	ns.Def("write", writeFn)

	writeAt := vm.NewCtxNativeFn("write-at", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if len(vs) != 3 {
			return vm.NIL, fmt.Errorf("write-at expects 3 args (col row str)")
		}
		col, ok1 := vs[0].(vm.Int)
		row, ok2 := vs[1].(vm.Int)
		if !ok1 || !ok2 {
			return vm.NIL, fmt.Errorf("write-at expects integers for col and row")
		}
		return vm.NIL, WriteToOut(ec, fmt.Sprintf("\033[%d;%dH%s", int(row), int(col), valueToString(vs[2])))
	})
	ns.Def("write-at", writeAt)

	charWidth, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("char-width expects 1 arg")
		}
		s, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("char-width expects string")
		}
		r, _ := utf8.DecodeRuneInString(string(s))
		if r == utf8.RuneError {
			return vm.MakeInt(0), nil
		}
		return vm.MakeInt(1), nil
	})
	ns.Def("char-width", charWidth)

	altScreen := vm.NewCtxNativeFn("alternate-screen", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?1049h")
	})
	ns.Def("alternate-screen", altScreen)

	mainScreen := vm.NewCtxNativeFn("main-screen", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?1049l")
	})
	ns.Def("main-screen", mainScreen)

	enableMouse := vm.NewCtxNativeFn("enable-mouse", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?1000;1006h")
	})
	ns.Def("enable-mouse", enableMouse)

	disableMouse := vm.NewCtxNativeFn("disable-mouse", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		return vm.NIL, WriteToOut(ec, "\033[?1000;1006l")
	})
	ns.Def("disable-mouse", disableMouse)

	// flush — best-effort sync of the active *out* handle. WASI has no fsync
	// for stdout (os.Stdout is a file but its Sync → "not implemented"), and
	// fd_write is unbuffered host-side, so a failed stdout sync is harmless;
	// a rebound file/buffer handle still flushes. Swallow the error either way.
	flushFn := vm.NewCtxNativeFn("flush", func(ec *vm.ExecContext, vs []vm.Value) (vm.Value, error) {
		if h := resolveIOHandleVar(ec, "*out*"); h != nil {
			_ = h.Sync()
		}
		return vm.NIL, nil
	})
	ns.Def("flush", flushFn)

	// NewNamespace alone doesn't make `term` findable — register it so
	// (require 'term) and term/* resolution work (mirrors term.go).
	RegisterNS(ns)
}

// colorSeq builds an SGR fg/bg escape for the 1-arg (256-color) or 3-arg
// (truecolor) form. base is 38 for foreground, 48 for background.
func colorSeq(name string, base int, vs []vm.Value) (string, error) {
	switch len(vs) {
	case 1:
		c, ok := vs[0].(vm.Int)
		if !ok {
			return "", fmt.Errorf("%s expects integer color code", name)
		}
		return fmt.Sprintf("\033[%d;5;%dm", base, int(c)), nil
	case 3:
		r, ok1 := vs[0].(vm.Int)
		g, ok2 := vs[1].(vm.Int)
		b, ok3 := vs[2].(vm.Int)
		if !ok1 || !ok2 || !ok3 {
			return "", fmt.Errorf("%s expects 3 integers (r g b)", name)
		}
		return fmt.Sprintf("\033[%d;2;%d;%d;%dm", base, int(r), int(g), int(b)), nil
	default:
		return "", fmt.Errorf("%s expects 1 or 3 args", name)
	}
}

func valueToString(v vm.Value) string {
	if s, ok := v.(vm.String); ok {
		return string(s)
	}
	return v.String()
}
