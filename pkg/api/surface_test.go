package api_test

import (
	"bytes"
	"testing"

	"github.com/nooga/let-go/pkg/api"
	"github.com/nooga/let-go/pkg/vm"
)

// captureSurface records the last presented frame, the graphics dual of the
// capturing callback in TestWithEmit. Reports available.
type captureSurface struct {
	rgba    []byte
	w, h, n int
}

func (c *captureSurface) Present(rgba []byte, w, h int) {
	c.rgba = append([]byte(nil), rgba...) // copy — the guest reuses its buffer
	c.w, c.h, c.n = w, h, c.n+1
}
func (c *captureSurface) Available() bool { return true }

// TestWithSurface proves the graphics dual of TestWithEmit: an rt.Surface passed
// via api.WithSurface receives the RGBA frame a (surface/present ...) call
// dispatches, and (surface/available?) reflects the binding. This is the seam's
// first test — the build-tag no-op it replaced had no embedder binding to test
// against.
func TestWithSurface(t *testing.T) {
	cap := &captureSurface{}
	lg, err := api.NewLetGo("withsurface-test", api.WithSurface(cap))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Run(`(surface/present (byte-array [255 0 0 255]) 1 1)`); err != nil {
		t.Fatal(err)
	}
	if cap.n != 1 {
		t.Fatalf("expected 1 present, got %d", cap.n)
	}
	if !bytes.Equal(cap.rgba, []byte{255, 0, 0, 255}) {
		t.Errorf("rgba = %v, want [255 0 0 255]", cap.rgba)
	}
	if cap.w != 1 || cap.h != 1 {
		t.Errorf("size = %dx%d, want 1x1", cap.w, cap.h)
	}
	v, err := lg.Run(`(surface/available?)`)
	if err != nil {
		t.Fatal(err)
	}
	if v != vm.TRUE {
		t.Errorf("available? = %v, want true", v)
	}
}

// TestSurfaceNoopWithoutOption proves (surface/present ...) is harmless — validates
// args and returns nil — when no sink is bound (the default nopSurface root), and
// (surface/available?) is false. A bad rgba type still errors, so native dev
// catches type bugs.
func TestSurfaceNoopWithoutOption(t *testing.T) {
	lg, err := api.NewLetGo("surface-noop-test")
	if err != nil {
		t.Fatal(err)
	}
	v, err := lg.Run(`(surface/available?)`)
	if err != nil {
		t.Fatal(err)
	}
	if v != vm.FALSE {
		t.Errorf("available? with no sink = %v, want false", v)
	}
	if _, err := lg.Run(`(surface/present (byte-array [0 0 0 0]) 1 1)`); err != nil {
		t.Fatalf("present with no sink should no-op, got %v", err)
	}
	if _, err := lg.Run(`(surface/present 42 1 1)`); err == nil {
		t.Error("expected byte-array validation error for non-buffer rgba, got nil")
	}
}

// TestSurfaceValidation drives every invalid-frame class through
// (surface/present ...) with a live capturing sink and proves none of them
// reach it: wrong rgba type (including String, which asBytes would otherwise
// coerce), zero/negative/oversized dimensions (the pre-multiply bound that
// keeps w*h*4 from overflowing), and inexact buffer lengths in both
// directions (ImageData wants exactly w*h*4).
func TestSurfaceValidation(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"string rgba", `(surface/present "abcd" 1 1)`},
		{"int rgba", `(surface/present 42 1 1)`},
		{"zero width", `(surface/present (byte-array []) 0 1)`},
		{"zero height", `(surface/present (byte-array []) 1 0)`},
		{"negative dims", `(surface/present (byte-array [0 0 0 0]) -1 -1)`},
		{"dim over cap", `(surface/present (byte-array [0 0 0 0]) 16385 1)`},
		{"overflow-scale dims", `(surface/present (byte-array [0 0 0 0]) 2147483647 2147483647)`},
		{"undersized buffer", `(surface/present (byte-array [0 0 0 0]) 2 2)`},
		{"oversized buffer", `(surface/present (byte-array [0 0 0 0 0 0 0 0]) 1 1)`},
	}
	cap := &captureSurface{}
	lg, err := api.NewLetGo("surface-validation-test", api.WithSurface(cap))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if _, err := lg.Run(tc.expr); err == nil {
			t.Errorf("%s: expected validation error, got nil", tc.name)
		}
	}
	if cap.n != 0 {
		t.Fatalf("invalid frames reached the sink: %d presents", cap.n)
	}
	// The exact-size happy path still lands.
	if _, err := lg.Run(`(surface/present (byte-array [1 2 3 4 5 6 7 8]) 2 1)`); err != nil {
		t.Fatal(err)
	}
	if cap.n != 1 || cap.w != 2 || cap.h != 1 {
		t.Fatalf("valid frame not presented: n=%d size=%dx%d", cap.n, cap.w, cap.h)
	}
}

// panicSurface panics on every Present — the native stand-in for a throwing
// _lgSurface JS callback, which syscall/js surfaces as a Go panic.
type panicSurface struct{ n int }

func (p *panicSurface) Present([]byte, int, int) { p.n++; panic("sink exploded") }
func (p *panicSurface) Available() bool          { return true }

// TestSurfacePanickingSinkDropsFrame proves the fire-and-forget contract under
// sink failure: a panicking sink drops the frame (no error, no runtime crash)
// and execution continues — including further presents into the same sink.
func TestSurfacePanickingSinkDropsFrame(t *testing.T) {
	sink := &panicSurface{}
	lg, err := api.NewLetGo("surface-panic-test", api.WithSurface(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Run(`(surface/present (byte-array [0 0 0 0]) 1 1)`); err != nil {
		t.Fatalf("panicking sink should drop the frame silently, got %v", err)
	}
	if _, err := lg.Run(`(surface/present (byte-array [0 0 0 0]) 1 1)`); err != nil {
		t.Fatalf("second present after a sink panic should still run, got %v", err)
	}
	if sink.n != 2 {
		t.Errorf("sink invoked %d times, want 2 (one attempt per present)", sink.n)
	}
	// The runtime is intact: ordinary evaluation still works.
	if v, err := lg.Run(`(+ 1 2)`); err != nil || v != vm.Int(3) {
		t.Fatalf("runtime damaged after sink panics: v=%v err=%v", v, err)
	}
}
