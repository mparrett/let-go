/*
 * Copyright (c) 2026 let-go contributors; see CONTRIBUTORS.
 * SPDX-License-Identifier: MIT
 */

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTermFlushWithPipedStdout(t *testing.T) {
	bin := buildLG(t)
	script := filepath.Join(t.TempDir(), "flush.lg")
	if err := os.WriteFile(script, []byte(`(term/write "ok") (term/flush)`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, script).CombinedOutput()
	if err != nil {
		t.Fatalf("term/flush with piped stdout failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "ok" {
		t.Fatalf("got %q, want ok", got)
	}
}
