package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/tutorial"
)

const (
	defaultTutorialSpec = "docs/tutorials/o-csv.json"
	defaultTutorialOut  = "docs/tutorials/out"
)

// tutorialDriver adapts o-csv's synchronous headless driver to tutorial.Driver.
type tutorialDriver struct {
	d *driver
}

func (td *tutorialDriver) SendKey(key string) error {
	if key == "" {
		return nil
	}
	if msg, ok := tutorialSpecialKeyMsg(key); ok {
		td.d.send(msg)
		return nil
	}
	if utf8.RuneCountInString(key) == 1 {
		if key == " " {
			td.d.send(keyMsg("space"))
			return nil
		}
		td.d.send(keyMsg(key))
		return nil
	}
	td.d.typeText(key)
	return nil
}

func (td *tutorialDriver) RenderANSI() string {
	return td.d.sh.View().Content
}

func (td *tutorialDriver) RenderPlain() string {
	return plain(td.d.sh.View().Content)
}

func tutorialSpecialKeyMsg(key string) (tea.KeyPressMsg, bool) {
	lower := strings.ToLower(key)
	switch lower {
	case "enter", "return":
		return keyMsg("enter"), true
	case "esc", "escape":
		return keyMsg("esc"), true
	case "tab":
		return keyMsg("tab"), true
	case "backtab", "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, true
	case "backspace":
		return keyMsg("backspace"), true
	case "delete", "del":
		return tea.KeyPressMsg{Code: tea.KeyDelete}, true
	case "space":
		return keyMsg("space"), true
	case "up", "down", "left", "right", "pgup", "pgdown":
		return keyMsg(lower), true
	case "pageup":
		return keyMsg("pgup"), true
	case "pagedown", "pgdn":
		return keyMsg("pgdown"), true
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}, true
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}, true
	}
	if strings.HasPrefix(lower, "ctrl+") || strings.HasPrefix(lower, "alt+") || strings.HasPrefix(lower, "shift+") {
		return keyMsg(lower), true
	}
	return tea.KeyPressMsg{}, false
}

// tutorialLaunch describes the parsed CLI arguments from Spec.Command.
type tutorialLaunch struct {
	file string
	opts Options
	edit bool
}

func parseTutorialCommand(command string) (tutorialLaunch, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return tutorialLaunch{}, nil
	}
	// Strip leading binary name (e.g. "o-csv").
	if filepath.Base(fields[0]) == "o-csv" {
		fields = fields[1:]
	}
	fl := flag.NewFlagSet("o-csv-tutorial-cmd", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	edit := fl.Bool("edit", false, "")
	delim := fl.String("delim", "", "")
	noHeader := fl.Bool("no-header", false, "")
	if err := fl.Parse(fields); err != nil {
		return tutorialLaunch{}, fmt.Errorf("parse spec command %q: %w", command, err)
	}
	d, err := ParseDelim(*delim)
	if err != nil {
		return tutorialLaunch{}, err
	}
	return tutorialLaunch{
		file: fl.Arg(0),
		opts: Options{Delim: d, NoHeader: *noHeader},
		edit: *edit,
	}, nil
}

// newTutorialDriver initializes a deterministic headless o-csv session for spec.
// It never writes to disk and never touches the system clipboard.
func newTutorialDriver(spec tutorial.Spec, baseDir string) (*tutorialDriver, *Env, error) {
	launch, err := parseTutorialCommand(spec.Command)
	if err != nil {
		return nil, nil, err
	}
	workDir := baseDir
	if spec.WorkDir != "" {
		if filepath.IsAbs(spec.WorkDir) {
			workDir = spec.WorkDir
		} else if baseDir != "" {
			workDir = filepath.Join(baseDir, spec.WorkDir)
		} else {
			workDir = spec.WorkDir
		}
	}

	var t *Table
	displayName := launch.file
	if launch.file == "" {
		t, err = Read(strings.NewReader(sampleCSV), launch.opts)
		displayName = "sample.csv"
	} else {
		csvPath := launch.file
		if !filepath.IsAbs(csvPath) && workDir != "" {
			csvPath = filepath.Join(workDir, csvPath)
		}
		opts := launch.opts
		if opts.Delim == 0 {
			opts.Delim = extDelim(csvPath)
		}
		t, err = ReadFile(csvPath, opts)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open tutorial fixture %q: %w", launch.file, err)
	}

	doc := newDoc(displayName, t, launch.opts.NoHeader)
	doc.Path = "" // never write to disk during headless tutorial capture
	env := newEnv(Config{NoHeader: launch.opts.NoHeader, Cwd: workDir})
	sh := newShell(env, doc, launch.edit)
	w, h := spec.Width, spec.Height
	if w <= 0 {
		w = tutorial.DefaultWidth
	}
	if h <= 0 {
		h = tutorial.DefaultHeight
	}
	return &tutorialDriver{d: newDriver(sh, w, h)}, env, nil
}

type tutorialBuildOptions struct {
	specPath string
	outDir   string
	runVHS   bool
}

type tutorialBuildSummary struct {
	Result tutorial.Result
	Paths  tutorial.BundlePaths
	MP4    string
}

func defaultTutorialTapeOptions() tutorial.TapeOptions {
	return tutorial.TapeOptions{
		OutputMP4:   "o-csv.mp4",
		FontSize:    15,
		WidthPx:     1280,
		HeightPx:    760,
		Padding:     16,
		TypingSpeed: "55ms",
		LaunchMs:    1500,
	}
}

func buildTutorial(opts tutorialBuildOptions) (tutorialBuildSummary, error) {
	spec, err := tutorial.LoadSpec(opts.specPath)
	if err != nil {
		return tutorialBuildSummary{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return tutorialBuildSummary{}, err
	}
	td, env, err := newTutorialDriver(spec, cwd)
	if err != nil {
		return tutorialBuildSummary{}, err
	}
	res, err := tutorial.Run(spec, td, env.Bindings)
	if err != nil {
		return tutorialBuildSummary{}, err
	}

	bundleOpts := tutorial.BundleOptions{
		HTML: tutorial.HTMLOptions{IncludeTOC: true},
		Tape: defaultTutorialTapeOptions(),
	}
	paths, err := tutorial.WriteBundle(opts.outDir, res, env.Bindings, bundleOpts)
	if err != nil {
		return tutorialBuildSummary{}, err
	}

	summary := tutorialBuildSummary{Result: res, Paths: paths}
	if opts.runVHS {
		vhsBin, err := findVHSBinary()
		if err != nil {
			return tutorialBuildSummary{}, err
		}
		absTape, err := filepath.Abs(paths.Tape)
		if err != nil {
			return tutorialBuildSummary{}, err
		}
		mp4Path := filepath.Join(opts.outDir, "o-csv.mp4")
		absMP4, err := filepath.Abs(mp4Path)
		if err != nil {
			return tutorialBuildSummary{}, err
		}
		cmd := exec.Command(vhsBin, absTape, "-o", absMP4)
		cmd.Dir = cwd
		if spec.WorkDir != "" {
			if filepath.IsAbs(spec.WorkDir) {
				cmd.Dir = spec.WorkDir
			} else {
				cmd.Dir = filepath.Join(cwd, spec.WorkDir)
			}
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return tutorialBuildSummary{}, fmt.Errorf("run vhs %s: %w\n%s", absTape, err, strings.TrimSpace(string(out)))
		}
		info, err := os.Stat(absMP4)
		if err != nil {
			return tutorialBuildSummary{}, fmt.Errorf("verify generated video %s: %w", absMP4, err)
		}
		if info.Size() == 0 {
			return tutorialBuildSummary{}, fmt.Errorf("generated video %s is empty", absMP4)
		}
		summary.MP4 = mp4Path

		// Re-write index.html with the generated MP4 embedded.
		bundleOpts.HTML.VideoPath = "o-csv.mp4"
		if err := os.WriteFile(paths.HTML, []byte(tutorial.RenderHTML(res, bundleOpts.HTML)), 0o644); err != nil {
			return tutorialBuildSummary{}, fmt.Errorf("rewrite %s with video: %w", paths.HTML, err)
		}
	}

	return summary, nil
}

func findVHSBinary() (string, error) {
	if p, err := exec.LookPath("vhs"); err == nil {
		return p, nil
	}
	const brewVHS = "/opt/homebrew/bin/vhs"
	if info, err := os.Stat(brewVHS); err == nil && !info.IsDir() {
		return brewVHS, nil
	}
	return "", errors.New("vhs binary not found on PATH or at /opt/homebrew/bin/vhs")
}

func runTutorialBuild(args []string, stdout, stderr io.Writer) int {
	const tutUsage = "usage: o-csv tutorial build [--spec PATH] [--out DIR] [--vhs]"
	fs := flag.NewFlagSet("o-csv tutorial build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, tutUsage) }
	specPath := fs.String("spec", defaultTutorialSpec, "path to the tutorial spec JSON file")
	outDir := fs.String("out", defaultTutorialOut, "output directory for HTML, VHS tape, SRT, JSON, and MP4")
	runVHS := fs.Bool("vhs", false, "render MP4 video via Charm VHS after generating the bundle")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "o-csv tutorial build: unexpected argument %q\n%s\n", fs.Arg(0), tutUsage)
		return 2
	}
	summary, err := buildTutorial(tutorialBuildOptions{
		specPath: *specPath,
		outDir:   *outDir,
		runVHS:   *runVHS,
	})
	if err != nil {
		fmt.Fprintln(stderr, "o-csv tutorial build:", err)
		return 1
	}
	steps := 0
	captured := 0
	for _, sec := range summary.Result.Sections {
		steps += len(sec.Steps)
		for _, st := range sec.Steps {
			if st.Captured {
				captured++
			}
		}
	}
	fmt.Fprintf(stdout, "built tutorial bundle in %s (%d sections, %d steps, %d captured frames)\n",
		summary.Paths.Dir, len(summary.Result.Sections), steps, captured)
	fmt.Fprintf(stdout, "  html: %s\n  tape: %s\n  srt:  %s\n  data: %s\n",
		summary.Paths.HTML, summary.Paths.Tape, summary.Paths.SRT, summary.Paths.DataJSON)
	if summary.MP4 != "" {
		fmt.Fprintf(stdout, "  mp4:  %s\n", summary.MP4)
	}
	return 0
}
