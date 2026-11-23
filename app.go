package main

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// Frame and node IDs.
const (
	pageID       = "main"
	nodeGrid     = "grid"
	nodeFilter   = "filter"
	modalPalette = "palette"
	modalFiles   = "files"
	modalHelp    = "help"
	modalForm    = "form"
	detailText   = "detail.text"
	detailTail   = "detail.tail" // blank last line of the detail pane; visible = scrolled to the end
)

// App is the page state, kept by value in the page frame's Data. Handlers
// copy it, change the copy and store it back; maps are cloned before they
// are written and Doc is copy-on-write.
type App struct {
	Doc  Doc
	Edit bool

	base []table.Row // one per Doc row, file order
	Grid table.Model // display model: projected rows, grouping, cursor

	Query     string
	QueryLine input.Line
	Field     int // filtered column; -1 = all columns (hidden ones included)
	Match     table.MatchMode
	FilterErr string
	Typing    bool // the filter input line is open

	SortCol int
	SortDir table.SortDir

	Hidden map[int]bool
	Col    int // active column (data index)
	ColOff int // first displayed visible column

	Detail    bool
	Ratio     float64
	DetailOff int    // detail pane scroll offset, in wrapped lines
	detailRow string // the row DetailOff belongs to

	Status, Tone string
	Register     string
	Confirm      string // a pending "press again" confirmation
	prevConfirm  string
}

func colKey(j int) string { return "c" + strconv.Itoa(j) }

// newApp opens doc with the session defaults in env.
func newApp(env *Env, doc Doc, edit bool) App {
	a := App{Doc: doc, Edit: edit, Field: -1, Match: env.Match, SortCol: -1, Ratio: env.Ratio}
	a.Grid = *table.New(nil, nil)
	a.rebuild()
	a.reproject()
	return a
}

func (a App) clone() App {
	a.Hidden = maps.Clone(a.Hidden)
	a.Grid = a.Grid.Copy()
	a.QueryLine.Text = slices.Clone(a.QueryLine.Text)
	return a
}

func appOf(s core.State) App {
	a, _ := s.Pages[pageID].Data.(App)
	return a
}

func withApp(s core.State, a App) core.State {
	f := s.Pages[pageID]
	f.Data = a
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func (a *App) note(msg string) { a.Status, a.Tone = msg, "success" }
func (a *App) info(msg string) { a.Status, a.Tone = msg, "info" }
func (a *App) warn(msg string) { a.Status, a.Tone = msg, "warning" }
func (a *App) fail(msg string) { a.Status, a.Tone = msg, "error" }

// headerName is column j's name, or its letter when the header is empty.
func (a App) headerName(j int) string {
	if j >= 0 && j < len(a.Doc.Header) && a.Doc.Header[j] != "" {
		return a.Doc.Header[j]
	}
	return ColumnLetter(j)
}

// rebuild recreates the base rows from the document.
func (a *App) rebuild() {
	a.base = make([]table.Row, len(a.Doc.Rows))
	for i, row := range a.Doc.Rows {
		cells := make(map[string]any, len(row))
		for j, v := range row {
			cells[colKey(j)] = v
		}
		id := strconv.Itoa(a.Doc.IDs[i])
		a.base[i] = table.Row{ID: id, Ref: &core.Ref{Kind: "row", ID: id}, Cells: cells}
	}
	cols := make([]table.Column, len(a.Doc.Header))
	for j := range cols {
		cols[j] = table.Column{Key: colKey(j), Title: a.headerName(j)}
	}
	a.Grid.Columns = cols
	a.Col = min(max(a.Col, 0), len(cols)-1)
}

// reproject filters (the widget's matcher over every column, hidden ones
// included), sorts with compareCells (stable, so ties keep file order) and
// keeps the cursor on the same source row when it is still shown.
func (a *App) reproject() {
	keep, onRow := a.focusedID()
	tmp := table.Model{Columns: a.Grid.Columns, Rows: a.base, Query: a.Query, MatchMode: a.Match}
	if a.Field >= 0 {
		tmp.FilterField = colKey(a.Field)
	}
	rows := tmp.Filtered()
	a.FilterErr = tmp.FilterErr
	if a.FilterErr != "" {
		rows = nil // an invalid regex shows nothing (with the error), never a silent substring match
	}
	if a.SortCol >= 0 && a.SortDir != table.SortNone {
		key := colKey(a.SortCol)
		desc := a.SortDir == table.SortDesc
		slices.SortStableFunc(rows, func(x, y table.Row) int {
			c := compareCells(table.Text(x.Cells[key]), table.Text(y.Cells[key]))
			if desc {
				return -c
			}
			return c
		})
	}
	a.Grid.Rows = rows
	if onRow {
		a.focusRow(keep)
	}
	a.Grid.Focused() // clamp
}

// focusedID returns the source ID of the focused row.
func (a *App) focusedID() (string, bool) {
	r, ok := a.Grid.FocusedRow()
	return r.ID, ok
}

// focusedRow returns the focused row's source ID and values.
func (a *App) focusedRow() (int, []string, bool) {
	id, ok := a.focusedID()
	if !ok {
		return 0, nil, false
	}
	n, _ := strconv.Atoi(id)
	row, ok := a.Doc.Row(n)
	return n, row, ok
}

// focusRow moves the cursor to source row id; false when it is not shown.
func (a *App) focusRow(id string) bool {
	for i, it := range a.Grid.Items() {
		if !it.Header && it.Row.ID == id {
			a.Grid.SelectedIdx = i
			a.Grid.Focused()
			return true
		}
	}
	return false
}

// visible lists the shown columns' data indexes.
func (a *App) visible() []int {
	var out []int
	for j := range a.Doc.Header {
		if !a.Hidden[j] {
			out = append(out, j)
		}
	}
	return out
}

// docChanged refreshes everything derived from the document.
func (a *App) docChanged(focus int) {
	a.rebuild()
	a.reproject()
	if focus >= 0 && !a.focusRow(strconv.Itoa(focus)) && a.Query != "" {
		a.warn(a.Status + " (row hidden by the filter)")
	}
}

// writable guards every document change: false (with a warning) in view mode.
func (a *App) writable(what string) bool {
	if a.Edit {
		return true
	}
	a.warn("read-only: " + what + " needs edit mode — press ctrl+e")
	return false
}

// --- environment ------------------------------------------------------------

// Env is the immutable session configuration shared by handlers and the view.
type Env struct {
	Km       *core.Keymap
	Bindings []core.Binding
	Specs    []core.CommandSpec
	Theme    ui.Theme
	Copy     func(string) error // nil: yanks stay in the register only
	FS       pathpicker.FS
	Cwd      string
	NoHeader bool
	Match    table.MatchMode
	Ratio    float64
	StackW   int      // below this width the detail pane stacks under the grid
	Notes    []string // configuration problems, shown at startup
}

// Config is what main (or a test) supplies to build an Env.
type Config struct {
	Bindings []core.Binding     // user overrides, merged ahead of the defaults
	Theme    *ui.Theme          // nil: the default dark theme
	Settings *config.Settings   // nil: defaults
	Copy     func(string) error // nil: no system clipboard
	FS       pathpicker.FS      // nil: the real file system
	Cwd      string
	NoHeader bool
	Notes    []string // problems found while loading the files
}

// defaultSettings are the recognized settings.json keys and their defaults.
var defaultSettings = map[string]any{
	"edit":        false,       // start in edit mode
	"matchMode":   "substring", // filter and palette match mode: substring, fuzzy or regex
	"detailRatio": 0.6,         // grid share of the detail split, 0.2 to 0.8
	"stackWidth":  100,         // below this terminal width the detail pane stacks under the grid
	"clipboard":   true,        // copy yanks to the system clipboard (pbcopy, wl-copy, xclip, xsel)
}

var modes = []core.Mode{{Name: "normal"}, {Name: "input"}, {Name: "palette"}, {Name: "files"}, {Name: "help"}, {Name: "form"}}

// newEnv compiles the keymap: user bindings go ahead of the defaults
// (config.MergeBindings), problems are reported in Notes and the valid
// bindings still apply.
func newEnv(cfg Config) *Env {
	env := &Env{Theme: ui.DefaultDarkTheme(), Copy: cfg.Copy, FS: cfg.FS, Cwd: cfg.Cwd, NoHeader: cfg.NoHeader, Notes: cfg.Notes}
	if cfg.Theme != nil {
		env.Theme = *cfg.Theme
	}
	if env.FS == nil {
		env.FS = csvFS{pathpicker.OSFS{}}
	}
	set := cfg.Settings
	if set == nil {
		set, _ = config.LoadSettings(nil, defaultSettings)
	}
	switch m := table.MatchMode(set.String("matchMode", "substring")); m {
	case table.MatchSubstring, table.MatchFuzzy, table.MatchRegex:
		env.Match = m
	default:
		env.Match = table.MatchSubstring
		env.Notes = append(env.Notes, "settings: matchMode must be substring, fuzzy or regex")
	}
	env.Ratio = min(max(set.Float("detailRatio", 0.6), 0.2), 0.8)
	env.StackW = set.Int("stackWidth", 100)
	env.Specs = commandSpecs()
	for _, d := range config.CheckBindings(modes, cfg.Bindings, env.Specs...) {
		if d.Severity == config.SevError {
			env.Notes = append(env.Notes, fmt.Sprintf("bindings.json #%d: %s", d.Index, d.Message))
		}
	}
	env.Bindings = config.MergeBindings(defaultBindings(), cfg.Bindings)
	km, err := core.NewKeymap(modes, env.Bindings, env.Specs...)
	if err != nil && len(env.Notes) == 0 {
		env.Notes = append(env.Notes, "bindings: "+strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	env.Km = km
	return env
}

// csvFS shows directories and CSV, TSV and PSV files only.
type csvFS struct{ pathpicker.FS }

func (f csvFS) ReadDir(dir string) ([]pathpicker.DirEntry, error) {
	es, err := f.FS.ReadDir(dir)
	var out []pathpicker.DirEntry
	for _, e := range es {
		if e.IsDir || isDataFile(e.Name) {
			out = append(out, e)
		}
	}
	return out, err
}

func isDataFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv", ".tsv", ".psv", ".tab":
		return true
	}
	return false
}

// extDelim is the delimiter a file's extension implies (0 = detect).
func extDelim(name string) rune {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".tsv", ".tab":
		return '\t'
	case ".psv":
		return '|'
	}
	return 0
}

// --- commands and bindings ------------------------------------------------------

// commandSpecs are the app's commands. Those tagged "user" are listed in
// the palette; the others are internal plumbing for inputs and modals.
func commandSpecs() []core.CommandSpec {
	user := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: []string{"user"}}
	}
	edit := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: []string{"user", "editing"}}
	}
	internal := func(id, desc string) core.CommandSpec { return core.CommandSpec{ID: id, Description: desc} }
	specs := []core.CommandSpec{
		user("filter.all", "Filter rows across all columns"),
		user("filter.column", "Filter rows by the active column"),
		user("filter.mode.cycle", "Cycle the filter match mode: substring, fuzzy, regex"),
		user("filter.clear", "Clear the filter"),
		user("sort.cycle", "Sort by the active column: ascending, descending, file order"),
		user("group.toggle", "Group rows by the active column (again: ungroup)"),
		user("group.collapse", "Collapse or expand the focused group"),
		user("column.hide", "Hide the active column"),
		user("column.show.all", "Show all hidden columns"),
		user("detail.toggle", "Show or hide the row detail pane"),
		user("split.grow", "Widen the grid next to the detail pane"),
		user("split.shrink", "Narrow the grid next to the detail pane"),
		user("split.reset", "Reset the detail split"),
		user("detail.scroll.down", "Scroll the row detail pane down"),
		user("detail.scroll.up", "Scroll the row detail pane up"),
		user("yank.cell", "Copy the active cell"),
		user("yank.row", "Copy the focused row as CSV (hidden columns included)"),
		user("file.open", "Browse for a CSV, TSV or PSV file to open"),
		user("palette.open", "Open the command palette"),
		user("help.open", "Show the active key bindings"),
		user("edit.toggle", "Toggle edit mode (view mode is read-only)"),
		edit("edit.row", "Edit the focused row in a form"),
		edit("edit.insert", "Insert a blank row below the focused row"),
		edit("edit.delete", "Delete the focused row"),
		edit("edit.upper", "Upper-case the active cell"),
		edit("edit.lower", "Lower-case the active cell"),
		edit("edit.title", "Title-case the active cell"),
		edit("history.undo", "Undo the last change"),
		edit("history.redo", "Redo the last undone change"),
		edit("file.save", "Save the file (atomic; keeps delimiter and header)"),
		user("app.close", "Quit (asks again when edits are unsaved)"),

		internal("input.insert", "Type into the filter"),
		internal("input.backspace", "Delete the last filter character"),
		internal("input.accept", "Keep the filter and return to the grid"),
		internal("input.cancel", "Clear the filter and return to the grid"),
		internal("palette.insert", "Type into the palette query"),
		internal("palette.backspace", "Delete the last query character"),
		internal("palette.close", "Close the palette"),
		internal(picker.CmdConfirm, "Run the highlighted command"),
		internal(picker.CmdModeCycle, "Cycle the match mode"),
		internal("files.insert", "Type into the file filter"),
		internal("files.backspace", "Delete a filter character, or go to the parent directory"),
		internal("files.close", "Close the file picker"),
		internal(pathpicker.CmdOpen, "Open the highlighted directory or file"),
		internal(pathpicker.CmdParent, "Go to the parent directory"),
		internal(pathpicker.CmdHiddenToggle, "Show or hide dotfiles"),
		internal(pathpicker.CmdPreviewToggle, "Show or hide the preview"),
		internal(pathpicker.CmdModeCycle, "Cycle the match mode"),
		internal("help.close", "Close help"),
		internal(form.CmdInsert, "Type into the field"),
		internal(form.CmdBackspace, "Delete the last character"),
		internal("form.clear", "Clear the field"),
		internal(form.CmdSubmit, "Apply the row"),
		internal(form.CmdCancel, "Discard the form"),
		internal("clipboard.done", "Clipboard copy finished"),
	}
	return append(specs, input.ReadlineSpecs()...)
}

func b(mode, keys, cmd, hint string) core.Binding {
	return core.Binding{Mode: mode, Keys: strings.Fields(keys), Command: cmd, Hint: hint}
}

func class(mode, cls, cmd string, args map[string]any, hint string) core.Binding {
	return core.Binding{Mode: mode, Class: cls, Command: cmd, Args: args, Hint: hint}
}

// unusedMotions are standard motions a grid has no use for.
var unusedMotions = []string{"motion.word.next", "motion.word.prev", "motion.word.end", "motion.group.next", "motion.group.prev"}

func standard(mode string) []core.Binding {
	var out []core.Binding
	for _, bd := range core.StandardBindings(mode) {
		if !slices.Contains(unusedMotions, bd.Command) {
			out = append(out, bd)
		}
	}
	return out
}

// defaultBindings is the built-in keymap. Help is generated from whatever
// of it (plus user overrides) actually resolves.
func defaultBindings() []core.Binding {
	text := map[string]any{"text": "$text"}
	out := []core.Binding{
		b("normal", "/", "filter.all", "filter all columns"),
		b("normal", "f", "filter.column", "filter active column"),
		b("normal", "ctrl+m", "filter.mode.cycle", "match mode"),
		b("normal", "alt+m", "filter.mode.cycle", "match mode"),
		b("normal", "esc", "filter.clear", "clear filter"),
		b("normal", "s", "sort.cycle", "sort column"),
		b("normal", "g b", "group.toggle", "group by column"),
		b("normal", "z a", "group.collapse", "fold group"),
		b("normal", "-", "column.hide", "hide column"),
		b("normal", "g =", "column.show.all", "show all columns"),
		b("normal", "enter", "detail.toggle", "row detail"),
		b("normal", "ctrl+w >", "split.grow", "wider grid"),
		b("normal", "ctrl+w <", "split.shrink", "narrower grid"),
		b("normal", "ctrl+w =", "split.reset", "reset split"),
		b("normal", "ctrl+w j", "detail.scroll.down", "scroll detail down"),
		b("normal", "ctrl+w k", "detail.scroll.up", "scroll detail up"),
		b("normal", "y", "yank.cell", "copy cell"),
		b("normal", "Y", "yank.row", "copy row"),
		b("normal", "ctrl+o", "file.open", "open file"),
		b("normal", ":", "palette.open", "commands"),
		b("normal", "?", "help.open", "help"),
		b("normal", "ctrl+e", "edit.toggle", "edit mode"),
		b("normal", "i", "edit.row", "edit row"),
		b("normal", "e", "edit.row", "edit row"),
		b("normal", "o", "edit.insert", "new row below"),
		b("normal", "d d", "edit.delete", "delete row"),
		b("normal", "t u", "edit.upper", "UPPER cell"),
		b("normal", "t l", "edit.lower", "lower cell"),
		b("normal", "t t", "edit.title", "Title cell"),
		b("normal", "u", "history.undo", "undo"),
		b("normal", "ctrl+r", "history.redo", "redo"),
		b("normal", "ctrl+s", "file.save", "save"),
		b("normal", "q", "app.close", "quit"),
		b("normal", "ctrl+c", "app.close", "quit"),
	}
	out = append(out, standard("normal")...)
	out = append(out,
		class("input", core.ClassPrintable, "input.insert", text, "type"),
		class("input", core.ClassPaste, "input.insert", text, "paste"),
		b("input", "backspace", "input.backspace", "delete"),
		b("input", "enter", "input.accept", "keep filter"),
		b("input", "esc", "input.cancel", "clear filter"),
		b("input", "ctrl+c", "input.cancel", "clear filter"),
		b("input", "ctrl+m", "filter.mode.cycle", "match mode"),
		b("input", "alt+m", "filter.mode.cycle", "match mode"),
		b("input", "tab", "filter.mode.cycle", "match mode"),
		b("input", "down", "motion.down", "next row"),
		b("input", "ctrl+n", "motion.down", "next row"),
		b("input", "up", "motion.up", "previous row"),
		b("input", "ctrl+p", "motion.up", "previous row"),
		b("input", "pgdown", "motion.page.down", "page down"),
		b("input", "pgup", "motion.page.up", "page up"),

		class("palette", core.ClassPrintable, "palette.insert", text, "type"),
		class("palette", core.ClassPaste, "palette.insert", text, "paste"),
		b("palette", "backspace", "palette.backspace", "delete"),
		b("palette", "enter", picker.CmdConfirm, "run"),
		b("palette", "esc", "palette.close", "close"),
		b("palette", "ctrl+c", "palette.close", "close"),
		b("palette", "ctrl+m", picker.CmdModeCycle, "match mode"),
		b("palette", "alt+m", picker.CmdModeCycle, "match mode"),
		b("palette", "tab", picker.CmdModeCycle, "match mode"),
		b("palette", "down", "motion.down", "next"),
		b("palette", "ctrl+n", "motion.down", "next"),
		b("palette", "up", "motion.up", "previous"),
		b("palette", "ctrl+p", "motion.up", "previous"),
		b("palette", "pgdown", "motion.page.down", "page down"),
		b("palette", "pgup", "motion.page.up", "page up"),

		class("files", core.ClassPrintable, "files.insert", text, "filter"),
		class("files", core.ClassPaste, "files.insert", text, "paste"),
		b("files", "backspace", "files.backspace", "delete / parent dir"),
		b("files", "enter", pathpicker.CmdOpen, "open"),
		b("files", "right", pathpicker.CmdOpen, "open"),
		b("files", "left", pathpicker.CmdParent, "parent dir"),
		b("files", "esc", "files.close", "close"),
		b("files", "ctrl+c", "files.close", "close"),
		b("files", "tab", pathpicker.CmdPreviewToggle, "preview"),
		b("files", "ctrl+t", pathpicker.CmdHiddenToggle, "dotfiles"),
		b("files", "ctrl+m", pathpicker.CmdModeCycle, "match mode"),
		b("files", "alt+m", pathpicker.CmdModeCycle, "match mode"),
		b("files", "down", "motion.down", "next"),
		b("files", "ctrl+n", "motion.down", "next"),
		b("files", "up", "motion.up", "previous"),
		b("files", "ctrl+p", "motion.up", "previous"),
		b("files", "pgdown", "motion.page.down", "page down"),
		b("files", "pgup", "motion.page.up", "page up"),

		class("form", core.ClassPrintable, form.CmdInsert, text, "type"),
		class("form", core.ClassPaste, form.CmdInsert, text, "paste"),
		b("form", "backspace", form.CmdBackspace, "delete"),
		b("form", "ctrl+u", "form.clear", "clear field"),
		b("form", "tab", "motion.down", "next field"),
		b("form", "down", "motion.down", "next field"),
		b("form", "shift+tab", "motion.up", "previous field"),
		b("form", "up", "motion.up", "previous field"),
		b("form", "enter", form.CmdSubmit, "apply"),
		b("form", "esc", form.CmdCancel, "cancel"),
		b("form", "ctrl+c", form.CmdCancel, "cancel"),

		b("help", "esc", "help.close", "close"),
		b("help", "q", "help.close", "close"),
		b("help", "?", "help.close", "close"),
		b("help", "ctrl+c", "help.close", "close"),
	)
	for _, mode := range []string{"input", "palette", "files", "form"} {
		taken := map[string]bool{}
		for _, bd := range out {
			if bd.Mode == mode && len(bd.Keys) > 0 {
				taken[strings.Join(bd.Keys, " ")] = true
			}
		}
		for _, rb := range input.ReadlineBindings(mode, "") {
			k := strings.Join(rb.Keys, " ")
			if taken[k] {
				continue
			}
			taken[k] = true
			out = append(out, rb)
		}
	}
	return append(out, standard("help")...)
}

// --- engine -----------------------------------------------------------------------

// fn is an app handler: it edits a (already a private copy) and may return
// a changed state (modals, page mode) plus effects.
type fn func(s core.State, a *App, c core.Command) (core.State, []core.Effect)

// wrap turns an app handler into a core.Handler: it copies the App, clears
// the last status and any pending confirmation, sizes the grid page, runs
// fn and stores the App back.
func (env *Env) wrap(f fn) core.Handler {
	return func(s core.State, c core.Command) core.StepResult {
		a := appOf(s).clone()
		a.prevConfirm, a.Confirm = a.Confirm, ""
		a.Status, a.Tone = "", ""
		a.Grid.PageSize = env.geometry(&a, s.Width, s.Height).rows
		s, effects := f(s, &a, c)
		a.prevConfirm = ""
		if !a.Detail {
			a.DetailOff = 0
		} else if id, _ := a.focusedID(); id != a.detailRow {
			a.detailRow, a.DetailOff = id, 0 // a new focused row starts at the top
		}
		return core.Next(withApp(s, a), effects...)
	}
}

func setPage(s core.State, mode, focus string) core.State {
	f := s.Pages[pageID]
	f.Mode, f.FocusID = mode, focus
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func openModal(s core.State, id, mode string, data any) core.State {
	s = closeModal(s, id)
	s.Modals = append(slices.Clone(s.Modals), core.Frame{ID: id, Kind: core.FrameModal, Mode: mode, FocusID: id, Data: data})
	return s
}

func closeModal(s core.State, id string) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Delete(slices.Clone(s.Modals), i, i+1)
	}
	return s
}

func modalData(s core.State, id string) any {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		return s.Modals[i].Data
	}
	return nil
}

func setModalData(s core.State, id string, data any) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Clone(s.Modals)
		s.Modals[i].Data = data
	}
	return s
}

// dispatch asks the shell to run cmd through the engine after this step
// (palette picks), so it meets the same handlers and guards as a key.
func dispatch(c core.Command) core.Effect { return core.CustomEffect{Kind: "dispatch", Data: c} }

// newEngine registers every handler on a fresh engine over env's keymap.
func newEngine(env *Env) *core.Engine {
	e := core.NewEngine(env.Km)
	h := func(id string, f fn) { e.Handle(id, env.wrap(f)) }
	scoped := func(scope, id string, f fn) { e.HandleScope(scope, id, env.wrap(f)) }

	for _, id := range []string{"motion.down", "motion.up", "motion.top", "motion.bottom", "motion.page.down", "motion.page.up", "motion.half.down", "motion.half.up", "motion.block.next", "motion.block.prev"} {
		scoped("grid", id, env.rowMotion)
		scoped("palette", id, env.paletteCmd)
		scoped("files", id, env.filesCmd)
		scoped("help", id, env.helpScroll)
		scoped("form", id, env.formCmd)
	}
	for _, id := range []string{"motion.left", "motion.right", "motion.start", "motion.end"} {
		scoped("grid", id, env.colMotion)
	}

	h("filter.all", env.filterOpen)
	h("filter.column", env.filterOpen)
	h("filter.mode.cycle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		m := table.Model{MatchMode: a.Match}
		m.CycleMatchMode()
		a.Match = m.MatchMode
		a.reproject()
		a.info("match mode: " + string(a.Match))
		return s, nil
	})
	h("filter.clear", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Query == "" {
			return s, nil
		}
		a.Query, a.Field = "", -1
		a.reproject()
		a.info("filter cleared")
		return s, nil
	})
	h("input.insert", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.QueryLine.Sync(a.Query)
		if a.QueryLine.Insert(c.Arg("text")) {
			a.Query = a.QueryLine.Value()
			a.reproject()
		}
		return s, nil
	})
	h("input.backspace", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.QueryLine.Sync(a.Query)
		if changed, _ := a.QueryLine.HandleCommand(core.Command{ID: input.CmdBackspace}); changed {
			a.Query = a.QueryLine.Value()
			a.reproject()
		}
		return s, nil
	})
	for _, id := range input.ReadlineCommandIDs() {
		if id == input.CmdBackspace {
			continue
		}
		h(id, func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
			a.QueryLine.Sync(a.Query)
			if changed, _ := a.QueryLine.HandleCommand(c); changed {
				a.Query = a.QueryLine.Value()
				a.reproject()
			}
			return s, nil
		})
	}
	h("input.accept", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Typing = false
		if a.Query == "" {
			a.Field = -1
		}
		return setPage(s, "normal", nodeGrid), nil
	})
	h("input.cancel", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Typing, a.Query, a.Field = false, "", -1
		a.reproject()
		return setPage(s, "normal", nodeGrid), nil
	})

	h("sort.cycle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.SortCol != a.Col {
			a.SortCol, a.SortDir = a.Col, table.SortAsc
		} else if a.SortDir = (a.SortDir + 1) % 3; a.SortDir == table.SortNone {
			a.SortCol = -1
		}
		a.reproject()
		a.info(sortLabel(a))
		return s, nil
	})
	h("group.toggle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		key := colKey(a.Col)
		keep, onRow := a.focusedID()
		if a.Grid.GroupField == key {
			a.Grid.GroupField = ""
			a.info("ungrouped")
		} else {
			a.Grid.GroupField = key
			a.info("grouped by " + a.headerName(a.Col))
		}
		a.Grid.Collapsed = nil
		if !onRow || !a.focusRow(keep) {
			a.Grid.SelectedIdx = 0
		}
		a.Grid.Focused()
		return s, nil
	})
	h("group.collapse", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Grid.GroupField == "" {
			a.warn("not grouped — g b groups by the active column")
			return s, nil
		}
		a.Grid.ToggleGroup()
		return s, nil
	})
	h("column.hide", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		vis := a.visible()
		if len(vis) <= 1 {
			a.warn("cannot hide the last visible column")
			return s, nil
		}
		if a.Hidden == nil {
			a.Hidden = map[int]bool{}
		}
		a.Hidden[a.Col] = true
		a.info("hid " + a.headerName(a.Col) + " — g = shows all")
		i := slices.Index(vis, a.Col)
		vis = slices.Delete(vis, i, i+1)
		a.Col = vis[min(i, len(vis)-1)]
		return s, nil
	})
	h("column.show.all", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if n := len(a.Hidden); n > 0 {
			a.Hidden = nil
			a.info(fmt.Sprintf("showing %d hidden column(s)", n))
		}
		return s, nil
	})
	h("detail.toggle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Detail = !a.Detail
		return s, nil
	})
	resize := func(d float64) fn {
		return func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
			if d == 0 {
				a.Ratio = env.Ratio
			} else {
				a.Ratio = min(max(a.Ratio+d*float64(c.Times()), 0.2), 0.8)
			}
			if !a.Detail {
				a.info("split set; enter shows the detail pane")
			}
			return s, nil
		}
	}
	scroll := func(dir int) fn {
		return func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
			if !a.Detail {
				a.info("the detail pane is hidden; enter shows it")
				return s, nil
			}
			if _, atEnd := s.Layout.Rects[detailTail]; dir < 0 {
				a.DetailOff = max(a.DetailOff-1, 0)
			} else if !atEnd {
				a.DetailOff++ // one line per key; the end is never overshot
			}
			return s, nil
		}
	}
	h("detail.scroll.down", scroll(1))
	h("detail.scroll.up", scroll(-1))
	h("split.grow", resize(0.05))
	h("split.shrink", resize(-0.05))
	h("split.reset", resize(0))
	h("yank.cell", env.yank)
	h("yank.row", env.yank)
	h("clipboard.done", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if err, _ := c.Args["error"].(error); err != nil {
			a.warn("clipboard: " + err.Error() + " (kept in the register)")
		} else {
			a.note("copied to the clipboard")
		}
		return s, nil
	})

	h("palette.open", env.paletteOpen)
	for _, id := range append([]string{"palette.insert", "palette.backspace", "palette.close", picker.CmdConfirm, picker.CmdModeCycle}, input.ReadlineCommandIDs()...) {
		scoped("palette", id, env.paletteCmd)
	}
	h("file.open", env.filesOpen)
	for _, id := range append([]string{"files.insert", "files.backspace", "files.close", pathpicker.CmdOpen, pathpicker.CmdParent, pathpicker.CmdHiddenToggle, pathpicker.CmdPreviewToggle, pathpicker.CmdModeCycle, picker.CmdConfirm}, input.ReadlineCommandIDs()...) {
		scoped("files", id, env.filesCmd)
	}
	h("help.open", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		return openModal(s, modalHelp, "help", 0), nil
	})
	h("help.close", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		return closeModal(s, modalHelp), nil
	})

	h("edit.toggle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Edit = !a.Edit
		switch {
		case a.Edit:
			a.note("edit mode — i edit row · o new row · d d delete · u undo · ctrl+s save")
		case a.Doc.Dirty():
			a.warn("view mode — unsaved edits are kept; ctrl+e then ctrl+s saves them")
		default:
			a.info("view mode (read-only)")
		}
		return s, nil
	})
	h("edit.row", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if !a.writable("editing a row") {
			return s, nil
		}
		id, _, ok := a.focusedRow()
		if !ok {
			a.warn("no row under the cursor")
			return s, nil
		}
		f, _ := newRowForm(a.Doc, id)
		return openModal(s, modalForm, "form", f), nil
	})
	for _, id := range append([]string{form.CmdInsert, form.CmdBackspace, "form.clear", form.CmdSubmit, form.CmdCancel}, input.ReadlineCommandIDs()...) {
		scoped("form", id, env.formCmd)
	}
	h(form.CmdSubmit, env.formCmd) // reachable from anywhere; still guarded
	h("edit.insert", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if !a.writable("inserting a row") {
			return s, nil
		}
		at := len(a.Doc.Rows)
		if id, _, ok := a.focusedRow(); ok {
			at = a.Doc.index(id) + 1
		}
		doc, id := a.Doc.InsertBlank(at)
		a.Doc = doc
		a.note(fmt.Sprintf("inserted row %d", at+1))
		a.docChanged(id)
		return s, nil
	})
	h("edit.delete", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if !a.writable("deleting a row") {
			return s, nil
		}
		id, _, ok := a.focusedRow()
		if !ok {
			a.warn("no row under the cursor")
			return s, nil
		}
		pos := a.Doc.index(id)
		a.Doc, _ = a.Doc.Delete(id)
		a.docChanged(-1)
		a.note(fmt.Sprintf("deleted row %d — u undoes", pos+1))
		return s, nil
	})
	for name := range transforms {
		h("edit."+name, func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
			if !a.writable("changing a cell") {
				return s, nil
			}
			id, row, ok := a.focusedRow()
			if !ok {
				a.warn("no row under the cursor")
				return s, nil
			}
			doc, changed := a.Doc.SetCell(id, a.Col, transforms[name](row[a.Col]))
			if !changed {
				a.info("cell unchanged")
				return s, nil
			}
			a.Doc = doc
			a.note(name + "-cased " + a.headerName(a.Col))
			a.docChanged(id)
			return s, nil
		})
	}
	history := func(redo bool) fn {
		return func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
			what := "undo"
			if redo {
				what = "redo"
			}
			if !a.writable(what) {
				return s, nil
			}
			var p patch
			var ok bool
			for range c.Times() {
				var q patch
				var did bool
				if redo {
					a.Doc, q, did = a.Doc.Redo()
				} else {
					a.Doc, q, did = a.Doc.Undo()
				}
				if !did {
					break
				}
				p, ok = q, true
			}
			if !ok {
				a.info("nothing to " + what)
				return s, nil
			}
			a.note(what + ": " + p.describe())
			a.docChanged(p.ID)
			return s, nil
		}
	}
	h("history.undo", history(false))
	h("history.redo", history(true))
	h("file.save", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if !a.writable("saving") {
			return s, nil
		}
		d := a.Doc
		if d.Path == "" {
			a.fail("there is no file to save to (" + d.Name + ")")
			return s, nil
		}
		// Synchronous on purpose: the engine is serialized, so the document
		// saved is exactly the one marked clean.
		if err := saveFile(d.Path, d.Header, d.Rows, d.Delim, d.NoHeader); err != nil {
			a.fail("save failed, file unchanged: " + err.Error())
			return s, nil
		}
		a.Doc = d.Saved(d.pos)
		a.note(fmt.Sprintf("saved %s (%d rows)", filepath.Base(d.Path), len(d.Rows)))
		return s, nil
	})
	h("app.close", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Doc.Dirty() && a.prevConfirm != "quit" {
			a.Confirm = "quit"
			a.warn("unsaved edits — press q again to quit without saving (ctrl+s saves)")
			return s, nil
		}
		return s, []core.Effect{core.QuitEffect{}}
	})
	return e
}

func sortLabel(a *App) string {
	if a.SortCol < 0 || a.SortDir == table.SortNone {
		return "file order"
	}
	dir := "ascending"
	if a.SortDir == table.SortDesc {
		dir = "descending"
	}
	return "sorted by " + a.headerName(a.SortCol) + " " + dir
}

func (env *Env) rowMotion(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	if (c.ID == "motion.bottom" || c.ID == "motion.top") && c.Count > 0 {
		a.Grid.SelectedIdx = c.Count - 1 // 5G / 5gg: go to line 5
		a.Grid.Focused()
		return s, nil
	}
	a.Grid.HandleCommand(c)
	return s, nil
}

func (env *Env) colMotion(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	vis := a.visible()
	if len(vis) == 0 {
		return s, nil
	}
	i := max(slices.Index(vis, a.Col), 0)
	switch c.ID {
	case "motion.left":
		i -= c.Times()
	case "motion.right":
		i += c.Times()
	case "motion.start":
		i = 0
	case "motion.end":
		i = len(vis) - 1
	}
	a.Col = vis[min(max(i, 0), len(vis)-1)]
	g := env.geometry(a, s.Width, s.Height)
	a.ColOff, _ = a.colWindow(g.tableW)
	return s, nil
}

func (env *Env) filterOpen(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	field := -1
	if c.ID == "filter.column" {
		field = a.Col
	}
	if field != a.Field {
		a.Query = ""
	}
	a.Field, a.Typing = field, true
	a.reproject()
	return setPage(s, "input", nodeFilter), nil
}

func (env *Env) yank(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	_, row, ok := a.focusedRow()
	if !ok {
		a.warn("no row under the cursor")
		return s, nil
	}
	text, what := row[a.Col], a.headerName(a.Col)
	if c.ID == "yank.row" {
		text, what = RowCSV(row, ','), "row"
	}
	a.Register = text
	if env.Copy == nil {
		a.info("yanked " + what + " to the register (no clipboard tool)")
		return s, nil
	}
	a.info("yanked " + what)
	cp := env.Copy
	return s, []core.Effect{core.AsyncEffect{Slot: "clipboard", Command: "clipboard.done", Run: func(context.Context) (any, error) {
		return nil, cp(text)
	}}}
}

// --- palette -------------------------------------------------------------------

func (env *Env) paletteOpen(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	keys := keysByCommand(env, "normal")
	var items []picker.Item
	for _, spec := range env.Specs {
		if !spec.HasTag("user") {
			continue
		}
		items = append(items, paletteItem(spec, keys))
	}
	for _, spec := range core.MotionSpecs() {
		if !slices.Contains(unusedMotions, spec.ID) {
			items = append(items, paletteItem(spec, keys))
		}
	}
	m := picker.New("Commands", items)
	m.Scope, m.Placeholder, m.MatchMode = "palette", "type to filter commands · tab cycles match mode", picker.MatchMode(a.Match)
	return openModal(s, modalPalette, "palette", m), nil
}

func paletteItem(spec core.CommandSpec, keys map[string][]string) picker.Item {
	sub := spec.Description
	if k := keys[spec.ID]; len(k) > 0 {
		sub += "  [" + strings.Join(k, ", ") + "]"
	}
	return picker.Item{ID: spec.ID, Title: spec.ID, Subtitle: sub, Keywords: []string{spec.Description}}
}

func (env *Env) paletteCmd(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	old, ok := modalData(s, modalPalette).(*picker.Model)
	if !ok {
		return s, nil
	}
	m := *old
	switch c.ID {
	case "palette.insert":
		m.AppendQuery(c.Arg("text"))
	case "palette.backspace":
		m.BackspaceQuery()
	case "palette.close":
		return closeModal(s, modalPalette), nil
	default:
		m.PageSize = max(s.Height*70/100-4, 1)
		picked, _ := m.HandleCommand(c)
		if picked != nil {
			s = closeModal(s, modalPalette)
			return s, []core.Effect{dispatch(core.Command{ID: picked.ID, Source: "palette", Actor: core.Actor{Kind: "user"}})}
		}
	}
	if m.FilterErr != "" {
		a.warn("regex: " + m.FilterErr)
	}
	return setModalData(s, modalPalette, &m), nil
}

// --- file picker -----------------------------------------------------------------

func (env *Env) filesOpen(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	dir := env.Cwd
	if a.Doc.Path != "" {
		dir = filepath.Dir(a.Doc.Path)
	}
	m, err := pathpicker.New(env.FS, dir, pathpicker.PickFile)
	if err != nil && env.Cwd != "" && dir != env.Cwd {
		m, err = pathpicker.New(env.FS, env.Cwd, pathpicker.PickFile)
	}
	if err != nil {
		a.fail("cannot browse " + dir + ": " + err.Error())
		return s, nil
	}
	m.ShowPreview = true
	m.MatchMode = picker.MatchMode(a.Match)
	m.Reload()
	return openModal(s, modalFiles, "files", m), nil
}

func (env *Env) filesCmd(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	old, ok := modalData(s, modalFiles).(*pathpicker.Model)
	if !ok {
		return s, nil
	}
	m := *old
	var picked *pathpicker.DirEntry
	switch c.ID {
	case "files.insert":
		m.AppendQuery(c.Arg("text"))
	case "files.backspace":
		if m.Query == "" {
			m.Parent()
		} else {
			m.BackspaceQuery()
		}
	case "files.close":
		return closeModal(s, modalFiles), nil
	default:
		m.List().PageSize = max(s.Height*80/100-6, 1)
		picked, _ = m.HandleCommand(c)
	}
	if m.Err != "" {
		a.fail(m.Err)
	}
	s = setModalData(s, modalFiles, &m)
	if picked == nil {
		return s, nil
	}
	if a.Doc.Dirty() && a.prevConfirm != "open:"+picked.Path {
		a.Confirm = "open:" + picked.Path
		a.warn("unsaved edits in " + a.Doc.Name + " — enter again opens " + picked.Name + " and discards them (esc cancels)")
		return s, nil
	}
	opts := Options{Delim: extDelim(picked.Path), NoHeader: env.NoHeader}
	t, err := ReadFile(picked.Path, opts)
	if err != nil {
		a.fail("cannot open " + picked.Name + ": " + err.Error())
		return s, nil
	}
	edit := a.Edit
	*a = newApp(env, newDoc(picked.Path, t, env.NoHeader), edit)
	a.note(fmt.Sprintf("opened %s (%d rows, %s)", picked.Name, len(t.Rows), DelimName(t.Delim)))
	return closeModal(s, modalFiles), nil
}

// --- help -------------------------------------------------------------------------

func (env *Env) helpScroll(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	off, _ := modalData(s, modalHelp).(int)
	total := len(env.helpLines())
	page := max(s.Height*85/100-2, 1)
	n := c.Times()
	switch c.ID {
	case "motion.down":
		off += n
	case "motion.up":
		off -= n
	case "motion.top":
		off = 0
	case "motion.bottom":
		off = total
	case "motion.page.down":
		off += n * page
	case "motion.page.up":
		off -= n * page
	case "motion.half.down":
		off += n * page / 2
	case "motion.half.up":
		off -= n * page / 2
	}
	off = min(max(off, 0), max(total-page, 0))
	return setModalData(s, modalHelp, off), nil
}

// --- form -------------------------------------------------------------------------

func (env *Env) formCmd(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	old, ok := modalData(s, modalForm).(rowForm)
	if !ok {
		return s, nil
	}
	f := old.clone()
	switch c.ID {
	case "form.clear":
		if i := f.M.ActiveIdx; i >= 0 && i < len(f.M.Specs) {
			f.M.SetValue(f.M.Specs[i].Name, "")
		}
	case form.CmdSubmit:
		if !a.writable("applying the form") {
			return s, nil
		}
		res, _ := f.M.HandleCommand(c)
		if !res.Submitted {
			var msgs []string
			for _, name := range res.ErrorFields {
				msgs = append(msgs, name+": "+f.M.Errors[name])
			}
			a.fail("not applied — " + strings.Join(msgs, "; "))
			return setModalData(s, modalForm, f), nil
		}
		row, ok := a.Doc.Row(f.ID)
		if !ok {
			a.fail("the row no longer exists")
			return closeModal(s, modalForm), nil
		}
		doc, changed := a.Doc.SetRow(f.ID, f.values(row))
		if !changed {
			a.info("row unchanged")
		} else {
			a.Doc = doc
			a.note(fmt.Sprintf("edited row %d — u undoes", a.Doc.index(f.ID)+1))
			a.docChanged(f.ID)
		}
		return closeModal(s, modalForm), nil
	default:
		res, _ := f.M.HandleCommand(c)
		if res.Cancelled {
			a.info("edit discarded")
			return closeModal(s, modalForm), nil
		}
	}
	return setModalData(s, modalForm, f), nil
}

// --- shell ---------------------------------------------------------------------------

// newShell builds the engine and tea shell over doc.
func newShell(env *Env, doc Doc, edit bool) *otea.Shell {
	a := newApp(env, doc, edit)
	if len(env.Notes) > 0 {
		a.warn(strings.Join(env.Notes, " · "))
	}
	initial := core.NewState(core.Frame{ID: pageID, FocusID: nodeGrid, Mode: "normal", Data: a})
	sh := otea.New(newEngine(env), initial, env.view, env.Theme)
	// The mouse wheel over the grid scrolls it; other mouse input is ignored.
	sh.OnPassThrough = func(e core.PassToWidgetEffect) tea.Cmd {
		id := map[string]string{"wheeldown": "motion.down", "wheelup": "motion.up"}[mouseButton(e)]
		if id == "" || e.NodeID != nodeGrid {
			return nil
		}
		return func() tea.Msg { return core.CommandEvent{Command: core.Command{ID: id, Count: 3, Source: "mouse"}} }
	}
	sh.OnCustom = func(e core.CustomEffect) tea.Cmd {
		c, ok := e.Data.(core.Command)
		if e.Kind != "dispatch" || !ok {
			return nil
		}
		return func() tea.Msg { return core.CommandEvent{Command: c} }
	}
	return sh
}

func mouseButton(e core.PassToWidgetEffect) string {
	if e.Mouse == nil {
		return ""
	}
	return e.Mouse.Button
}

// driver feeds a Shell synchronously, without a terminal: every command a
// step returns runs at once and its message is fed back. The smoke run and
// the tests use it; it expects PrefixTimeout 0 and MaxFPS 0, so no timer
// commands are produced.
type driver struct{ sh *otea.Shell }

func newDriver(sh *otea.Shell, w, h int) *driver {
	sh.Engine.PrefixTimeout = 0
	sh.MaxFPS = 0
	d := &driver{sh: sh}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.sh.Update(msg)
	d.pump(cmd)
}

func (d *driver) pump(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var m tea.Msg
	select {
	case m = <-done:
	case <-time.After(5 * time.Second):
		return
	}
	switch m := m.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			d.pump(c)
		}
	case tea.QuitMsg, nil:
	default:
		d.send(m)
	}
}

// keys sends key presses by core name: "j", "G", "ctrl+d", "enter", "esc",
// "space", "backspace", "tab", "up", "down", "left", "right".
func (d *driver) keys(names ...string) {
	for _, n := range names {
		d.send(keyMsg(n))
	}
}

// typeText sends each rune of s as a key press.
func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == ' ' {
			d.send(keyMsg("space"))
		} else {
			d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

// command dispatches a command as the palette or a script would.
func (d *driver) command(id string) {
	d.send(core.CommandEvent{Command: core.Command{ID: id, Source: "palette", Actor: core.Actor{Kind: "user"}}})
}

var namedKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
}

func keyMsg(n string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		if rest, ok := strings.CutPrefix(n, "ctrl+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModCtrl, rest
		} else if rest, ok := strings.CutPrefix(n, "alt+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModAlt, rest
		} else if rest, ok := strings.CutPrefix(n, "shift+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModShift, rest
		} else {
			break
		}
	}
	if r, ok := namedKeys[n]; ok {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	if n == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " ", Mod: mod}
	}
	r := []rune(n)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: n}
}
