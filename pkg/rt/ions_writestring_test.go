package rt

import (
	"strings"
	"testing"
)

func TestLGWriterWriteString(t *testing.T) {
	var sb strings.Builder
	w := newLGWriter(&sb, nil)
	n, err := w.WriteString("héllo")
	if err != nil {
		t.Fatalf("WriteString err: %v", err)
	}
	if n != len("héllo") {
		t.Fatalf("n = %d, want %d", n, len("héllo"))
	}
	if sb.String() != "héllo" {
		t.Fatalf("got %q", sb.String())
	}
}
