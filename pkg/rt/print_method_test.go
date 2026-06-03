package rt

import (
	"testing"

	"github.com/nooga/let-go/pkg/vm"
)

func TestRenderLeafReadable(t *testing.T) {
	// String readable: should be quoted (via String())
	if got := renderLeaf(vm.String("x"), true); got != `"x"` {
		t.Fatalf("readable string: got %q want %q", got, `"x"`)
	}

	// String human: should be unquoted
	if got := renderLeaf(vm.String("x"), false); got != "x" {
		t.Fatalf("human string: got %q want %q", got, "x")
	}

	// Int: should be "42" regardless of readably flag
	if got := renderLeaf(vm.Int(42), true); got != "42" {
		t.Fatalf("readable int: got %q want %q", got, "42")
	}
	if got := renderLeaf(vm.Int(42), false); got != "42" {
		t.Fatalf("human int: got %q want %q", got, "42")
	}

	// Char readable: should be escaped form like "\x"
	charA := vm.Char('a')
	readableA := renderLeaf(charA, true)
	if readableA != "\\a" {
		t.Fatalf("readable char('a'): got %q want %q", readableA, "\\a")
	}

	// Char human: should be raw character
	humanA := renderLeaf(charA, false)
	if humanA != "a" {
		t.Fatalf("human char('a'): got %q want %q", humanA, "a")
	}
}
