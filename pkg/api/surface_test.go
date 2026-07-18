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
