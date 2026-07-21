package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nooga/let-go/pkg/perfdata"
)

func writeMachineBaseline(t *testing.T, path, arch, cpu string, ratios map[string]float64) {
	t.Helper()
	m := Machine{Arch: arch, CPUModel: cpu}
	benches := map[string]BenchmarkEntry{}
	for k, r := range ratios {
		benches[k] = BenchmarkEntry{RatioToAnchor: r}
	}
	multi := MultiBaseline{
		Version: 2,
		Machines: map[string]Baseline{
			perfdata.MachineKey(m): {Machine: m, Benchmarks: benches},
		},
	}
	b, err := json.Marshal(multi)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSplitBenchmarkName(t *testing.T) {
	pkg, name := splitBenchmarkName("github.com/nooga/let-go/pkg/vm.BenchmarkFuncInvoke/Direct")
	if pkg != "pkg/vm" {
		t.Fatalf("package = %q, want pkg/vm", pkg)
	}
	if name != "BenchmarkFuncInvoke/Direct" {
		t.Fatalf("name = %q, want BenchmarkFuncInvoke/Direct", name)
	}

	pkg, name = splitBenchmarkName("github.com/nooga/let-go/pkg/ir.BenchmarkIRCompile [gogen_ir]")
	if pkg != "pkg/ir" {
		t.Fatalf("package = %q, want pkg/ir", pkg)
	}
	if name != "BenchmarkIRCompile [gogen_ir]" {
		t.Fatalf("name = %q, want BenchmarkIRCompile [gogen_ir]", name)
	}
}

func TestCompareWithHistorical(t *testing.T) {
	current := Baseline{Benchmarks: map[string]BenchmarkEntry{
		"pkg.BenchmarkA": {RatioToAnchor: 80},
		"pkg.BenchmarkB": {RatioToAnchor: 120},
		"pkg.BenchmarkC": {RatioToAnchor: 50},
	}}
	reference := Baseline{Benchmarks: map[string]BenchmarkEntry{
		"pkg.BenchmarkA": {RatioToAnchor: 100},
		"pkg.BenchmarkB": {RatioToAnchor: 100},
		"pkg.BenchmarkD": {RatioToAnchor: 10},
	}}

	changes, summary := compare(current, reference)
	if summary.Common != 2 {
		t.Fatalf("common = %d, want 2", summary.Common)
	}
	if summary.New != 1 {
		t.Fatalf("new = %d, want 1", summary.New)
	}
	if summary.Missing != 1 {
		t.Fatalf("missing = %d, want 1", summary.Missing)
	}
	if summary.Faster != 1 {
		t.Fatalf("faster = %d, want 1", summary.Faster)
	}
	if summary.Slower != 1 {
		t.Fatalf("slower = %d, want 1", summary.Slower)
	}
	if summary.MedianDelta != 0 {
		t.Fatalf("median delta = %v, want 0", summary.MedianDelta)
	}
	if got := changes["pkg.BenchmarkA"]; !near(got, -0.2) {
		t.Fatalf("BenchmarkA delta = %v, want -0.2", got)
	}
	if got := changes["pkg.BenchmarkB"]; !near(got, 0.2) {
		t.Fatalf("BenchmarkB delta = %v, want 0.2", got)
	}
}

func TestVersionLessOrdersNumerically(t *testing.T) {
	// The trap: plain string order puts "v1.9.0" after "v1.10.0" once MINOR
	// reaches two digits. versionLess must compare component-wise.
	if !versionLess("v1.9.0", "v1.10.0") {
		t.Fatal("v1.9.0 should sort before v1.10.0")
	}
	if versionLess("v1.11.0", "v1.11.0") {
		t.Fatal("equal versions are not less")
	}
	// versionLess is the ASCENDING comparator; the display sorts descending, so
	// versioned stems land first there. That means ascending puts non-versioned
	// stems earlier: versionLess("nightly","v1.8.0") is true, not the reverse.
	if !versionLess("nightly", "v1.8.0") {
		t.Fatal("non-versioned stem should sort earlier ascending (later in display)")
	}
	if versionLess("v1.8.0", "nightly") {
		t.Fatal("versioned stem should not sort earlier ascending")
	}
}

func TestBuildAnchorPayloadsSkewAndMachine(t *testing.T) {
	dir := t.TempDir()
	current := Baseline{
		Machine: Machine{Arch: "arm64", CPUModel: "Apple M2"},
		Benchmarks: map[string]BenchmarkEntry{
			"pkg.BenchmarkA":   {RatioToAnchor: 80},
			"pkg.BenchmarkB":   {RatioToAnchor: 120},
			"pkg.BenchmarkNew": {RatioToAnchor: 50}, // added since the anchor
		},
	}
	// Same-machine anchor missing one current benchmark and carrying one the
	// current run dropped.
	writeMachineBaseline(t, filepath.Join(dir, "v1.9.0.json"), "arm64", "Apple M2", map[string]float64{
		"pkg.BenchmarkA": 100, "pkg.BenchmarkB": 100, "pkg.BenchmarkGone": 10,
	})
	// Cross-machine anchor.
	writeMachineBaseline(t, filepath.Join(dir, "v1.8.0.json"), "arm64", "Apple M3", map[string]float64{
		"pkg.BenchmarkA": 90,
	})

	got := buildAnchorPayloads(current, dir)
	if len(got) != 2 {
		t.Fatalf("payloads = %d, want 2", len(got))
	}
	// Newest first.
	if got[0].Name != "v1.9.0" || got[1].Name != "v1.8.0" {
		t.Fatalf("order = %q,%q; want v1.9.0,v1.8.0", got[0].Name, got[1].Name)
	}
	v190 := got[0]
	if !v190.SameMachine {
		t.Fatal("v1.9.0 captured on Apple M2 should be same-machine")
	}
	if v190.Added != 1 { // BenchmarkNew
		t.Fatalf("v1.9.0 added = %d, want 1", v190.Added)
	}
	if v190.Missing != 1 { // BenchmarkGone
		t.Fatalf("v1.9.0 missing = %d, want 1", v190.Missing)
	}
	if got[1].SameMachine {
		t.Fatal("v1.8.0 captured on Apple M3 should be cross-machine")
	}
}

func TestBuildCharts(t *testing.T) {
	timeline := []Snapshot{
		makeSnapshot("a", Baseline{
			CapturedAt:    "2026-06-01T00:00:00Z",
			CapturedAtSHA: "aaaaaaaaaaaa",
			Benchmarks: map[string]BenchmarkEntry{
				"github.com/nooga/let-go/test.BenchmarkClojureTestSuite [bytecode]": {RatioToAnchor: 100, AllocsPerOp: 10, BytesPerOp: 1000},
			},
		}),
		makeSnapshot("b", Baseline{
			CapturedAt:    "2026-06-02T00:00:00Z",
			CapturedAtSHA: "bbbbbbbbbbbb",
			Benchmarks: map[string]BenchmarkEntry{
				"github.com/nooga/let-go/test.BenchmarkClojureTestSuite [bytecode]": {RatioToAnchor: 80, AllocsPerOp: 9, BytesPerOp: 900},
			},
		}),
	}

	charts := buildCharts(timeline, Baseline{}, "", defaultBudgetFraction)
	if len(charts) != 3 {
		t.Fatalf("chart count = %d, want 3", len(charts))
	}
	if charts[0].Title != "End-to-end suite" {
		t.Fatalf("first chart = %q, want End-to-end suite", charts[0].Title)
	}
	if len(charts[0].Series) != 1 {
		t.Fatalf("series count = %d, want 1", len(charts[0].Series))
	}
	if len(charts[0].Series[0].Points) != 2 {
		t.Fatalf("point count = %d, want 2", len(charts[0].Series[0].Points))
	}
	if charts[0].Series[0].Path == "" {
		t.Fatal("expected SVG path")
	}
	if charts[0].Series[0].Points[1].X <= charts[0].Series[0].Points[0].X {
		t.Fatalf("x coordinates did not advance: %#v", charts[0].Series[0].Points)
	}
}

func near(got, want float64) bool {
	diff := got - want
	return diff < 0.0000001 && diff > -0.0000001
}

func TestFormatNS(t *testing.T) {
	tests := map[float64]string{
		12.345:         "12.35 ns",
		12_345:         "12.35 us",
		12_345_000:     "12.35 ms",
		12_345_000_000: "12.35 s",
	}
	for input, want := range tests {
		if got := formatNS(input); got != want {
			t.Fatalf("formatNS(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadTimelineSkipsCorruptSnapshots(t *testing.T) {
	dir := t.TempDir()
	timelineDir := filepath.Join(dir, "timeline")
	historicalDir := filepath.Join(dir, "historical")
	if err := os.MkdirAll(timelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(historicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	good := `{"version":1,"captured_at":"2026-06-01T00:00:00Z","captured_at_sha":"abc","benchmarks":{"pkg.BenchmarkA":{"ns_per_op":1,"ratio_to_anchor":2}}}`
	if err := os.WriteFile(filepath.Join(timelineDir, "good.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(timelineDir, "bad.json"), []byte(`{"version":`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stderr strings.Builder
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	snapshots, err := loadTimeline(timelineDir, historicalDir, Baseline{})
	_ = w.Close()
	os.Stderr = oldStderr
	if _, copyErr := io.Copy(&stderr, r); copyErr != nil {
		t.Fatal(copyErr)
	}
	if err != nil {
		t.Fatalf("loadTimeline returned error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if !strings.Contains(stderr.String(), "skipping") {
		t.Fatalf("stderr = %q, want skip warning", stderr.String())
	}
}

func TestFormatRatioCompactsLargeValues(t *testing.T) {
	tests := map[float64]string{
		1_124_520_183: "1.12B",
		8_519_621:     "8.52M",
		66_309:        "66.3k",
		16.209:        "16.2",
		4.688:         "4.69",
	}
	for input, want := range tests {
		if got := formatRatio(input); got != want {
			t.Fatalf("formatRatio(%v) = %q, want %q", input, got, want)
		}
	}
}
