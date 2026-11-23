package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke/tutorial"
)

func TestTutorialSpec(t *testing.T) {
	spec, err := tutorial.LoadSpec(defaultTutorialSpec)
	if err != nil {
		t.Fatalf("LoadSpec(%q): %v", defaultTutorialSpec, err)
	}
	if spec.Tool != "o-csv" {
		t.Fatalf("spec.Tool = %q, want %q", spec.Tool, "o-csv")
	}
	if len(spec.Sections) < 5 {
		t.Fatalf("expected at least 5 tutorial sections, got %d", len(spec.Sections))
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	td, env, err := newTutorialDriver(spec, cwd)
	if err != nil {
		t.Fatalf("newTutorialDriver: %v", err)
	}

	res, err := tutorial.Run(spec, td, env.Bindings)
	if err != nil {
		t.Fatalf("tutorial.Run failed: %v", err)
	}
	if len(res.Sections) != len(spec.Sections) {
		t.Fatalf("captured %d sections, want %d", len(res.Sections), len(spec.Sections))
	}

	htmlOut := tutorial.RenderHTML(res, tutorial.HTMLOptions{
		VideoPath:  "o-csv.mp4",
		IncludeTOC: true,
	})
	for _, want := range []string{"<!DOCTYPE html>", spec.Title, "o-csv.mp4", "sales.csv", "Step 1"} {
		if !strings.Contains(htmlOut, want) {
			t.Errorf("RenderHTML missing %q", want)
		}
	}

	tapeOut := tutorial.RenderTape(spec, env.Bindings, defaultTutorialTapeOptions())
	for _, want := range []string{"Output o-csv.mp4", "o-csv docs/tutorials/fixtures/sales.csv", "Enter", "Sleep"} {
		if !strings.Contains(tapeOut, want) {
			t.Errorf("RenderTape missing %q", want)
		}
	}

	srtOut := tutorial.RenderSRT(spec)
	if !strings.Contains(srtOut, "00:00:00,000 -->") {
		t.Errorf("RenderSRT missing initial cue timing")
	}
	if !strings.Contains(srtOut, "Open sales.csv") {
		t.Errorf("RenderSRT missing opening caption")
	}

	// Verify the CLI subcommand writes the complete bundle cleanly without touching the fixture.
	fixturePath := filepath.Join(cwd, "docs/tutorials/fixtures/sales.csv")
	beforeFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"tutorial", "build", "--spec", defaultTutorialSpec, "--out", outDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("o-csv tutorial build exited %d: stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	for _, name := range []string{"index.html", "o-csv.tape", "o-csv.srt", "tutorial-data.json"} {
		p := filepath.Join(outDir, name)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing bundle artifact %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("bundle artifact %s is empty", name)
		}
	}

	afterFixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if !bytes.Equal(beforeFixture, afterFixture) {
		t.Fatalf("tutorial build modified fixture %s on disk", fixturePath)
	}
}
