package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nooga/let-go/pkg/bytecode"
)

func TestLGBGenUsesSourceBootstrap(t *testing.T) {
	root := repoRoot(t)
	out := filepath.Join(t.TempDir(), "core_compiled.lgb")

	cmd := exec.Command("go", "run", "-tags", "bootstrap", "./cmd/lgbgen", out)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lgbgen source bootstrap failed: %v\n%s", err, output)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat generated lgb: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("generated lgb is empty")
	}
}

// TestLGBGenStripEmitsDebuglessCore exercises the `-strip` flag end to end:
// it regenerates the real core bundle with stripping on and asserts the
// emitted bundle decodes, carries no source maps or local-var tables, has
// FlagLocalVars cleared, and is smaller than the committed full-debug core.
// This guards the lgbgen wiring specifically — bytecode.StripDebug's own
// correctness is covered in pkg/bytecode/strip_test.go.
func TestLGBGenStripEmitsDebuglessCore(t *testing.T) {
	root := repoRoot(t)
	out := filepath.Join(t.TempDir(), "core_stripped.lgb")

	cmd := exec.Command("go", "run", "-tags", "bootstrap", "./cmd/lgbgen", "-strip", out)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lgbgen -strip failed: %v\n%s", err, output)
	}

	stripped, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read stripped bundle: %v", err)
	}

	m, err := bytecode.Decode(bytes.NewReader(stripped))
	if err != nil {
		t.Fatalf("stripped core does not decode: %v", err)
	}
	for i, c := range m.Chunks {
		if len(c.SourceMap) != 0 {
			t.Errorf("chunk %d retains %d source map entries", i, len(c.SourceMap))
		}
		if len(c.LocalVars) != 0 {
			t.Errorf("chunk %d retains %d localvar entries", i, len(c.LocalVars))
		}
	}
	if m.Flags&bytecode.FlagLocalVars != 0 {
		t.Error("FlagLocalVars still set on stripped core")
	}

	// The committed core is the full-debug baseline; stripping must shrink it.
	full, err := os.ReadFile(filepath.Join(root, "pkg", "rt", "core_compiled.lgb"))
	if err != nil {
		t.Fatalf("read committed core: %v", err)
	}
	if len(stripped) >= len(full) {
		t.Fatalf("stripped core not smaller than committed full core: %d >= %d", len(stripped), len(full))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
