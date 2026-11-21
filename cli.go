package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke/widgets/table"
)

var subUsage = map[string]string{
	"show":    "o-csv show FILE [--where COL~TEXT]... [--search TEXT] [--sort COL] [--desc] [--limit N] [--format json|table|markdown] [--delim D] [--no-header]",
	"columns": "o-csv columns FILE [--format json|table|markdown] [--delim D] [--no-header]",
	"stats":   "o-csv stats FILE [--format json|table|markdown] [--delim D] [--no-header]",
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// subcommand runs show, columns or stats. FILE comes first ("-" is stdin);
// flags follow it.
func subcommand(name string, args []string, stdout, stderr io.Writer) int {
	usage := subUsage[name]
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
		fmt.Fprintln(stdout, "usage: "+usage)
		return 0
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && args[0] != "-" {
		fmt.Fprintf(stderr, "o-csv %s: FILE comes first, flags after it\nusage: %s\n", name, usage)
		return 2
	}
	file := args[0]
	fs := flag.NewFlagSet("o-csv "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: "+usage) }
	format := fs.String("format", "table", "output format: json, table or markdown")
	delim := fs.String("delim", "", "delimiter: comma, tab, semicolon, pipe or one character (default: detect)")
	noHeader := fs.Bool("no-header", false, "the first row is data; columns are named A, B, C, ...")
	var where multiFlag
	var search, sortCol *string
	var desc *bool
	var limit *int
	if name == "show" {
		fs.Var(&where, "where", "COL~TEXT: keep rows whose COL contains TEXT (case-insensitive; repeatable)")
		search = fs.String("search", "", "keep rows where any cell contains TEXT (case-insensitive)")
		sortCol = fs.String("sort", "", "sort by COL (numbers numerically, empty cells last)")
		desc = fs.Bool("desc", false, "sort descending")
		limit = fs.Int("limit", 0, "print at most N rows (0 = all)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "o-csv %s: unexpected argument %q (only one FILE)\nusage: %s\n", name, fs.Arg(0), usage)
		return 2
	}
	switch *format {
	case "json", "table", "markdown":
	default:
		fmt.Fprintf(stderr, "o-csv %s: --format %q: use json, table or markdown\n", name, *format)
		return 2
	}
	d, err := ParseDelim(*delim)
	if err != nil {
		fmt.Fprintf(stderr, "o-csv %s: %v\n", name, err)
		return 2
	}
	t, err := ReadFile(file, Options{Delim: d, NoHeader: *noHeader})
	if err != nil {
		fmt.Fprintf(stderr, "o-csv %s: %v\n", name, err)
		return 1
	}
	var out bytes.Buffer
	switch name {
	case "show":
		err = show(&out, t, showOpts{where: where, search: *search, sort: *sortCol, desc: *desc, limit: *limit, format: *format})
	case "columns":
		err = columns(&out, t, *format)
	case "stats":
		err = stats(&out, t, *format)
	}
	if err != nil {
		fmt.Fprintf(stderr, "o-csv %s: %v\n", name, err)
		return 2
	}
	stdout.Write(out.Bytes())
	return 0
}

// resolveCol finds a column by exact name (the first of duplicates), then
// case-insensitive name, then spreadsheet letter (A, B, ...), then 1-based
// number.
func resolveCol(t *Table, name string) (int, error) {
	if i := slices.Index(t.Header, name); i >= 0 {
		return i, nil
	}
	for i, h := range t.Header {
		if strings.EqualFold(h, name) {
			return i, nil
		}
	}
	for i := range t.Header {
		if ColumnLetter(i) == strings.ToUpper(name) {
			return i, nil
		}
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 1 && n <= len(t.Header) {
		return n - 1, nil
	}
	return 0, fmt.Errorf("unknown column %q (columns: %s)", name, strings.Join(t.Header, ", "))
}

type showOpts struct {
	where        []string
	search, sort string
	desc         bool
	limit        int
	format       string
}

func show(w io.Writer, t *Table, o showOpts) error {
	type cond struct {
		col  int
		text string
	}
	var conds []cond
	for _, wh := range o.where {
		name, txt, ok := strings.Cut(wh, "~")
		if !ok {
			return fmt.Errorf("--where %q: use COL~TEXT", wh)
		}
		j, err := resolveCol(t, name)
		if err != nil {
			return fmt.Errorf("--where: %w", err)
		}
		conds = append(conds, cond{j, strings.ToLower(txt)})
	}
	search := strings.ToLower(o.search)
	var rows [][]string
	for _, r := range t.Rows {
		ok := true
		for _, c := range conds {
			ok = ok && strings.Contains(strings.ToLower(r[c.col]), c.text)
		}
		if ok && search != "" {
			ok = slices.ContainsFunc(r, func(v string) bool { return strings.Contains(strings.ToLower(v), search) })
		}
		if ok {
			rows = append(rows, r)
		}
	}
	if o.sort != "" {
		j, err := resolveCol(t, o.sort)
		if err != nil {
			return fmt.Errorf("--sort: %w", err)
		}
		slices.SortStableFunc(rows, func(a, b []string) int {
			if o.desc {
				return compareCells(b[j], a[j])
			}
			return compareCells(a[j], b[j])
		})
	} else if o.desc {
		return errors.New("--desc needs --sort COL")
	}
	if o.limit < 0 {
		return errors.New("--limit must be 0 or more")
	}
	matched := len(rows)
	if o.limit > 0 && len(rows) > o.limit {
		rows = rows[:o.limit]
	}
	switch o.format {
	case "json":
		keys := jsonKeys(t.Header)
		objs := make([]orderedRow, len(rows))
		for i, r := range rows {
			objs[i] = orderedRow{keys, r}
		}
		return writeJSON(w, map[string]any{"columns": columnInfo(t, keys), "rows": objs, "total": len(t.Rows), "matched": matched})
	case "markdown":
		writeMarkdown(w, t.Header, rows)
	default:
		writeTable(w, t.Header, rows)
		fmt.Fprintf(w, "(%d of %d rows", len(rows), len(t.Rows))
		if matched > len(rows) {
			fmt.Fprintf(w, ", %d matched", matched)
		}
		fmt.Fprintln(w, ")")
	}
	return nil
}

// jsonKeys gives every column a unique JSON key: its name, the column
// letter when the name is empty, and "#2", "#3", ... after a repeat.
func jsonKeys(header []string) []string {
	used := map[string]bool{}
	keys := make([]string, len(header))
	for i, h := range header {
		base := h
		if base == "" {
			base = ColumnLetter(i)
		}
		k := base
		for n := 2; used[k]; n++ {
			k = base + "#" + strconv.Itoa(n)
		}
		used[k] = true
		keys[i] = k
	}
	return keys
}

type colInfo struct {
	Index  int    `json:"index"`
	Letter string `json:"letter"`
	Name   string `json:"name"`
	Key    string `json:"key"`
	Type   string `json:"type,omitempty"`
}

func columnInfo(t *Table, keys []string) []colInfo {
	out := make([]colInfo, len(t.Header))
	for i, h := range t.Header {
		out[i] = colInfo{Index: i, Letter: ColumnLetter(i), Name: h, Key: keys[i]}
	}
	return out
}

// orderedRow marshals as an object with keys in column order.
type orderedRow struct {
	keys []string
	vals []string
}

func (r orderedRow) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range r.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(r.vals[i])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func columns(w io.Writer, t *Table, format string) error {
	st := Summarize(t)
	keys := jsonKeys(t.Header)
	info := columnInfo(t, keys)
	for i := range info {
		info[i].Type = st.Each[i].Type
	}
	if format == "json" {
		return writeJSON(w, info)
	}
	rows := make([][]string, len(info))
	for i, c := range info {
		rows[i] = []string{c.Letter, c.Name, c.Type}
	}
	if format == "markdown" {
		writeMarkdown(w, []string{"col", "name", "type"}, rows)
	} else {
		writeTable(w, []string{"col", "name", "type"}, rows)
	}
	return nil
}

func stats(w io.Writer, t *Table, format string) error {
	st := Summarize(t)
	if format == "json" {
		return writeJSON(w, st)
	}
	head := []string{"col", "name", "type", "empty", "distinct", "min", "max", "width"}
	rows := make([][]string, len(st.Each))
	for i, c := range st.Each {
		rows[i] = []string{ColumnLetter(c.Index), c.Name, c.Type, strconv.Itoa(c.Empty), strconv.Itoa(c.Distinct), c.Min, c.Max, strconv.Itoa(c.Width)}
	}
	if format == "markdown" {
		fmt.Fprintf(w, "**%d rows · %d columns · %s**\n\n", st.Rows, st.Columns, st.Delim)
		writeMarkdown(w, head, rows)
	} else {
		fmt.Fprintf(w, "%d rows · %d columns · %s\n\n", st.Rows, st.Columns, st.Delim)
		writeTable(w, head, rows)
	}
	return nil
}

// cellText is a cell on one line, cut to 40 characters.
func cellText(s string) string { return clip(oneLine(s), 40) }

func writeTable(w io.Writer, head []string, rows [][]string) {
	widths := make([]int, len(head))
	for i, h := range head {
		widths[i] = textW(cellText(h))
	}
	for _, r := range rows {
		for i := range head {
			widths[i] = max(widths[i], textW(cellText(r[i])))
		}
	}
	line := func(cells []string) {
		var b strings.Builder
		for i := range head {
			c := cellText(cells[i])
			if i < len(head)-1 {
				c += strings.Repeat(" ", widths[i]-textW(c)+2)
			}
			b.WriteString(c)
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	line(head)
	sep := make([]string, len(head))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	line(sep)
	for _, r := range rows {
		line(r)
	}
}

func writeMarkdown(w io.Writer, head []string, rows [][]string) {
	esc := strings.NewReplacer("|", `\|`, "\r\n", "<br>", "\n", "<br>", "\r", "<br>")
	line := func(cells []string) {
		parts := make([]string, len(head))
		for i := range head {
			parts[i] = esc.Replace(cells[i])
		}
		fmt.Fprintln(w, "| "+strings.Join(parts, " | ")+" |")
	}
	line(head)
	sep := make([]string, len(head))
	for i := range sep {
		sep[i] = "---"
	}
	line(sep)
	for _, r := range rows {
		line(r)
	}
}

// --- smoke ---------------------------------------------------------------------

const sampleCSV = `city,country,population,area_km2,founded,capital,notes
Tokyo,Japan,13960000,2194,1457,true,"Largest metropolitan area in the world, home to the Imperial Palace"
Osaka,Japan,2750000,225,645,false,Known for street food
Lagos,Nigeria,15388000,1171,1472,false,
Lima,Peru,9752000,2672,1535,true,"City of Kings, founded by Pizarro"
Cairo,Egypt,10230000,3085,969,true,On the Nile
Kyoto,Japan,1475000,828,794,false,Former imperial capital
Abuja,Nigeria,1235880,1769,1976,true,Planned city
Cusco,Peru,428450,385,1100,false,Inca capital
Alexandria,Egypt,5200000,2679,-331,false,Founded by Alexander the Great
Nara,Japan,354630,277,710,false,Deer park
`

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]|\x1b\][^\a\x1b]*(\a|\x1b\\)`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// runSmoke drives a scripted read-only session at 120x32 without a TTY,
// prints the final frame, the checks and a summary. It writes no files and
// never touches the clipboard.
func runSmoke(stdout io.Writer, file string, opts Options) int {
	var t *Table
	var err error
	name := file
	if file == "" {
		t, err = Read(strings.NewReader(sampleCSV), opts)
		name = "sample.csv"
	} else {
		t, err = ReadFile(file, opts)
	}
	if err != nil {
		fmt.Fprintln(stdout, "smoke:", err)
		return 1
	}
	doc := newDoc(name, t, opts.NoHeader)
	doc.Path = "" // never saved
	env := newEnv(Config{NoHeader: opts.NoHeader})
	sh := newShell(env, doc, false)
	d := newDriver(sh, 120, 32)
	app := func() App { return appOf(sh.State) }
	var results []string
	fails := 0
	check := func(name string, ok bool) {
		mark := "ok  "
		if !ok {
			mark, fails = "FAIL", fails+1
		}
		results = append(results, mark+" "+name)
	}
	rows := len(doc.Rows)
	frame := func() string { return plain(sh.View().Content) }

	f := frame()
	check("frame is 120x32", len(strings.Split(f, "\n")) == 32 && maxLineW(f) <= 120)
	check("starts read-only (VIEW)", !app().Edit && strings.Contains(f, " VIEW "))
	d.keys("3", "j")
	check("3j moves to row 4", app().Grid.SelectedIdx == max(min(3, rows-1), 0))
	d.keys("g", "g", "l", "l")
	check("l l moves the active column", app().Col == min(2, len(doc.Header)-1))
	d.keys("$")
	head := []rune(app().headerName(app().Col))
	check("$ shows the last column", app().Col == len(doc.Header)-1 && strings.Contains(frame(), "▸"+string(head[:min(4, len(head))])))
	d.keys("0")
	check("0 returns to the first column", app().Col == 0)
	d.keys("/")
	d.typeText("ja")
	n := len(app().Grid.Rows)
	count := func(q string) int {
		n := 0
		for _, row := range doc.Rows {
			if slices.ContainsFunc(row, func(c string) bool { return strings.Contains(strings.ToLower(c), q) }) {
				n++
			}
		}
		return n
	}
	want := count("ja")
	check("/ filters while typing", app().Typing && n == want)
	d.typeText("~zq")
	check("a longer query narrows the matches", app().Typing && len(app().Grid.Rows) == count("ja~zq"))
	d.keys("backspace", "backspace", "backspace")
	check("backspace restores the matches", len(app().Grid.Rows) == want)
	d.keys("down")
	check("↓ moves while the filter is open", app().Grid.SelectedIdx == max(min(1, want-1), 0) && app().Typing)
	d.keys("esc")
	check("esc clears the filter", !app().Typing && app().Query == "" && len(app().Grid.Rows) == rows)
	d.keys("s")
	check("s sorts the active column", app().SortCol == 0 && app().SortDir == table.SortAsc)
	d.keys("s", "s")
	check("s s returns to file order", app().SortCol == -1)
	d.keys("d", "d", "u", "o")
	check("read-only: d d, u, o change nothing", len(app().Doc.Rows) == rows && !app().Doc.Dirty())
	d.keys(":")
	check(": opens the command palette", len(sh.State.Modals) == 1 && sh.State.Modals[0].ID == modalPalette)
	d.keys("esc", "?")
	check("? opens live help", len(sh.State.Modals) == 1 && strings.Contains(frame(), "filter.all"))
	d.keys("esc", "enter")
	check("enter opens the row detail", app().Detail && strings.Contains(frame(), detailTitle(app().Grid.SelectedIdx, rows)))
	d.keys("ctrl+w", ">")
	check("ctrl+w > widens the grid", app().Ratio > env.Ratio)
	a := app()
	g, geo := sh.State.Layout.Rects[nodeGrid], env.geometry(&a, 120, 32)
	check("grid geometry matches the layout", g.W == geo.tableW && g.H == geo.tableH)

	f = frame()
	for _, line := range strings.Split(f, "\n") {
		fmt.Fprintln(stdout, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(stdout)
	for _, r := range results {
		fmt.Fprintln(stdout, r)
	}
	fmt.Fprintf(stdout, "smoke: %d/%d checks passed · %d rows × %d cols · %d renders\n", len(results)-fails, len(results), rows, len(doc.Header), sh.Renders)
	if fails > 0 {
		return 1
	}
	return 0
}

// detailTitle is the detail pane title for the row at index i of rows.
func detailTitle(i, rows int) string {
	if rows == 0 {
		return "no rows"
	}
	return fmt.Sprintf("Row %d of %d", i+1, rows)
}

func maxLineW(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		w = max(w, textW(l))
	}
	return w
}
