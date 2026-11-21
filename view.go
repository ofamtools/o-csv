package main

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// geom is where the grid lands: its size, the data rows it shows, and
// whether the detail pane sits beside it (else below).
type geom struct {
	tableW, tableH, rows int
	side                 bool
}

// geometry mirrors the view's layout: a title line, the body (grid, or a
// split of grid and detail), the filter line while typing, and two status
// lines. The split rounds like layout.Solve.
func (env *Env) geometry(a *App, w, h int) geom {
	if w <= 0 || h <= 0 {
		w, h = 120, 32
	}
	body := h - 3
	if a.Typing {
		body--
	}
	body = max(body, 3)
	g := geom{tableW: w, tableH: body}
	if a.Detail {
		if w >= env.StackW {
			g.side = true
			g.tableW = int(math.Round(float64(max(0, w-1)) * a.Ratio))
		} else {
			g.tableH = int(math.Round(float64(max(0, body-1)) * a.Ratio))
		}
	}
	g.rows = max(g.tableH-2, 1)
	return g
}

func textW(s string) int { return utf8.RuneCountInString(s) }

// colTitle is a column header: the name, a sort arrow, and a marker on the
// active column.
func (a *App) colTitle(j int) string {
	t := a.headerName(j)
	if j == a.SortCol && a.SortDir == table.SortAsc {
		t += " ▲"
	} else if j == a.SortCol && a.SortDir == table.SortDesc {
		t += " ▼"
	}
	if j == a.Col {
		t = "▸" + t
	}
	return t
}

// colWindow picks the visible columns [off, end) that fit width tw and
// include the active column, scrolling from ColOff as little as possible.
// Widths follow the table renderer: title or widest of 200 rows, max 40.
func (a *App) colWindow(tw int) (int, int) {
	vis := a.visible()
	n := len(vis)
	if n == 0 {
		return 0, 0
	}
	vi := max(slices.Index(vis, a.Col), 0)
	sample := a.Grid.Rows[:min(len(a.Grid.Rows), 200)]
	widths := make([]int, n)
	for i, j := range vis {
		w := textW(a.colTitle(j))
		for _, r := range sample {
			w = min(max(w, textW(table.Text(r.Cells[colKey(j)]))), 40)
		}
		widths[i] = w
	}
	span := func(from, to int) int {
		s := 2 * (to - from - 1)
		for k := from; k < to; k++ {
			s += widths[k]
		}
		return s
	}
	off := min(max(a.ColOff, 0), vi)
	for off < vi && span(off, vi+1) > tw {
		off++
	}
	end := vi + 1
	for end < n && span(off, end+1) <= tw {
		end++
	}
	for end == n && off > 0 && span(off-1, end) <= tw {
		off--
	}
	return off, end
}

func text(t, tone string) ui.ViewNode {
	return ui.ViewNode{Kind: ui.KindText, Tone: tone, Props: map[string]any{"text": t}}
}

func clip(s string, w int) string {
	if r := []rune(s); w > 1 && len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

func oneLine(s string) string {
	return strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎", "\t", " ").Replace(s)
}

// view builds the whole screen.
func (env *Env) view(s core.State) ui.ViewNode {
	a := appOf(s)
	w := s.Width
	if w <= 0 {
		w = 120
	}
	g := env.geometry(&a, s.Width, s.Height)
	grid := a.Grid.Copy()
	grid.PageSize = g.rows
	off, end := a.colWindow(g.tableW)
	vis := a.visible()
	grid.Columns = nil
	for _, j := range vis[off:end] {
		grid.Columns = append(grid.Columns, table.Column{Key: colKey(j), Title: a.colTitle(j)})
	}
	body := grid.View(nodeGrid)
	if a.Detail {
		split := ui.ViewNode{ID: "body", Kind: ui.KindSplit, Ratio: a.Ratio, Gap: 1, Props: map[string]any{"axis": "horizontal"}}
		if !g.side {
			split.Props["axis"] = "vertical"
		}
		split.Children = []ui.ViewNode{body, a.detailNode(&grid)}
		body = split
	}

	children := []ui.ViewNode{a.titleLine(w), body}
	if a.Typing {
		children = append(children, a.inputNode())
	}
	children = append(children, a.infoLine(s, &grid, off, end, w), env.statusLine(&a, w))
	root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{
		{ID: "page", Kind: ui.KindVStack, Scope: "grid", Children: children},
	}}
	for _, m := range s.Modals {
		root.Children = append(root.Children, env.modalView(s, m))
	}
	return root
}

func (a *App) titleLine(w int) ui.ViewNode {
	d := a.Doc
	mode, tone := " VIEW ", "badge"
	if a.Edit {
		mode, tone = " EDIT ", "badge.warning"
	}
	tail := ""
	if d.NoHeader {
		tail = " · no header"
	}
	// A long path never pushes the mode badge off the line: use the base
	// name, then clip it to what is left.
	rest := fmt.Sprintf(" o-csv · %s · %d rows × %d cols · %s", "", len(d.Rows), len(d.Header), DelimName(d.Delim))
	name := d.Name
	if avail := w - textW(rest) - textW(tail) - 1 - textW(mode); textW(name) > avail {
		name = filepath.Base(name)
		if textW(name) > avail {
			name = clip(name, max(avail, 1))
		}
	}
	line := fmt.Sprintf(" o-csv · %s · %d rows × %d cols · %s", name, len(d.Rows), len(d.Header), DelimName(d.Delim))
	if d.Path == "" && d.Name == "no file" {
		line = " o-csv · no file open · ctrl+o opens one · q quits"
	}
	line += tail
	line += " "
	from := textW(line)
	line += mode
	spans := []ui.ToneSpan{{From: 0, To: from, Tone: "title"}, {From: from, To: textW(line), Tone: tone}}
	if d.Dirty() {
		dirty := " ● unsaved edits"
		if !a.Edit {
			dirty += " (view mode keeps them; ctrl+e, ctrl+s saves)"
		}
		spans = append(spans, ui.ToneSpan{From: textW(line), To: textW(line) + textW(dirty), Tone: "warning"})
		line += dirty
	}
	n := text(clip(line, w), "title")
	n.Props["spans"] = spans
	return n
}

func (a *App) inputNode() ui.ViewNode {
	label := "/ all columns"
	if a.Field >= 0 {
		label = "f " + a.headerName(a.Field)
	}
	label += " [" + string(a.Match) + "]"
	return ui.ViewNode{ID: nodeFilter, Kind: ui.KindInput, Focusable: true, Title: label, Props: map[string]any{
		"value": a.Query, "cursor": utf8.RuneCountInString(a.Query),
		"placeholder": "type to filter · enter keeps · esc clears · tab/ctrl+m match mode · ↑↓ move",
	}}
}

func (a *App) infoLine(s core.State, grid *table.Model, off, end, w int) ui.ViewNode {
	var parts []string
	items := grid.Items()
	pos := "no rows"
	if len(items) > 0 {
		pos = fmt.Sprintf("row %d/%d", grid.SelectedIdx+1, len(items))
	}
	if len(grid.Rows) != len(a.Doc.Rows) {
		pos += fmt.Sprintf(" (%d of %d match)", len(grid.Rows), len(a.Doc.Rows))
	}
	parts = append(parts, pos)
	vis := a.visible()
	col := fmt.Sprintf("col %d/%d", slices.Index(vis, a.Col)+1, len(vis))
	if len(a.Hidden) > 0 {
		col += fmt.Sprintf(" (%d hidden)", len(a.Hidden))
	}
	if off > 0 || end < len(vis) {
		col += fmt.Sprintf(" showing %d-%d", off+1, end)
	}
	parts = append(parts, col)
	if r, ok := grid.FocusedRow(); ok {
		parts = append(parts, a.headerName(a.Col)+" = "+clip(oneLine(table.Text(r.Cells[colKey(a.Col)])), 40))
	}
	tone := "muted"
	if a.Query != "" || a.Typing {
		scope := "all"
		if a.Field >= 0 {
			scope = a.headerName(a.Field)
		}
		parts = append(parts, fmt.Sprintf("filter %s %s %q", a.Match, scope, a.Query))
	}
	if a.FilterErr != "" {
		parts = append(parts, "regex error: "+a.FilterErr+" (no rows shown)")
		tone = "error"
	}
	if a.SortCol >= 0 && a.SortDir != table.SortNone {
		parts = append(parts, sortLabel(a))
	}
	if a.Grid.GroupField != "" {
		j, _ := strconv.Atoi(strings.TrimPrefix(a.Grid.GroupField, "c"))
		parts = append(parts, "grouped by "+a.headerName(j))
	}
	if in := s.Input; in.Count != nil || len(in.Prefix) > 0 {
		p := strings.Join(in.Prefix, " ")
		if in.Count != nil {
			p = strconv.Itoa(*in.Count) + " " + p
		}
		parts = append(parts, "pending "+strings.TrimSpace(p))
	}
	return text(clip(" "+strings.Join(parts, " · "), w), tone)
}

func (env *Env) statusLine(a *App, w int) ui.ViewNode {
	if a.Status != "" {
		return text(clip(" "+oneLine(a.Status), w), a.Tone)
	}
	hint := " ? help · : commands · / filter · f column · s sort · g b group · enter detail · ctrl+o open · ctrl+e edit · q quit"
	if a.Edit {
		hint = " EDIT · i edit row · o new row · d d delete · t u/l/t case · u undo · ctrl+r redo · ctrl+s save · ctrl+e view · ? help"
	}
	return text(clip(hint, w), "muted")
}

// detailNode shows every column of the focused row in full, hidden ones
// included and marked.
func (a *App) detailNode(grid *table.Model) ui.ViewNode {
	n := ui.ViewNode{ID: "detail", Kind: ui.KindPanel, Title: "Detail"}
	it, ok := grid.Focused()
	var b strings.Builder
	var spans []ui.ToneSpan
	switch {
	case !ok:
		b.WriteString("no rows")
	case it.Header:
		j, _ := strconv.Atoi(strings.TrimPrefix(a.Grid.GroupField, "c"))
		n.Title = "Group"
		fmt.Fprintf(&b, "%s = %s\n%d row(s) · z a folds", a.headerName(j), it.Group, it.Count)
	default:
		id, _ := strconv.Atoi(it.Row.ID)
		row, _ := a.Doc.Row(id)
		n.Title = fmt.Sprintf("Row %d of %d", a.Doc.index(id)+1, len(a.Doc.Rows))
		for j, v := range row {
			name := a.headerName(j)
			if a.Hidden[j] {
				name += " (hidden)"
			}
			from := utf8.RuneCountInString(b.String())
			b.WriteString(name + ":")
			tone := "accent"
			if j == a.Col {
				tone = "match"
			}
			spans = append(spans, ui.ToneSpan{From: from, To: from + textW(name) + 1, Tone: tone})
			b.WriteString(" " + v + "\n")
		}
	}
	t := text(strings.TrimRight(b.String(), "\n"), "text")
	if len(spans) > 0 {
		t.Props["spans"] = spans
	}
	// The scroll node clamps the offset. The blank tail line is visible only
	// when the content is scrolled to the end (see detail.scroll.down).
	t.ID = detailText
	tail := text(" ", "text")
	tail.ID = detailTail
	n.Children = []ui.ViewNode{{ID: "detail.scroll", Kind: ui.KindScroll, Props: map[string]any{"offset": a.DetailOff}, Children: []ui.ViewNode{t, tail}}}
	return n
}

// formWindow is a view-only copy of m showing as many fields as fit a screen
// of height h (the widget draws one line per field and clips a taller form),
// scrolled so the active field is always inside. The overlay's border, the
// widget's submit line, a two-line hint and the validation lines are
// reserved. The backing form is untouched, so every field stays editable
// and validated; only Specs and ActiveIdx of the copy are narrowed.
func formWindow(m *form.Model, h, errLines int) *form.Model {
	if h <= 0 {
		h = 32
	}
	n := len(m.Specs)
	size := max(h-2-1-2-errLines, 1)
	if n <= size {
		return m
	}
	start := min(max(m.ActiveIdx-size/2, 0), n-size)
	w := *m
	w.Specs = m.Specs[start : start+size]
	w.ActiveIdx = m.ActiveIdx - start
	w.Title = fmt.Sprintf("%s · fields %d-%d of %d", m.Title, start+1, start+size, n)
	return &w
}

func (env *Env) modalView(s core.State, f core.Frame) ui.ViewNode {
	switch f.ID {
	case modalPalette:
		m, _ := f.Data.(*picker.Model)
		if m == nil {
			break
		}
		return ui.ViewNode{ID: "palette.overlay", Kind: ui.KindOverlay, Scope: "palette", Title: "Command palette · " + string(m.MatchMode),
			Props:    map[string]any{"widthPct": 70, "heightPct": 70},
			Children: []ui.ViewNode{m.View(modalPalette), text("enter runs · esc closes · tab/ctrl+m match mode · ↑↓ move", "muted")}}
	case modalFiles:
		m, _ := f.Data.(*pathpicker.Model)
		if m == nil {
			break
		}
		return ui.ViewNode{ID: "files.overlay", Kind: ui.KindOverlay, Scope: "files", Title: "Open a CSV, TSV or PSV file · " + string(m.MatchMode),
			Props:    map[string]any{"widthPct": 90, "heightPct": 80},
			Children: []ui.ViewNode{m.View(modalFiles), text("enter opens · ← parent · backspace edits filter (parent when empty) · tab preview · ctrl+t dotfiles · esc closes", "muted")}}
	case modalHelp:
		off, _ := f.Data.(int)
		lines := env.helpLines()
		off = min(max(off, 0), len(lines))
		return ui.ViewNode{ID: modalHelp, Kind: ui.KindOverlay, Scope: "help", Focusable: true, Title: "Help · active key bindings",
			Props:    map[string]any{"widthPct": 80, "heightPct": 85},
			Children: []ui.ViewNode{text(strings.Join(lines[off:], "\n"), "text")}}
	case modalForm:
		rf, ok := f.Data.(rowForm)
		if !ok {
			break
		}
		var errs []ui.ViewNode
		for _, sp := range rf.M.Specs {
			if e := rf.M.Errors[sp.Name]; e != "" {
				errs = append(errs, text(sp.Name+": "+e, "error"))
			}
		}
		win := formWindow(rf.M, s.Height, len(errs))
		kids := append([]ui.ViewNode{win.View(modalForm)}, errs...)
		kids = append(kids, text("type to edit · backspace · ctrl+u clears · tab/↑↓ field · enter applies · esc cancels", "muted"))
		return ui.ViewNode{ID: "form.overlay", Kind: ui.KindOverlay, Title: win.Title, Props: map[string]any{"widthPct": 70}, Children: kids}
	}
	return ui.ViewNode{ID: f.ID + ".overlay", Kind: ui.KindOverlay, Children: []ui.ViewNode{text(f.ID, "muted")}}
}

// --- live help ------------------------------------------------------------------

// keyHelp is one binding that actually resolves in its mode.
type keyHelp struct{ Keys, Command, Desc string }

var helpSections = []struct {
	mode, title string
	chain       []core.ContextFrame
}{
	{"normal", "Grid", []core.ContextFrame{{Scope: "row"}, {Scope: "grid"}, {Scope: "app"}}},
	{"input", "Filter input (/ or f)", []core.ContextFrame{{Scope: "grid"}, {Scope: "app"}}},
	{"palette", "Command palette (:)", []core.ContextFrame{{Scope: "palette"}, {Scope: "app"}}},
	{"files", "File picker (ctrl+o)", []core.ContextFrame{{Scope: "pathpicker.file"}, {Scope: "files"}, {Scope: "app"}}},
	{"form", "Row form (i / e)", []core.ContextFrame{{Scope: "form"}, {Scope: "app"}}},
	{"help", "Help", []core.ContextFrame{{Scope: "help"}, {Scope: "app"}}},
}

var classSamples = map[string]string{core.ClassDigit0: "0", core.ClassDigit: "5", core.ClassAlpha: "x", core.ClassPrintable: "x", core.ClassPaste: core.PasteKey, core.ClassAny: "x"}

// resolveWinner runs keys through the keymap from an idle state and
// returns the binding that fires.
func resolveWinner(km *core.Keymap, chain []core.ContextFrame, mode string, keys []string) *core.Binding {
	in := core.InputState{}
	for i, k := range keys {
		res := km.Resolve(core.KeyQuery{Chain: chain, Mode: mode, Input: in, Key: k, Text: k})
		in = res.Input
		last := i == len(keys)-1
		switch {
		case !last && res.Outcome == core.Pending:
		case last && (res.Outcome == core.Matched || res.Outcome == core.PushedLayer):
			return res.Trace.Winner
		case last && res.Outcome == core.Pending && res.Trace.Winner != nil:
			return res.Trace.Winner // exact match that is also a prefix (fires on timeout)
		default:
			return nil
		}
	}
	return nil
}

// activeBindings lists the bindings of mode that win resolution, so user
// overrides and unbinds show exactly as they behave.
func (env *Env) activeBindings(mode string, chain []core.ContextFrame) []keyHelp {
	var out []keyHelp
	seen := map[string]bool{}
	for _, bd := range env.Bindings {
		if bd.Hidden || bd.Unbind || (bd.Mode != mode && bd.Mode != "") || bd.Command == "" && bd.Layer == nil {
			continue
		}
		keys, label := bd.Keys, strings.Join(bd.Keys, " ")
		if len(keys) == 0 {
			sample, ok := classSamples[bd.Class]
			if !ok {
				continue
			}
			keys, label = []string{sample}, "‹"+bd.Class+"›"
			if bd.Class == core.ClassDigit0 {
				label = "0"
			}
		}
		if seen[label] {
			continue
		}
		win := resolveWinner(env.Km, chain, mode, keys)
		if win == nil || win.Command != bd.Command || !slices.Equal(win.Keys, bd.Keys) || win.Class != bd.Class {
			continue
		}
		seen[label] = true
		desc := bd.Hint
		if spec, ok := env.Km.Commands.Lookup(bd.Command); ok && spec.Description != "" {
			desc = spec.Description
		}
		out = append(out, keyHelp{Keys: label, Command: bd.Command, Desc: desc})
	}
	return out
}

// keysByCommand maps command IDs to their active keys in mode.
func keysByCommand(env *Env, mode string) map[string][]string {
	out := map[string][]string{}
	for _, sec := range helpSections {
		if sec.mode != mode {
			continue
		}
		for _, k := range env.activeBindings(mode, sec.chain) {
			out[k.Command] = append(out[k.Command], k.Keys)
		}
	}
	return out
}

// helpLines renders the help page from the live keymap.
func (env *Env) helpLines() []string {
	var lines []string
	for _, sec := range helpSections {
		bs := env.activeBindings(sec.mode, sec.chain)
		if len(bs) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, sec.title)
		for _, k := range bs {
			lines = append(lines, fmt.Sprintf("  %-12s %s  (%s)", k.Keys, k.Desc, k.Command))
		}
	}
	lines = append(lines, "",
		"Counts prefix motions: 10j, 3l, 5G. 0 is a motion only while no count is pending.",
		"ctrl+m is distinct from enter only on terminals with the kitty keyboard protocol;",
		"elsewhere use alt+m, tab in inputs, or : filter.mode.cycle.",
		"User bindings, theme and settings load from $XDG_CONFIG_HOME/o-csv or ~/.config/o-csv.")
	for _, n := range env.Notes {
		lines = append(lines, "config: "+n)
	}
	return lines
}
