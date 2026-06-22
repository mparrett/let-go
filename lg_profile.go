//go:build lg_profile

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"

	"github.com/nooga/let-go/pkg/rt"
)

var cpuProfile string
var memProfile string

var (
	profileStop     func()
	profileStopOnce sync.Once
)

func init() {
	flag.StringVar(&cpuProfile, "cpuprofile", "",
		"write a Go CPU profile of script/REPL execution to this file "+
			"(build modes -c/-b/-w and the bundled-binary path are not profiled)")
	flag.StringVar(&memProfile, "memprofile", "",
		"write a Go heap profile to this file after script/REPL execution")
}

// startProfiling begins CPU profiling when -cpuprofile is set and arranges for
// stopProfiling to write the heap profile (when -memprofile is set) and stop
// the CPU profile. The explicit stop at the end of main handles normal returns;
// rt.AtExit covers language-level os/exit and System/exit, which skip defers.
func startProfiling() {
	stop := func() {}
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: %v\n", err)
			_ = f.Close()
		} else {
			stop = func() { pprof.StopCPUProfile(); _ = f.Close() }
		}
	}
	if memProfile != "" {
		prev := stop
		stop = func() {
			prev()
			f, err := os.Create(memProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "memprofile: %v\n", err)
				return
			}
			defer f.Close()
			runtime.GC() // materialize up-to-date heap stats before the dump
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "memprofile: %v\n", err)
			}
		}
	}
	profileStop = stop
	rt.AtExit(stopProfiling)
}

func stopProfiling() {
	if profileStop != nil {
		profileStopOnce.Do(profileStop)
	}
}
