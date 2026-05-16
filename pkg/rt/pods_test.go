//go:build !js

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestPodShutdownKillsPodWithoutShutdownOp(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestPodShutdownHelperProcess", "--")
	cmd.Env = append(os.Environ(), "LET_GO_POD_SHUTDOWN_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stderrCh := make(chan []byte, 1)
	go func() {
		stderrBytes, _ := io.ReadAll(stderr)
		stderrCh <- stderrBytes
	}()

	p := &Pod{
		cmd:   cmd,
		stdin: stdin,
	}
	p.Shutdown()

	stderrBytes := <-stderrCh
	if bytes.Contains(stderrBytes, []byte("stdin closed")) {
		t.Fatalf("expected Shutdown to kill non-shutdown pod before closing stdin; stderr: %s", stderrBytes)
	}
}

func TestPodShutdownHelperProcess(t *testing.T) {
	if os.Getenv("LET_GO_POD_SHUTDOWN_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	fmt.Fprint(os.Stderr, "stdin closed")
	os.Exit(0)
}
