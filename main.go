// Command o-csv is a viewing-first terminal grid for CSV, TSV and PSV
// files, built on Ochazuke v2, plus show/columns/stats subcommands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/oden"
)

// Version is o-csv's version.
const Version = "0.2.0"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run dispatches through the uniform Ochazuke v2 / Oden v2 CLI shell while
// opening positional FILE (or "./show", "-- show", "-") directly in the TUI.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "--" {
		return interactive(args, stdout, stderr)
	}
	app := newOdenApp(args, os.Stdin, stdout, stderr)
	if err := app.Run(context.Background()); err != nil {
		var ee *oden.ExitError
		if errors.As(err, &ee) {
			if ee.Err != nil {
				fmt.Fprintln(stderr, "o-csv:", ee.Err)
			}
			return ee.Code
		}
		fmt.Fprintln(stderr, "o-csv:", err)
		return 1
	}
	return 0
}

func newOdenApp(args []string, stdin io.Reader, stdout, stderr io.Writer) *oden.App {
	runSub := func(name string) func(*oden.Ctx) error {
		return func(c *oden.Ctx) error {
			if code := subcommand(name, c.Args, stdout, stderr); code != 0 {
				return &oden.ExitError{Code: code}
			}
			return nil
		}
	}

	info := cli.AppInfo{
		Name:     "o-csv",
		Version:  Version,
		Summary:  "Viewing-first terminal grid and CLI for CSV, TSV, and PSV files",
		HelpText: usage,
		Modes:    func() []core.Mode { return modes },
		Bindings: func() []core.Binding { return defaultBindings() },
		Specs:    func() []core.CommandSpec { return commandSpecs() },
		RunTUI: func(_ context.Context, rootArgs []string) error {
			if code := interactive(rootArgs, stdout, stderr); code != 0 {
				return &oden.ExitError{Code: code}
			}
			return nil
		},
		Manifest: csvExtManifest(),
		Capabilities: &oden.Capabilities{
			Name:         "o-csv",
			Version:      Version,
			Summary:      "Viewing-first terminal grid and CLI for CSV, TSV, and PSV files",
			Capabilities: []string{"query", "catalog", "tui"},
		},
		Query: csvExtQuery,
	}

	app := cli.NewApp(info, args, oden.WithStdio(stdin, stdout, stderr))

	app.Add(
		oden.Command{
			Name:    "show",
			Summary: "filter, sort, and print rows from a CSV/TSV/PSV file",
			Usage:   "FILE [--where COL~TEXT]... [--search TEXT] [--sort COL] [--desc] [--limit N] [--format json|table|markdown] [--delim D] [--no-header]",
			Handle:  runSub("show"),
		},
		oden.Command{
			Name:    "columns",
			Summary: "list columns and inferred types of a CSV/TSV/PSV file",
			Usage:   "FILE [--format json|table|markdown] [--delim D] [--no-header]",
			Handle:  runSub("columns"),
		},
		oden.Command{
			Name:    "stats",
			Summary: "summarize row count, types, empty/distinct counts, and min/max per column",
			Usage:   "FILE [--format json|table|markdown] [--delim D] [--no-header]",
			Handle:  runSub("stats"),
		},
		oden.Command{
			Name:    "smoke",
			Aliases: []string{"--smoke"},
			Summary: "run a headless 120x32 smoke render check",
			Usage:   "[FILE] [--delim D] [--no-header]",
			Handle: func(c *oden.Ctx) error {
				sargs := append([]string{"--smoke"}, c.Args...)
				if code := interactive(sargs, stdout, stderr); code != 0 {
					return &oden.ExitError{Code: code}
				}
				return nil
			},
		},
	)

	// Any non-command args (empty, flags, positional FILE, "./show", "-- show", "-")
	// open directly in the interactive TUI.
	app.Default = func(ctx context.Context, rootArgs []string) error {
		return info.RunTUI(ctx, rootArgs)
	}

	return app
}

func csvExtManifest() *oden.ExtManifest {
	return &oden.ExtManifest{
		Name:    "o-csv",
		Version: Version,
		Schemas: []oden.ExtSchema{
			{
				Kind:           "csv.row",
				Title:          "Row",
				Plural:         "Rows",
				DefaultColumns: []string{"row", "file"},
				SummaryField:   "row",
				Fields: []oden.ExtField{
					{Name: "row", Type: "int", Description: "1-based row number"},
					{Name: "file", Type: "path", Description: "Source CSV/TSV/PSV file"},
				},
			},
		},
		Commands: []oden.ExtCommand{
			{ID: "csv.show", Description: "Filter, sort, and print rows from a CSV/TSV/PSV file", Tags: []string{"read"}},
			{ID: "csv.columns", Description: "List columns and inferred types of a CSV/TSV/PSV file", Tags: []string{"read"}},
			{ID: "csv.stats", Description: "Summarize statistics for each column of a CSV/TSV/PSV file", Tags: []string{"read"}},
		},
	}
}

func csvExtQuery(_ context.Context, kind string, args []string) (oden.ExtQueryResult, error) {
	if kind != "" && kind != "csv.row" && kind != "row" {
		return oden.ExtQueryResult{}, fmt.Errorf("unknown kind %q (expected csv.row)", kind)
	}
	if len(args) == 0 {
		return oden.ExtQueryResult{Items: []oden.ExtRecord{}, Total: 0}, nil
	}
	file := args[0]
	needle := strings.ToLower(strings.TrimSpace(strings.Join(args[1:], " ")))
	t, err := ReadFile(file, Options{})
	if err != nil {
		return oden.ExtQueryResult{}, err
	}
	keys := jsonKeys(t.Header)
	items := make([]oden.ExtRecord, 0, len(t.Rows))
	for i, row := range t.Rows {
		if needle != "" {
			matched := false
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), needle) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		fields := map[string]any{
			"row":  i + 1,
			"file": file,
		}
		for j, k := range keys {
			if j < len(row) {
				fields[k] = row[j]
			}
		}
		items = append(items, oden.ExtRecord{
			Kind:          "csv.row",
			ID:            strconv.Itoa(i + 1),
			Fields:        fields,
			ArchiveStatus: "active",
			StarStatus:    "unstarred",
		})
	}
	return oden.ExtQueryResult{Items: items, Total: len(items)}, nil
}

const usage = `usage: o-csv [flags] [FILE|-]
       o-csv show FILE [--where COL~TEXT] [--search TEXT] [--sort COL] [--desc] [--limit N] [--format json|table|markdown]
       o-csv columns FILE [--format json|table|markdown]
       o-csv stats FILE [--format json|table|markdown]

Opens FILE (or stdin when piped or given as -) in a read-only grid; --edit or
ctrl+e enables editing. Press ? for the live key help, : for commands.

flags:
`

func interactive(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("o-csv", flag.ContinueOnError)
	fl.SetOutput(stderr)
	edit := fl.Bool("edit", false, "start in edit mode")
	delim := fl.String("delim", "", "delimiter: comma, tab, semicolon, pipe or one character (default: detect)")
	noHeader := fl.Bool("no-header", false, "the first row is data; columns are named A, B, C, ...")
	smoke := fl.Bool("smoke", false, "run a scripted 120x32 session without a TTY, print the frame and checks")
	fl.Usage = func() {
		fmt.Fprint(stderr, usage)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fl.NArg() > 1 {
		fmt.Fprintf(stderr, "o-csv: one FILE at a time (got %d)\n", fl.NArg())
		return 2
	}
	d, err := ParseDelim(*delim)
	if err != nil {
		fmt.Fprintln(stderr, "o-csv:", err)
		return 2
	}
	opts := Options{Delim: d, NoHeader: *noHeader}
	file := fl.Arg(0)
	if *smoke {
		return runSmoke(stdout, file, opts)
	}
	pick := false // no FILE on a terminal: start empty with the file picker open
	if file == "" {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			file = "-"
		} else {
			pick = true
		}
	}
	doc := emptyDoc(*noHeader)
	if !pick {
		t, err := ReadFile(file, opts)
		if err != nil {
			fmt.Fprintln(stderr, "o-csv:", err)
			return 1
		}
		doc = newDoc(file, t, *noHeader)
	}
	cfg, set := loadConfig(configDir())
	cfg.NoHeader = *noHeader
	cfg.Cwd, _ = os.Getwd()
	if set.Bool("clipboard", true) {
		cfg.Copy = systemClipboard()
	}
	env := newEnv(cfg)
	sh := newShell(env, doc, *edit || set.Bool("edit", false))
	if pick {
		sh.Update(core.CommandEvent{Command: core.Command{ID: "file.open", Source: "startup", Actor: core.Actor{Kind: "user"}}})
	}
	// With piped stdin, Bubble Tea reads keys from the controlling TTY.
	if _, err := tea.NewProgram(sh).Run(); err != nil {
		fmt.Fprintln(stderr, "o-csv:", err)
		return 1
	}
	return 0
}

// configDir is $XDG_CONFIG_HOME/o-csv, else ~/.config/o-csv.
func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "o-csv")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "o-csv")
	}
	return ""
}

// loadConfig reads the optional bindings.json, themes.json and
// settings.json (JSONC) from dir. Missing files are fine; broken ones are
// reported as notes and skipped. Nothing is ever written.
func loadConfig(dir string) (Config, *config.Settings) {
	var cfg Config
	read := func(name string) []byte {
		if dir == "" {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			cfg.Notes = append(cfg.Notes, name+": "+err.Error())
		}
		return data
	}
	if data := read("bindings.json"); data != nil {
		if bs, err := config.LoadBindings(data); err != nil {
			cfg.Notes = append(cfg.Notes, err.Error())
		} else {
			cfg.Bindings = bs
		}
	}
	if data := read("themes.json"); data != nil {
		if th, err := config.LoadTheme(data, ui.DefaultDarkTheme()); err != nil {
			cfg.Notes = append(cfg.Notes, err.Error())
		} else {
			cfg.Theme = &th
		}
	}
	set, err := config.LoadSettings(read("settings.json"), defaultSettings)
	if err != nil {
		cfg.Notes = append(cfg.Notes, err.Error())
		set, _ = config.LoadSettings(nil, defaultSettings)
	}
	cfg.Settings = set
	return cfg, set
}
