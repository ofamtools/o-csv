package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
)

// Doc is one open file: its rows in file order, a stable source ID per row,
// and the undo history. A Doc is a value; every change returns a new Doc
// and never writes into slices an older Doc still holds.
type Doc struct {
	Path     string // "" when read from stdin
	Name     string // display name
	Delim    rune
	NoHeader bool
	Header   []string
	Rows     [][]string
	IDs      []int // IDs[i] is the source ID of Rows[i]

	nextID   int
	hist     []patch
	pos      int // patches applied
	savedPos int // pos at the last load or save; -1 when unreachable
}

// patch is one undoable change. Cell patches carry one value in Before and
// After; row patches carry whole rows; insert and delete carry the row and
// its file position.
type patch struct {
	Op     string // "cell", "row", "insert", "delete"
	ID     int
	Pos    int
	Col    int
	Before []string
	After  []string
}

// newDoc wraps a parsed table.
func newDoc(path string, t *Table, noHeader bool) Doc {
	d := Doc{Path: path, Name: path, Delim: t.Delim, NoHeader: noHeader, Header: t.Header, Rows: t.Rows}
	if path == "-" || path == "" {
		d.Path, d.Name = "", "stdin"
	}
	if len(d.Header) == 0 {
		d.Header = []string{"A"}
	}
	d.IDs = make([]int, len(d.Rows))
	for i := range d.Rows {
		d.Rows[i] = padTo(d.Rows[i], len(d.Header))
		d.IDs[i] = i
	}
	d.nextID = len(d.Rows)
	return d
}

// emptyDoc is the placeholder shown before any file is opened.
func emptyDoc(noHeader bool) Doc {
	d := newDoc("", &Table{Delim: ','}, noHeader)
	d.Name = "no file"
	return d
}

// Dirty reports unsaved changes.
func (d Doc) Dirty() bool { return d.pos != d.savedPos }

// CanUndo and CanRedo report history availability.
func (d Doc) CanUndo() bool { return d.pos > 0 }

// CanRedo reports whether a redo is available.
func (d Doc) CanRedo() bool { return d.pos < len(d.hist) }

// index returns the file position of the row with source ID id.
func (d Doc) index(id int) int { return slices.Index(d.IDs, id) }

// Row returns the row with source ID id.
func (d Doc) Row(id int) ([]string, bool) {
	if i := d.index(id); i >= 0 {
		return d.Rows[i], true
	}
	return nil, false
}

func (d Doc) apply(p patch, forward bool) Doc {
	op, vals := p.Op, p.After
	if !forward {
		vals = p.Before
		switch op {
		case "insert":
			op = "delete"
		case "delete":
			op, vals = "insert", p.Before
		}
	}
	switch op {
	case "cell", "row":
		i := d.index(p.ID)
		if i < 0 {
			return d
		}
		row := slices.Clone(d.Rows[i])
		if op == "cell" {
			row[p.Col] = vals[0]
		} else {
			row = slices.Clone(vals)
		}
		d.Rows = slices.Clone(d.Rows)
		d.Rows[i] = row
	case "insert":
		at := min(max(p.Pos, 0), len(d.Rows))
		d.Rows = slices.Insert(slices.Clone(d.Rows), at, slices.Clone(vals))
		d.IDs = slices.Insert(slices.Clone(d.IDs), at, p.ID)
	case "delete":
		i := d.index(p.ID)
		if i < 0 {
			return d
		}
		d.Rows = slices.Delete(slices.Clone(d.Rows), i, i+1)
		d.IDs = slices.Delete(slices.Clone(d.IDs), i, i+1)
	}
	return d
}

// commit applies p as a new history step, dropping any redo tail.
func (d Doc) commit(p patch) Doc {
	if d.savedPos > d.pos {
		d.savedPos = -1 // the saved state was on the discarded redo branch
	}
	d.hist = append(slices.Clip(d.hist[:d.pos]), p)
	d.pos++
	return d.apply(p, true)
}

// Undo steps back one change.
func (d Doc) Undo() (Doc, patch, bool) {
	if !d.CanUndo() {
		return d, patch{}, false
	}
	d.pos--
	p := d.hist[d.pos]
	return d.apply(p, false), p, true
}

// Redo re-applies the next change.
func (d Doc) Redo() (Doc, patch, bool) {
	if !d.CanRedo() {
		return d, patch{}, false
	}
	p := d.hist[d.pos]
	d.pos++
	return d.apply(p, true), p, true
}

// SetCell changes one value.
func (d Doc) SetCell(id, col int, v string) (Doc, bool) {
	row, ok := d.Row(id)
	if !ok || col < 0 || col >= len(row) || row[col] == v {
		return d, false
	}
	return d.commit(patch{Op: "cell", ID: id, Col: col, Before: []string{row[col]}, After: []string{v}}), true
}

// SetRow replaces a whole row.
func (d Doc) SetRow(id int, vals []string) (Doc, bool) {
	row, ok := d.Row(id)
	if !ok || slices.Equal(row, vals) {
		return d, false
	}
	return d.commit(patch{Op: "row", ID: id, Before: slices.Clone(row), After: slices.Clone(vals)}), true
}

// InsertBlank inserts an empty row at file position at and returns its ID.
func (d Doc) InsertBlank(at int) (Doc, int) {
	id := d.nextID
	d.nextID++
	return d.commit(patch{Op: "insert", ID: id, Pos: at, After: make([]string, len(d.Header))}), id
}

// Delete removes the row with source ID id.
func (d Doc) Delete(id int) (Doc, bool) {
	i := d.index(id)
	if i < 0 {
		return d, false
	}
	return d.commit(patch{Op: "delete", ID: id, Pos: i, Before: slices.Clone(d.Rows[i])}), true
}

// Saved marks history position pos as what is on disk.
func (d Doc) Saved(pos int) Doc { d.savedPos = pos; return d }

// Table returns the document as a Table for Summarize and writing.
func (d Doc) Table() *Table { return &Table{Header: d.Header, Rows: d.Rows, Delim: d.Delim} }

// describe names a patch for status lines.
func (p patch) describe() string {
	switch p.Op {
	case "cell":
		return "cell edit"
	case "row":
		return "row edit"
	case "insert":
		return "row insert"
	}
	return "row delete"
}

// --- writing ----------------------------------------------------------------

// saveFile writes atomically: a temp file next to the target, synced, then
// renamed over it. A failure leaves the original untouched. Symlinks are
// followed so the link survives; the original file mode is kept.
func saveFile(path string, header []string, rows [][]string, delim rune, noHeader bool) (err error) {
	target := path
	if p, e := filepath.EvalSymlinks(path); e == nil {
		target = p
	}
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if info, e := os.Stat(target); e == nil {
		if err = f.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	}
	if err = Write(f, &Table{Header: header, Rows: rows, Delim: delim}, noHeader); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// --- cell transforms ---------------------------------------------------------

func titleCase(s string) string {
	rs := []rune(strings.ToLower(s))
	start := true
	for i, r := range rs {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			if start {
				rs[i] = unicode.ToTitle(r)
			}
			start = false
		} else {
			start = true
		}
	}
	return string(rs)
}

var transforms = map[string]func(string) string{
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"title": titleCase,
}

// --- row form -----------------------------------------------------------------

// rowForm is the edit form modal's data: the form model plus which column
// each field edits and which row it targets.
type rowForm struct {
	M    *form.Model
	Cols []int
	ID   int
}

func (f rowForm) clone() rowForm {
	m := *f.M
	m.Values = maps.Clone(m.Values)
	m.Errors = maps.Clone(m.Errors)
	f.M = &m
	return f
}

// fieldSpecs derives one form field per column from Summarize's types: int
// and float columns validate as numbers, every other type (bool, date,
// mixed, empty) stays free text so no existing value is rewritten. Fields
// are never required, so blank cells stay blank. Field names are unique
// even when headers repeat or are empty.
func fieldSpecs(d Doc) []core.ParamSpec {
	st := Summarize(d.Table())
	used := map[string]bool{}
	specs := make([]core.ParamSpec, len(d.Header))
	for j, h := range d.Header {
		name := strings.TrimSpace(h)
		if name == "" {
			name = ColumnLetter(j)
		}
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)", strings.TrimSpace(h), n)
			if strings.TrimSpace(h) == "" {
				name = fmt.Sprintf("%s (%d)", ColumnLetter(j), n)
			}
		}
		used[name] = true
		typ, desc := core.ParamString, "text"
		if j < len(st.Each) {
			switch st.Each[j].Type {
			case TypeInt:
				typ, desc = core.ParamInt, "integer"
			case TypeFloat:
				typ, desc = core.ParamFloat, "number"
			case TypeEmpty:
				desc = "text (empty column)"
			default:
				desc = st.Each[j].Type
			}
		}
		specs[j] = core.ParamSpec{Name: name, Type: typ, Description: desc}
	}
	return specs
}

// newRowForm builds the form for row id.
func newRowForm(d Doc, id int) (rowForm, bool) {
	row, ok := d.Row(id)
	if !ok {
		return rowForm{}, false
	}
	specs := fieldSpecs(d)
	init := map[string]any{}
	cols := make([]int, len(specs))
	for j, s := range specs {
		init[s.Name] = row[j]
		cols[j] = j
	}
	m := form.New(fmt.Sprintf("Edit row %d", d.index(id)+1), specs, init)
	m.SubmitLabel = "Apply (enter) · esc cancels"
	return rowForm{M: m, Cols: cols, ID: id}, true
}

// values returns the row the form describes: the raw typed text, untrimmed.
func (f rowForm) values(base []string) []string {
	out := slices.Clone(base)
	for i, s := range f.M.Specs {
		if v, ok := f.M.Values[s.Name].(string); ok && f.Cols[i] < len(out) {
			out[f.Cols[i]] = v
		}
	}
	return out
}
