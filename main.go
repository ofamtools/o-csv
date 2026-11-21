// Command o-csv is a viewing-first terminal grid for CSV, TSV and PSV
// files, built on Ochazuke v2, plus show/columns/stats subcommands.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run dispatches a subcommand only when the first argument is exactly
// show, columns or stats; "./show" or "-- show" opens a file named show.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if _, ok := subUsage[args[0]]; ok {
			return subcommand(args[0], args[1:], stdout, stderr)
		}
	}
	return interactive(args, stdout, stderr)
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
