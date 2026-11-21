package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// --- helpers --------------------------------------------------------------------

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func copyFile(t *testing.T, src, dir string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, dir, filepath.Base(src), string(data))
}

// openApp starts a shell over path at 120x32 through the real engine.
func openApp(t *testing.T, path string, edit bool, cfg Config) *driver {
	t.Helper()
	tab, err := ReadFile(path, Options{Delim: extDelim(path), NoHeader: cfg.NoHeader})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cwd == "" {
		cfg.Cwd = filepath.Dir(path)
	}
	return newDriver(newShell(newEnv(cfg), newDoc(path, tab, cfg.NoHeader), edit), 120, 32)
}

func (d *driver) app() App            { return appOf(d.sh.State) }
func (d *driver) items() []table.Item { a := d.app(); return a.Grid.Items() }
func (d *driver) frame() string       { return plain(d.sh.View().Content) }

func (d *driver) modal() string {
	if n := len(d.sh.State.Modals); n > 0 {
		return d.sh.State.Modals[n-1].ID
	}
	return ""
}

// shownIDs lists the source IDs of the displayed data rows, in order.
func shownIDs(a App) []int {
	var out []int
	for _, it := range a.Grid.Items() {
		if !it.Header {
			n, _ := strconv.Atoi(it.Row.ID)
			out = append(out, n)
		}
	}
	return out
}

func resize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func quitRequested(d *driver) bool {
	return slices.ContainsFunc(d.sh.LastEffects, func(e core.Effect) bool { _, ok := e.(core.QuitEffect); return ok })
}

type fakeClip struct {
	mu   sync.Mutex
	got  []string
	copy func(string) error
}

func newClip() *fakeClip {
	c := &fakeClip{}
	c.copy = func(s string) error { c.mu.Lock(); defer c.mu.Unlock(); c.got = append(c.got, s); return nil }
	return c
}

func (c *fakeClip) all() []string { c.mu.Lock(); defer c.mu.Unlock(); return slices.Clone(c.got) }

// --- read-only ----------------------------------------------------------------------

func TestReadOnlyNeverWrites(t *testing.T) {
	dir := t.TempDir()
	path := copyFile(t, "testdata/cities.csv", dir)
	before, _ := os.ReadFile(path)
	info0, _ := os.Stat(path)
	d := openApp(t, path, false, Config{})

	// Keys.
	d.keys("i")
	if d.modal() != "" {
		t.Fatalf("i opened %q in view mode", d.modal())
	}
	if !strings.Contains(d.app().Status, "read-only") {
		t.Fatalf("no read-only warning: %q", d.app().Status)
	}
	d.keys("e", "o", "d", "d", "t", "u", "t", "l", "t", "t", "u", "ctrl+r", "ctrl+s")
	// Direct dispatch, as the palette, CLI or a script would.
	for _, id := range []string{"edit.row", "edit.insert", "edit.delete", "edit.upper", "edit.lower", "edit.title", "history.undo", "history.redo", "file.save", "form.submit"} {
		d.command(id)
	}
	// And through the palette itself.
	d.keys(":")
	d.typeText("edit.insert")
	d.keys("enter")
	if d.modal() != "" || len(d.app().Doc.Rows) != 10 || !strings.Contains(d.app().Status, "read-only") {
		t.Fatalf("palette edit.insert not guarded: modal=%q rows=%d status=%q", d.modal(), len(d.app().Doc.Rows), d.app().Status)
	}
	if d.app().Doc.Dirty() || len(d.app().Doc.Rows) != 10 {
		t.Fatal("view mode changed the document")
	}

	// Leaving edit mode keeps edits, warns, and still never writes.
	d.keys("ctrl+e", "t", "u")
	if !d.app().Doc.Dirty() {
		t.Fatal("t u in edit mode did not change the cell")
	}
	d.keys("ctrl+e")
	a := d.app()
	if a.Edit || !a.Doc.Dirty() || !strings.Contains(a.Status, "unsaved edits are kept") {
		t.Fatalf("leaving edit mode: edit=%v dirty=%v status=%q", a.Edit, a.Doc.Dirty(), a.Status)
	}
	if !strings.Contains(d.frame(), "unsaved edits") {
		t.Fatal("frame does not show the unsaved marker")
	}
	d.keys("ctrl+s")
	d.command("file.save")

	after, _ := os.ReadFile(path)
	info1, _ := os.Stat(path)
	if !bytes.Equal(before, after) || !info0.ModTime().Equal(info1.ModTime()) {
		t.Fatal("file changed in view mode")
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("stray files: %v", es)
	}

	// Quitting with unsaved edits asks once.
	d.keys("q")
	if quitRequested(d) || !strings.Contains(d.app().Status, "q again") {
		t.Fatalf("first q quit with unsaved edits: %q", d.app().Status)
	}
	d.keys("j", "q")
	if quitRequested(d) {
		t.Fatal("confirmation survived another key")
	}
	d.keys("q")
	if !quitRequested(d) {
		t.Fatal("second q did not quit")
	}
}

// --- filters -------------------------------------------------------------------------

func TestFilterModesColumnAndInvalidRegex(t *testing.T) {
	clipboard := newClip()
	d := openApp(t, "testdata/cities.csv", false, Config{Copy: clipboard.copy})

	d.keys("/")
	if !d.app().Typing || d.sh.State.ActiveFrame().Mode != "input" {
		t.Fatal("/ did not open the filter input")
	}
	d.typeText("japan")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{0, 1, 9}) {
		t.Fatalf("substring japan = %v", got)
	}
	// Navigation while the input is open; typing keeps going to the input.
	d.keys("down", "ctrl+n")
	if d.app().Grid.SelectedIdx != 2 || !d.app().Typing {
		t.Fatalf("down while typing: idx %d", d.app().Grid.SelectedIdx)
	}
	d.keys("up")
	if d.app().Grid.SelectedIdx != 1 {
		t.Fatalf("up while typing: idx %d", d.app().Grid.SelectedIdx)
	}
	d.typeText("j") // digits and letters are text here, not motions
	if d.app().Query != "japanj" {
		t.Fatalf("query = %q", d.app().Query)
	}
	d.keys("backspace", "enter")
	if d.app().Typing || d.app().Query != "japan" || len(d.app().Grid.Rows) != 3 {
		t.Fatal("enter should keep the filter and leave the input")
	}
	d.keys("esc")
	if d.app().Query != "" || len(d.app().Grid.Rows) != 10 {
		t.Fatal("esc in the grid should clear the filter")
	}

	// Mode cycling: ctrl+m (distinct key event) and tab in the input.
	d.keys("/", "ctrl+m")
	if d.app().Match != table.MatchFuzzy {
		t.Fatalf("ctrl+m: %s", d.app().Match)
	}
	d.typeText("tky")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{0, 3}) { // Tokyo; "City of Kings, founded by"
		t.Fatalf("fuzzy tky = %v", got)
	}
	d.keys("tab")
	if d.app().Match != table.MatchRegex {
		t.Fatalf("tab: %s", d.app().Match)
	}
	for range 3 {
		d.keys("backspace")
	}
	d.typeText("^(o|k)")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{1, 4, 9}) {
		t.Fatalf("regex ^(o|k) = %v", got)
	}
	d.keys("backspace", "backspace", "backspace", "backspace", "backspace") // "^"
	d.typeText("(")
	a := d.app()
	if a.FilterErr == "" || len(a.Grid.Rows) != 0 {
		t.Fatalf("invalid regex: err %q rows %d", a.FilterErr, len(a.Grid.Rows))
	}
	if !strings.Contains(d.frame(), "regex error") {
		t.Fatal("frame does not show the regex error")
	}
	d.keys("backspace")
	if d.app().FilterErr != "" {
		t.Fatal("error should clear with a valid pattern")
	}
	d.keys("esc")
	if d.app().Typing || d.app().Query != "" || len(d.app().Grid.Rows) != 10 {
		t.Fatal("esc in the input should clear the filter")
	}
	d.keys("ctrl+m") // back to substring from the grid (ctrl+m stays distinct from enter)
	if d.app().Match != table.MatchSubstring || d.app().Detail {
		t.Fatalf("ctrl+m in the grid: %s detail=%v", d.app().Match, d.app().Detail)
	}

	// Active-column filter versus all columns.
	d.keys("l", "f")
	d.typeText("no")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{4, 7}) {
		t.Fatalf("country~no = %v", got)
	}
	d.keys("esc", "/")
	d.typeText("no")
	if got := shownIDs(d.app()); !slices.Contains(got, 1) {
		t.Fatalf("all-column no should match 'Known': %v", got)
	}
	d.keys("esc")

	// Hidden columns stay searchable, in the detail pane and in row yanks.
	d.keys("$", "-")
	if header := strings.Split(d.frame(), "\n")[1]; !d.app().Hidden[6] || strings.Contains(header, "notes") || !strings.Contains(header, "▸capital") {
		t.Fatal("notes not hidden")
	}
	d.keys("/")
	d.typeText("pizarro")
	d.keys("enter")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("search in hidden column = %v", got)
	}
	d.keys("enter")
	tree := newEnv(Config{}).view(d.sh.State)
	det := tree.Find(detailText)
	if det == nil {
		t.Fatalf("no detail text node:\n%s", d.frame())
	}
	// The label starts a line, so word wrapping cannot split it in the frame.
	if txt, _ := det.Props["text"].(string); !strings.Contains(txt, "notes (hidden): City of Kings, founded by Pizarro") || !strings.Contains(d.frame(), "notes (hidden)") {
		t.Fatalf("detail misses hidden column:\n%s", d.frame())
	}
	d.keys("Y")
	want := `Lima,Peru,9752000,2672,1535,true,"City of Kings, founded by Pizarro"`
	if d.app().Register != want || !slices.Contains(clipboard.all(), want) {
		t.Fatalf("Y = %q, clipboard %q", d.app().Register, clipboard.all())
	}
	d.keys("g", "=")
	if len(d.app().Hidden) != 0 {
		t.Fatal("g = did not restore columns")
	}
}

// --- sort, group, counts ----------------------------------------------------------------

func TestSortStabilityGroupCollapse(t *testing.T) {
	d := openApp(t, "testdata/cities.csv", false, Config{})
	d.keys("j", "j") // Lagos (2)
	d.keys("l", "s")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{0, 1, 9, 2, 4, 7, 3, 8, 5, 6}) {
		t.Fatalf("country asc = %v", got)
	}
	a := d.app()
	if id, _ := a.focusedID(); id != "2" {
		t.Fatalf("cursor left Lagos: %s", id)
	}
	d.keys("s")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{6, 5, 3, 8, 4, 7, 2, 0, 1, 9}) {
		t.Fatalf("country desc (ties in file order) = %v", got)
	}
	if !strings.Contains(d.frame(), "country ▼") {
		t.Fatal("no sort arrow")
	}
	d.keys("s")
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Fatalf("third s = %v, want file order", got)
	}
	d.keys("l", "s") // population: numeric via compareCells, not lexicographic
	if got := shownIDs(d.app()); !reflect.DeepEqual(got, []int{7, 5, 8, 4, 6, 9, 1, 3, 0, 2}) {
		t.Fatalf("population asc = %v", got)
	}
	d.keys("s", "s", "0")

	// Grouping by a column whose values are all unique still works.
	d.keys("g", "g", "g", "b")
	items := d.items()
	if d.app().Grid.GroupField != "c0" || len(items) != 20 || !items[0].Header || items[0].Count != 1 {
		t.Fatalf("unique grouping: %d items", len(items))
	}
	d.keys("}")
	if idx := d.app().Grid.SelectedIdx; !d.items()[idx].Header || idx == 0 {
		t.Fatalf("} landed on %d", idx)
	}
	d.keys("z", "a")
	if got := len(d.items()); got != 19 {
		t.Fatalf("z a: %d items, want 19", got)
	}
	d.keys("{")
	d.keys("g", "b")
	if d.app().Grid.GroupField != "" || len(d.items()) != 10 {
		t.Fatal("g b again should ungroup")
	}
	d.keys("l", "g", "b")
	var groups []string
	for _, it := range d.items() {
		if it.Header {
			groups = append(groups, it.Group)
		}
	}
	if !reflect.DeepEqual(groups, []string{"Japan", "Nigeria", "Peru", "Norway", "Switzerland", "United States"}) {
		t.Fatalf("groups = %v", groups)
	}
}

func TestCountsAndScrolling(t *testing.T) {
	var b strings.Builder
	b.WriteString("n,label\n")
	for i := range 100 {
		fmt.Fprintf(&b, "%d,row%02d\n", i, i)
	}
	path := writeFile(t, t.TempDir(), "many.csv", b.String())
	d := openApp(t, path, false, Config{})
	d.send(resize(120, 20))
	d.keys("l", "1", "0", "j")
	if a := d.app(); a.Grid.SelectedIdx != 10 || a.Col != 1 {
		t.Fatalf("10j: idx %d col %d (0 must join the count)", a.Grid.SelectedIdx, a.Col)
	}
	d.keys("0")
	if d.app().Col != 0 {
		t.Fatal("0 alone should go to the first column")
	}
	d.keys("5", "G")
	if d.app().Grid.SelectedIdx != 4 {
		t.Fatalf("5G: %d", d.app().Grid.SelectedIdx)
	}
	d.keys("g", "g", "ctrl+d")
	page := 20 - 3 - 2
	if d.app().Grid.SelectedIdx != page/2 || d.app().Grid.PageSize != page {
		t.Fatalf("ctrl+d: idx %d page %d", d.app().Grid.SelectedIdx, d.app().Grid.PageSize)
	}
	d.keys("ctrl+f", "ctrl+b", "ctrl+u")
	if d.app().Grid.SelectedIdx != 0 {
		t.Fatalf("ctrl+f ctrl+b ctrl+u: %d", d.app().Grid.SelectedIdx)
	}
	d.keys("G")
	f := d.frame()
	if !strings.Contains(f, "row99") || strings.Contains(f, "row00") {
		t.Fatal("G did not scroll the viewport to the last row")
	}
	if r := d.sh.State.Layout.Rects[nodeGrid]; r.H != 20-3 {
		t.Fatalf("grid height %d", r.H)
	}
	d.keys("g", "g")
	if !strings.Contains(d.frame(), "row00") {
		t.Fatal("g g did not scroll back")
	}
	d.send(tea.MouseWheelMsg{X: 3, Y: 5, Button: tea.MouseWheelDown})
	if d.app().Grid.SelectedIdx != 3 {
		t.Fatalf("wheel down: %d", d.app().Grid.SelectedIdx)
	}
}

func TestWideTableKeepsActiveColumnVisible(t *testing.T) {
	var b strings.Builder
	for j := range 30 {
		if j > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "col%02d", j)
	}
	b.WriteByte('\n')
	for i := range 5 {
		for j := range 30 {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "value-%02d-%02d-xxxx", i, j)
		}
		b.WriteByte('\n')
	}
	d := openApp(t, writeFile(t, t.TempDir(), "wide.csv", b.String()), false, Config{})
	if f := d.frame(); !strings.Contains(f, "▸col00") || strings.Contains(f, "col29") {
		t.Fatal("initial window")
	}
	d.keys("$")
	f := d.frame()
	if !strings.Contains(f, "▸col29") || strings.Contains(f, "col00") || d.app().ColOff == 0 {
		t.Fatalf("$ did not scroll to the last column:\n%s", f)
	}
	d.keys("h", "h")
	if !strings.Contains(d.frame(), "▸col27") {
		t.Fatal("h h")
	}
	d.keys("0")
	if !strings.Contains(d.frame(), "▸col00") {
		t.Fatal("0 did not scroll back")
	}
}

// --- editing ----------------------------------------------------------------------------

func TestFormEditUndoRedoSave(t *testing.T) {
	dir := t.TempDir()
	path := copyFile(t, "testdata/parts.csv", dir)
	d := openApp(t, path, false, Config{})
	d.keys("ctrl+e", "i")
	if d.modal() != modalForm {
		t.Fatal("i did not open the form")
	}
	rf := modalData(d.sh.State, modalForm).(rowForm)
	types := []core.ParamType{rf.M.Specs[0].Type, rf.M.Specs[1].Type, rf.M.Specs[2].Type}
	if !reflect.DeepEqual(types, []core.ParamType{core.ParamString, core.ParamString, core.ParamFloat}) {
		t.Fatalf("field types = %v", types)
	}
	if rf.M.Values["price"] != " 0.10" {
		t.Fatalf("raw value not kept: %q", rf.M.Values["price"])
	}
	d.keys("tab", "ctrl+u")
	d.typeText("Bolt XL")
	d.keys("tab", "ctrl+u")
	d.typeText("abc")
	d.keys("enter")
	if d.modal() != modalForm || !strings.Contains(d.app().Status, "price: must be a number") {
		t.Fatalf("invalid number accepted: modal=%q status=%q", d.modal(), d.app().Status)
	}
	d.keys("backspace", "backspace", "backspace")
	d.typeText("0.20")
	d.keys("enter")
	a := d.app()
	if d.modal() != "" || !reflect.DeepEqual(a.Doc.Rows[0], []string{"A1", "Bolt XL", "0.20"}) || !a.Doc.Dirty() {
		t.Fatalf("form edit: modal=%q row=%q", d.modal(), a.Doc.Rows[0])
	}

	d.keys("l", "t", "u")
	if got := d.app().Doc.Rows[0][1]; got != "BOLT XL" {
		t.Fatalf("t u = %q", got)
	}
	d.keys("u")
	if got := d.app().Doc.Rows[0][1]; got != "Bolt XL" {
		t.Fatalf("undo cell = %q", got)
	}
	d.keys("u")
	if a := d.app(); !reflect.DeepEqual(a.Doc.Rows[0], []string{"A1", "Bolt", " 0.10"}) || a.Doc.Dirty() {
		t.Fatalf("undo row: %q dirty=%v", a.Doc.Rows[0], a.Doc.Dirty())
	}
	d.keys("ctrl+r")
	if a := d.app(); !reflect.DeepEqual(a.Doc.Rows[0], []string{"A1", "Bolt XL", "0.20"}) || !a.Doc.Dirty() {
		t.Fatalf("redo row: %q", a.Doc.Rows[0])
	}
	d.keys("ctrl+s")
	if a := d.app(); a.Doc.Dirty() || !strings.Contains(a.Status, "saved") {
		t.Fatalf("save: dirty=%v status=%q", a.Doc.Dirty(), a.Status)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "sku;name;price\nA1;Bolt XL;0.20\nA2;Nut;0.05\nA3;Washer;1.25\n" {
		t.Fatalf("saved file:\n%s", got)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("temp file left behind: %v", es)
	}
	d.keys("u")
	if !d.app().Doc.Dirty() {
		t.Fatal("undo past the save should be dirty")
	}
	d.keys("ctrl+r")
	if d.app().Doc.Dirty() {
		t.Fatal("redo back to the saved state should be clean")
	}

	// o inserts a blank row below and focuses it; d d deletes it.
	d.keys("o")
	if a := d.app(); len(a.Doc.Rows) != 4 || !reflect.DeepEqual(a.Doc.Rows[1], []string{"", "", ""}) || a.Grid.SelectedIdx != 1 {
		t.Fatalf("o: %q idx %d", a.Doc.Rows, a.Grid.SelectedIdx)
	}
	d.keys("d", "d")
	if a := d.app(); len(a.Doc.Rows) != 3 || slices.Contains(a.Doc.IDs, 3) || !a.Doc.Dirty() {
		t.Fatalf("d d: %d rows dirty=%v", len(a.Doc.Rows), a.Doc.Dirty())
	}
	d.keys("u")
	if len(d.app().Doc.Rows) != 4 {
		t.Fatal("undo delete")
	}

	// A failed save leaves the file alone and the document dirty.
	if os.Geteuid() != 0 {
		os.Chmod(dir, 0o500)
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		d.keys("ctrl+s")
		if a := d.app(); !a.Doc.Dirty() || !strings.Contains(a.Status, "save failed") {
			t.Fatalf("failed save: dirty=%v status=%q", a.Doc.Dirty(), a.Status)
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(now, got) {
			t.Fatal("failed save touched the file")
		}
		os.Chmod(dir, 0o755)
	}
}

func TestSaveKeepsNoHeaderDelimiterAndBlankRows(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "data.psv", "ab|cd\nef|gh\n")
	d := openApp(t, path, true, Config{NoHeader: true})
	d.keys("t", "u", "ctrl+s")
	if got, _ := os.ReadFile(path); string(got) != "AB|cd\nef|gh\n" {
		t.Fatalf("no-header save:\n%q", got)
	}

	one := writeFile(t, dir, "names.csv", "name\nann\n")
	d = openApp(t, one, true, Config{})
	d.keys("o", "ctrl+s")
	got, _ := os.ReadFile(one)
	if string(got) != "name\nann\n\"\"\n" {
		t.Fatalf("blank single-column row:\n%q", got)
	}
	tab, err := ReadFile(one, Options{})
	if err != nil || len(tab.Rows) != 2 || tab.Rows[1][0] != "" {
		t.Fatalf("reread: %v %q", err, tab.Rows)
	}
}

// --- file picker ------------------------------------------------------------------------------

func TestPathpickerOpensAnotherCSV(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.csv", "x,y\n1,2\n")
	writeFile(t, dir, "b.tsv", "p\tq\n3\t4\n5\t6\n")
	writeFile(t, dir, "notes.txt", "not data\n")
	writeFile(t, dir, "sub/c.psv", "k|v\nz|9\n")
	d := openApp(t, a, false, Config{})

	d.keys("ctrl+o")
	if d.modal() != modalFiles {
		t.Fatal("ctrl+o did not open the picker")
	}
	pm := modalData(d.sh.State, modalFiles).(*pathpicker.Model)
	var names []string
	for _, e := range pm.Entries() {
		names = append(names, e.Name)
	}
	if pm.Dir != dir || !reflect.DeepEqual(names, []string{"sub", "a.csv", "b.tsv"}) {
		t.Fatalf("picker at %s lists %v", pm.Dir, names)
	}
	d.typeText("b.t")
	if pm := modalData(d.sh.State, modalFiles).(*pathpicker.Model); len(pm.Entries()) != 1 || pm.Entries()[0].Name != "b.tsv" {
		t.Fatalf("filtered picker: %v", pm.Entries())
	}
	if tree := newEnv(Config{}).view(d.sh.State); tree.Find("files.preview") == nil || !strings.Contains(d.frame(), "b.tsv") {
		t.Fatalf("no preview:\n%s", d.frame())
	}
	d.keys("enter")
	if app := d.app(); d.modal() != "" || filepath.Base(app.Doc.Path) != "b.tsv" || app.Doc.Delim != '\t' || len(app.Doc.Rows) != 2 {
		t.Fatalf("open b.tsv: modal=%q path=%s delim=%q", d.modal(), app.Doc.Path, app.Doc.Delim)
	}

	d.keys("ctrl+o")
	d.typeText("sub")
	d.keys("enter")
	if pm := modalData(d.sh.State, modalFiles).(*pathpicker.Model); pm.Dir != filepath.Join(dir, "sub") {
		t.Fatalf("did not drill into sub: %s", pm.Dir)
	}
	d.keys("enter")
	if filepath.Base(d.app().Doc.Path) != "c.psv" || d.app().Doc.Delim != '|' {
		t.Fatal("open sub/c.psv")
	}

	// Unsaved edits are not silently dropped by opening another file.
	d.keys("ctrl+e", "t", "u", "ctrl+o", "left")
	d.typeText("a.c")
	d.keys("enter")
	if filepath.Base(d.app().Doc.Path) != "c.psv" || d.modal() != modalFiles || !strings.Contains(d.app().Status, "enter again") {
		t.Fatalf("dirty open not confirmed: %s %q", d.app().Doc.Path, d.app().Status)
	}
	d.keys("enter")
	if app := d.app(); filepath.Base(app.Doc.Path) != "a.csv" || !app.Edit || app.Doc.Dirty() {
		t.Fatalf("second enter: %s edit=%v", app.Doc.Path, app.Edit)
	}
	d.keys("ctrl+o", "esc")
	if d.modal() != "" {
		t.Fatal("esc did not close the picker")
	}
}

// --- detail split --------------------------------------------------------------------------------

func TestDetailSplitToggleResize(t *testing.T) {
	d := openApp(t, "testdata/cities.csv", false, Config{})
	d.keys("enter")
	f := d.frame()
	if !d.app().Detail || !strings.Contains(f, "Row 1 of 10") || !strings.Contains(f, "notes: Largest metropolitan") {
		t.Fatalf("detail:\n%s", f)
	}
	rects := func() (grid, det [4]int) {
		g, dt := d.sh.State.Layout.Rects[nodeGrid], d.sh.State.Layout.Rects["detail"]
		return [4]int{g.X, g.Y, g.W, g.H}, [4]int{dt.X, dt.Y, dt.W, dt.H}
	}
	g0, det := rects()
	if det[0] <= g0[0] || det[1] != g0[1] {
		t.Fatalf("not side by side: grid %v detail %v", g0, det)
	}
	a := d.app()
	if geo := newEnv(Config{}).geometry(&a, 120, 32); g0[2] != geo.tableW || g0[3] != geo.tableH || !geo.side {
		t.Fatalf("geometry mismatch: %v vs %+v", g0, geo)
	}
	d.keys("ctrl+w", ">")
	g1, _ := rects()
	if d.app().Ratio <= 0.6 || g1[2] <= g0[2] {
		t.Fatalf("ctrl+w >: ratio %v width %d -> %d", d.app().Ratio, g0[2], g1[2])
	}
	d.keys("ctrl+w", "<", "ctrl+w", "<")
	if r := d.app().Ratio; r >= 0.6 {
		t.Fatalf("ctrl+w < <: %v", r)
	}
	d.keys("ctrl+w", "=")
	if d.app().Ratio != 0.6 {
		t.Fatalf("ctrl+w =: %v", d.app().Ratio)
	}

	d.send(resize(80, 30))
	g2, det2 := rects()
	if det2[1] <= g2[1] || det2[0] != g2[0] {
		t.Fatalf("narrow terminal should stack: grid %v detail %v", g2, det2)
	}
	a = d.app()
	if geo := newEnv(Config{}).geometry(&a, 80, 30); g2[3] != geo.tableH || geo.side {
		t.Fatalf("stacked geometry %v vs %+v", g2, geo)
	}
	d.keys("G")
	if !strings.Contains(d.frame(), "Row 10 of 10") {
		t.Fatal("detail does not follow the cursor")
	}
	d.keys("enter")
	if d.app().Detail || strings.Contains(d.frame(), "Row 10 of 10") {
		t.Fatal("enter did not close the detail")
	}
}

// --- help and palette ------------------------------------------------------------------------------

func TestHelpAndPaletteModals(t *testing.T) {
	cfgDir := t.TempDir()
	writeFile(t, cfgDir, "bindings.json", `// user overrides (JSONC)
[
  {"mode": "normal", "keys": ["F"], "command": "filter.all", "hint": "filter"},
  {"mode": "normal", "keys": ["s"], "unbind": true}, // no sorting on s
]`)
	writeFile(t, cfgDir, "settings.json", `{"matchMode": "fuzzy", /* comment */ "edit": true}`)
	writeFile(t, cfgDir, "themes.json", `{"roles": {"accent": "#ff00ff"}}`)
	cfg, set := loadConfig(cfgDir)
	if len(cfg.Notes) != 0 || !set.Bool("edit", false) || cfg.Theme.Roles["accent"] != "#ff00ff" {
		t.Fatalf("config: %v", cfg.Notes)
	}
	if empty, _ := loadConfig(filepath.Join(cfgDir, "missing")); len(empty.Notes) != 0 || len(empty.Bindings) != 0 {
		t.Fatal("a missing config dir should load quietly")
	}
	d := openApp(t, "testdata/cities.csv", false, cfg)
	if d.app().Match != table.MatchFuzzy {
		t.Fatal("matchMode setting ignored")
	}

	d.keys("?")
	if d.modal() != modalHelp {
		t.Fatal("? did not open help")
	}
	f := d.frame()
	if !strings.Contains(f, "Help · active key bindings") || !strings.Contains(f, "(filter.all)") {
		t.Fatalf("help frame:\n%s", f)
	}
	lines := newEnv(cfg).helpLines()
	has := func(keys, cmd string) bool {
		return slices.ContainsFunc(lines, func(l string) bool {
			return strings.HasPrefix(l, fmt.Sprintf("  %-12s ", keys)) && strings.HasSuffix(l, "("+cmd+")")
		})
	}
	if !has("F", "filter.all") || !has("/", "filter.all") || has("s", "sort.cycle") || !has("g b", "group.toggle") || !has("ctrl+w >", "split.grow") {
		t.Fatalf("help does not follow the live keymap:\n%s", strings.Join(lines, "\n"))
	}
	d.keys("j")
	if off, _ := modalData(d.sh.State, modalHelp).(int); off != 1 {
		t.Fatalf("help scroll: %d", off)
	}
	d.keys("esc", "s")
	if d.modal() != "" || d.app().SortCol != -1 {
		t.Fatal("unbound s still sorts")
	}
	d.keys("F")
	if !d.app().Typing {
		t.Fatal("user binding F did not open the filter")
	}
	d.keys("esc")

	d.keys(":")
	pm := modalData(d.sh.State, modalPalette).(*picker.Model)
	users := 0
	for _, s := range commandSpecs() {
		if s.HasTag("user") {
			users++
			if !slices.ContainsFunc(pm.Items, func(it picker.Item) bool { return it.ID == s.ID }) {
				t.Fatalf("palette misses %s", s.ID)
			}
		}
	}
	if len(pm.Items) <= users {
		t.Fatal("palette should also list motions")
	}
	d.keys("tab") // fuzzy -> regex
	if pm := modalData(d.sh.State, modalPalette).(*picker.Model); pm.MatchMode != picker.MatchRegex {
		t.Fatalf("palette mode %s", pm.MatchMode)
	}
	d.typeText("^sort")
	pm = modalData(d.sh.State, modalPalette).(*picker.Model)
	if f := pm.Filtered(); len(f) != 1 || f[0].ID != "sort.cycle" {
		t.Fatalf("palette filter: %v", f)
	}
	d.keys("enter")
	if d.modal() != "" || d.app().SortCol != 0 {
		t.Fatalf("palette did not run sort.cycle: modal=%q sort=%d", d.modal(), d.app().SortCol)
	}
	d.keys(":", "esc")
	if d.modal() != "" {
		t.Fatal("esc did not close the palette")
	}
}

func TestYankCell(t *testing.T) {
	c := newClip()
	d := openApp(t, "testdata/cities.csv", false, Config{Copy: c.copy})
	d.keys("j", "$", "y")
	if d.app().Register != "Known for street food" || !strings.Contains(d.app().Status, "clipboard") {
		t.Fatalf("y: %q %q", d.app().Register, d.app().Status)
	}
	d.keys("g", "g", "Y")
	want := `Tokyo,Japan,13960000,2194,1457,true,"Largest metropolitan area in the world, home to the Imperial Palace"`
	if got := c.all(); len(got) != 2 || got[1] != want {
		t.Fatalf("clipboard = %q", got)
	}
	d = openApp(t, "testdata/cities.csv", false, Config{})
	d.keys("y")
	if d.app().Register != "Tokyo" || !strings.Contains(d.app().Status, "register") {
		t.Fatalf("no clipboard: %q", d.app().Status)
	}
}

// --- CLI ----------------------------------------------------------------------------------------------

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCLIShowColumnsStats(t *testing.T) {
	code, out, errs := runCLI("show", "testdata/cities.csv", "--where", "country~japan", "--sort", "population", "--desc", "--limit", "2", "--format", "json")
	if code != 0 {
		t.Fatalf("show json: %d %s", code, errs)
	}
	var res struct {
		Columns        []colInfo
		Rows           []map[string]string
		Total, Matched int
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 || res.Rows[0]["city"] != "Tokyo" || res.Rows[1]["city"] != "Osaka" || res.Matched != 3 || res.Total != 10 || len(res.Columns) != 7 {
		t.Fatalf("show json: %+v", res)
	}

	dup := writeFile(t, t.TempDir(), "dup.csv", "a,a,\n1,2,3\n")
	_, out, _ = runCLI("show", dup, "--format", "json")
	if i, j, k := strings.Index(out, `"a": "1"`), strings.Index(out, `"a#2": "2"`), strings.Index(out, `"C": "3"`); i < 0 || i > j || j > k {
		t.Fatalf("duplicate headers (keys in column order):\n%s", out)
	}
	res.Rows, res.Columns = nil, nil
	json.Unmarshal([]byte(out), &res)
	if !reflect.DeepEqual(res.Rows[0], map[string]string{"a": "1", "a#2": "2", "C": "3"}) || res.Columns[1].Key != "a#2" || res.Columns[1].Name != "a" {
		t.Fatalf("duplicate keys: %+v", res)
	}

	code, out, _ = runCLI("show", "testdata/cities.csv", "--search", "JAPAN")
	if code != 0 || !strings.Contains(out, "Kyoto") || strings.Contains(out, "Lagos") || !strings.Contains(out, "(3 of 10 rows)") {
		t.Fatalf("show table:\n%s", out)
	}
	_, out, _ = runCLI("show", "testdata/scores.tsv", "--sort", "score", "--format", "markdown")
	if !strings.HasPrefix(out, "| id | name | score |\n| --- | --- | --- |\n| 2 | bo | 7 |") {
		t.Fatalf("markdown:\n%s", out)
	}
	_, out, _ = runCLI("show", "testdata/hosts.psv", "--no-header", "--where", "A~alpha", "--format", "json")
	if !strings.Contains(out, `"A": "alpha"`) {
		t.Fatalf("no-header show:\n%s", out)
	}

	code, out, _ = runCLI("columns", "testdata/cities.csv", "--format", "json")
	var cols []colInfo
	if code != 0 || json.Unmarshal([]byte(out), &cols) != nil || len(cols) != 7 || cols[2].Type != "int" || cols[2].Letter != "C" {
		t.Fatalf("columns json:\n%s", out)
	}
	_, out, _ = runCLI("columns", "testdata/parts.csv")
	if !strings.Contains(out, "price  float") {
		t.Fatalf("columns table:\n%s", out)
	}
	code, out, _ = runCLI("stats", "testdata/cities.csv", "--format", "json")
	var st Stats
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st.Rows != 10 || st.Each[2].Max != "15388000" {
		t.Fatalf("stats json:\n%s", out)
	}
	_, out, _ = runCLI("stats", "testdata/cities.csv", "--format", "markdown")
	if !strings.Contains(out, "| C | population | int |") {
		t.Fatalf("stats markdown:\n%s", out)
	}

	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"show", "--format", "json", "testdata/cities.csv"}, 2, "FILE comes first"},
		{[]string{"show"}, 2, "FILE comes first"},
		{[]string{"show", "testdata/cities.csv", "--sort", "nope"}, 2, `unknown column "nope"`},
		{[]string{"show", "testdata/cities.csv", "--where", "city"}, 2, "COL~TEXT"},
		{[]string{"show", "testdata/cities.csv", "--format", "xml"}, 2, "json, table or markdown"},
		{[]string{"columns", "testdata/cities.csv", "--where", "a~b"}, 2, "not defined"},
		{[]string{"stats", "testdata/cities.csv", "extra"}, 2, "unexpected argument"},
		{[]string{"show", "testdata/missing.csv"}, 1, "no such file"},
		// Only an exact first word dispatches: these open a file named show.
		{[]string{"--", "show"}, 1, "open show"},
		{[]string{"./show"}, 1, "open ./show"},
	} {
		code, _, errs := runCLI(c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%q: code %d, stderr %q", c.args, code, errs)
		}
	}
}

func TestSmoke(t *testing.T) {
	code, out, errs := runCLI("--smoke")
	if code != 0 || !strings.Contains(out, "smoke: 19/19 checks passed") {
		t.Fatalf("smoke: %d %s\n%s", code, errs, out)
	}
	lines := strings.Split(out, "\n")
	first := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "ok   ") || strings.HasPrefix(l, "FAIL ") })
	if first != 33 || !strings.Contains(lines[0], "o-csv · sample.csv") {
		t.Fatalf("smoke frame should be 32 lines then a blank, checks at line %d:\n%s", first, out)
	}
	if code, out, _ := runCLI("--smoke", "testdata/scores.tsv"); code != 0 || !strings.Contains(out, "scores.tsv") {
		t.Fatalf("smoke with a file: %d\n%s", code, out)
	}
	zero := writeFile(t, t.TempDir(), "zero.csv", "a,b\n")
	if code, out, _ := runCLI("--smoke", zero); code != 0 || !strings.Contains(out, "19/19 checks passed") || !strings.Contains(out, "0 rows × 2 cols") {
		t.Fatalf("smoke with zero rows: %d\n%s", code, out)
	}
}

func TestDetailScrollReachesEndAndResets(t *testing.T) {
	const marker = "ENDMARK"
	long := strings.Repeat("lorem ipsum dolor ", 200) + marker
	path := writeFile(t, t.TempDir(), "long.csv", "id,notes\n1,"+long+"\n2,short\n")
	d := openApp(t, path, false, Config{})
	d.keys("enter")
	if strings.Contains(d.frame(), marker) {
		t.Fatal("the end marker is visible before scrolling")
	}
	for i := 0; i < 400 && !strings.Contains(d.frame(), marker); i++ {
		d.keys("ctrl+w", "j")
	}
	if !strings.Contains(d.frame(), marker) || d.app().DetailOff == 0 {
		t.Fatalf("ctrl+w j never reached the end marker (offset %d)", d.app().DetailOff)
	}
	for range 5 {
		d.keys("ctrl+w", "j")
	}
	settled := d.app().DetailOff
	for range 20 {
		d.keys("ctrl+w", "j")
	}
	if d.app().DetailOff != settled || !strings.Contains(d.frame(), marker) {
		t.Fatalf("repeated ctrl+w j at the bottom overshoots: %d -> %d", settled, d.app().DetailOff)
	}
	d.keys("ctrl+w", "k")
	if d.app().DetailOff != settled-1 {
		t.Fatalf("one ctrl+w k should step one line: %d -> %d", settled, d.app().DetailOff)
	}
	for range 500 {
		d.keys("ctrl+w", "k")
	}
	if d.app().DetailOff != 0 || strings.Contains(d.frame(), marker) {
		t.Fatalf("ctrl+w k did not return to the top (offset %d)", d.app().DetailOff)
	}
	d.keys("ctrl+w", "j", "ctrl+w", "j", "ctrl+w", "j")
	if d.app().DetailOff != 3 {
		t.Fatalf("three ctrl+w j = offset %d", d.app().DetailOff)
	}
	d.keys("j")
	if d.app().DetailOff != 0 || !strings.Contains(d.frame(), "Row 2 of 2") {
		t.Fatalf("moving the row did not reset the detail scroll (offset %d)", d.app().DetailOff)
	}
}

func TestTallFormKeepsActiveFieldVisible(t *testing.T) {
	var head, vals []string
	for i := range 60 {
		head = append(head, fmt.Sprintf("col%02d", i))
		vals = append(vals, strconv.Itoa(i))
	}
	path := writeFile(t, t.TempDir(), "wide.csv", strings.Join(head, ",")+"\n"+strings.Join(vals, ",")+"\n")
	d := openApp(t, path, true, Config{})
	d.keys("i")
	if d.modal() != modalForm || !strings.Contains(d.frame(), "col00") {
		t.Fatalf("form did not open:\n%s", d.frame())
	}
	for range 45 {
		d.keys("tab")
	}
	f := d.frame()
	if got := modalData(d.sh.State, modalForm).(rowForm).M.ActiveIdx; got != 45 || !strings.Contains(f, "▸ col45") || !strings.Contains(f, "· fields ") {
		t.Fatalf("active field %d not visible:\n%s", got, f)
	}
	// The grid behind the overlay still shows col00, so check the form node.
	tree := newEnv(Config{}).view(d.sh.State)
	node := tree.Find(modalForm)
	if node == nil {
		t.Fatalf("no form node:\n%s", f)
	}
	shown, _ := node.Props["fields"].([]core.ParamSpec)
	if len(shown) == 0 || len(shown) >= 60 || shown[0].Name == "col00" || !slices.ContainsFunc(shown, func(p core.ParamSpec) bool { return p.Name == "col45" }) {
		t.Fatalf("form window shows %d fields, first %v", len(shown), shown)
	}
}
