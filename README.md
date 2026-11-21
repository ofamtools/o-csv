# o-csv

A viewing-first terminal grid for CSV, TSV and PSV files, built on
Ochazuke v2 (`core.Engine`, `tea.Shell`, `ui.ViewNode`
and the table, picker, pathpicker and form widgets).

```
o-csv [--edit] [--delim D] [--no-header] [FILE|-]
o-csv --smoke [FILE]
o-csv show    FILE [--where COL~TEXT]... [--search TEXT] [--sort COL] [--desc] [--limit N] [--format json|table|markdown]
o-csv columns FILE [--format json|table|markdown]
o-csv stats   FILE [--format json|table|markdown]
```

Piped input is read when FILE is `-` or omitted; keys then come from the
terminal (`/dev/tty`). The delimiter is detected (comma, tab, semicolon,
pipe) unless `--delim` names one. With `--no-header` columns are named
A, B, C, …

## The grid

Files open **read-only**. Press `?` for help generated from the live
keymap (your overrides included) and `:` for a palette of every command.
Keys and the palette run the same commands, so the same guards apply.

- Move: `h j k l`, `0` / `$` first/last column, `g g` / `G`, `ctrl+d/u`,
  `ctrl+f/b`. Counts work: `10j`, `3l`, `5G` (`0` joins a pending count).
- Filter: `/` searches all columns (hidden ones too), `f` the active
  column. Rows filter as you type; `↑ ↓` move meanwhile; `enter` keeps the
  filter, `esc` clears it. `ctrl+m` cycles substring → fuzzy → regex; it is
  only distinct from `enter` on terminals with the kitty keyboard protocol,
  so `alt+m`, `tab` (in the input) and the palette do the same. An invalid
  regex shows no rows and the error until it is fixed.
- `s` sorts the active column ascending, descending, then back to file
  order (numbers numerically, empty cells last; ties keep file order).
- `g b` groups by the active column (unique values give one group per
  row), `}` / `{` jump between groups, `z a` folds one.
- `-` hides the active column, `g =` shows all.
- `enter` toggles a detail pane with every column in full (hidden ones
  marked), beside the grid or below it on narrow terminals;
  `ctrl+w >` / `<` / `=` resize and reset the split, and `ctrl+w j` / `k`
  scroll long values or many columns one line at a time (back to the top
  when the focused row changes).
- `y` copies the cell, `Y` the row as CSV, to a register and the system
  clipboard (pbcopy, wl-copy, xclip or xsel) when present.
- `ctrl+o` browses for another CSV/TSV/PSV file (with preview), starting in
  the current file's directory. Started on a terminal without a FILE,
  o-csv opens empty with this file picker already open (`esc` closes it,
  `q` quits); piped stdin and `-` still read stdin.

## Editing

`--edit` or `ctrl+e` enables editing: `i`/`e` edit the focused row in a
form (int and float columns are validated, everything else is free text,
blanks stay blank), `o` inserts a blank row below, `d d` deletes the row,
`t u` / `t l` / `t t` upper-, lower- or title-case the cell, `u` undo,
`ctrl+r` redo, `ctrl+s` save.

Saving writes a temporary file next to the original and renames it into
place, so a failed save leaves the file untouched; the delimiter and the
header setting are kept, and a row holding one empty field is written as
`""` so it survives a reread. Leaving edit mode keeps unsaved edits (the
title bar says so). Quitting or opening another file with unsaved edits
asks you to repeat the key first. Stdin input cannot be saved.

## Subcommands

The first argument dispatches only when it is exactly `show`, `columns` or
`stats`; use `./show` or `-- show` for a file with such a name. FILE comes
first, flags after it; `--delim` and `--no-header` work everywhere.

Columns (`--where`, `--sort`) are matched by exact name (the first of
duplicates), then case-insensitively, then by letter (`A`) or 1-based
number. `show --format json` prints `{"columns", "rows", "total",
"matched"}`; each row is an object in column order whose keys are the
header names, with an empty name replaced by the column letter and a
repeated name suffixed `#2`, `#3`, …; `columns` lists each column's index,
letter, name and key, so nothing is lost.

## Configuration

Optional JSONC files in `$XDG_CONFIG_HOME/o-csv` (else `~/.config/o-csv`);
missing files are fine, broken ones are reported and skipped, nothing is
written:

- `bindings.json`: an array (or `{"bindings": [...]}`) of Ochazuke
  bindings, applied ahead of the defaults; `"unbind": true` removes a key.
  Modes: `normal`, `input`, `palette`, `files`, `form`, `help`.
  Example: `[{"mode": "normal", "keys": ["F"], "command": "filter.all"}]`.
- `themes.json`: a partial Ochazuke theme (`roles`, `tones`) over the
  default dark theme.
- `settings.json`: `edit` (false), `matchMode` (`"substring"`), `detailRatio`
  (0.6, grid share of the split, 0.2 to 0.8), `stackWidth` (100, narrower
  terminals stack the detail pane), `clipboard` (true).

## Limits

Cell-level highlighting is not available in the table widget: the active
column is marked in the header (`▸`) and its value shown in the status
line. Form fields append and backspace only (no cursor within a field) and
a form taller than the screen shows a window of its fields that follows the
active one (the title says which, e.g. "fields 3-20 of 40"). The whole file is held in memory
and the grid re-renders every row, so very large files are slow. A
leading byte-order mark is not written back.
