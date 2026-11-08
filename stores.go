package main

import (
	"fmt"
	"strconv"
	"strings"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/grid"
	"ofam.dev/ochazuke/namecheck"
)

// The sheet's nouns that held a verb named like a core verb (cell edit,
// row and column delete, sheet edit, transformation list) are thing nouns:
// each has a facts store over the state it already answers with, so get (and
// list, where there are many) read it, and create, set, delete, archive and
// edit are on the noun, unavailable as a system fact. The verbs the tool
// registers under a core name stay and win over the generated ones: the
// edits go through a.Apply, the one patch that records undo.

// csvRemovedVerbs are the verbs o-csv renamed so no command verb is named
// like a core verb (DESIGN-001, owner decision: purged with no alias).
var csvRemovedVerbs = []namecheck.Removed{
	{Noun: "ochazuke.selection", Verb: "delete", Replacement: "ochazuke.selection.cut", Since: "o-csv R1 (ochazuke v0.92.1)",
		Note: "DESIGN-001: purged with no alias; the selection is not a keyed collection, and the verb removes it into the registers, a cut"},
}

func fact(name, typ, desc string) ochazuke.PropSpec {
	return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: ochazuke.Parameter{Type: typ, Description: desc}}
}

func factKey(name, desc string) ochazuke.PropSpec {
	p := fact(name, "", desc)
	p.Key, p.Human = true, true
	return p
}

func factLabel(name, desc string) ochazuke.PropSpec {
	p := fact(name, "", desc)
	p.Role = ochazuke.RoleLabel
	return p
}

// collection declares a noun whose things are the rows list answers; get
// finds one by its id. A nil list is a noun with no list (get reads by id).
func collection(r *ochazuke.Runtime, noun string, props []ochazuke.PropSpec, list func() []ochazuke.Thing, get func(string) (ochazuke.Thing, error)) {
	s := ochazuke.Store{Kind: ochazuke.KindCollection, Facts: true}
	if list != nil {
		s.List = func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) { return list(), nil }
		get = func(key string) (ochazuke.Thing, error) {
			for _, t := range list() {
				if fmt.Sprint(t["id"]) == key {
					return t, nil
				}
			}
			return nil, fmt.Errorf("unknown %s %q", noun, key)
		}
	} else {
		s.Missing = map[string]string{"list": "a " + noun + " is named by its place; there is no list of them"}
	}
	s.Get = func(_ ochazuke.Ctx, key string) (ochazuke.Thing, error) { return get(key) }
	r.Define(noun).Props(props...).Store(s)
}

// defineStores makes the sheet's nouns thing nouns (see above) and records
// the verbs o-csv renamed. It runs once every verb is registered.
func (a *App) defineStores() error {
	r := a.r
	r.Define("sheet").Props(
		fact("path", "", "The file shown"), fact("editing", "boolean", "Whether editing is on"), fact("dirty", "boolean", "Whether there are unsaved edits"),
		fact("rows", "integer", "How many rows"), fact("columns", "integer", "How many columns"), fact("visible", "integer", "How many rows the filter shows"),
		fact("delimiter", "", "The field delimiter"), fact("frozen", "boolean", "Whether the first shown column stays at the left"),
		fact("undo_steps", "integer", "How many edits undo takes back"), fact("redo_steps", "integer", "How many undone edits redo brings back"), fact("cursor", "object", "The cursor's row and column"),
		fact("filtered_by", "object", "The column filter"), fact("sorted_by", "object", "The sort"), fact("search_text", "", "The text the find verb looks for"),
		fact("hidden_columns", "list", "The hidden columns, from 0"), fact("detail_shown", "boolean", "Whether the row detail is shown"),
	).Store(ochazuke.Store{Kind: ochazuke.Singleton, Facts: true,
		Get: func(ochazuke.Ctx, string) (ochazuke.Thing, error) {
			s := a.state()
			return ochazuke.Thing{"path": s["path"], "editing": s["edit"], "dirty": s["dirty"], "rows": s["rows"], "columns": s["columns"],
				"visible": s["visible"], "delimiter": s["delimiter"], "frozen": s["freeze"],
				"undo_steps": s["undo"], "redo_steps": s["redo"], "cursor": s["cursor"], "filtered_by": s["filter"], "sorted_by": s["sort"], "search_text": s["search"],
				"hidden_columns": s["hidden"], "detail_shown": s["detail"]}, nil
		}})
	collection(r, "row", []ochazuke.PropSpec{
		factKey("id", "The row's number, from 1"), factLabel("label", "The row's first cells"), fact("cells", "list", "The row's cells"),
	}, func() []ochazuke.Thing {
		out := make([]ochazuke.Thing, len(a.Rows))
		for i, row := range a.Rows {
			out[i] = ochazuke.Thing{"id": strconv.Itoa(i + 1), "label": strings.Join(row, ", "), "cells": row}
		}
		return out
	}, nil)
	collection(r, "column", []ochazuke.PropSpec{
		factKey("id", "The column's number, from 1"), factLabel("label", "The column's header"), fact("hidden", "boolean", "Whether the column is hidden"),
	}, func() []ochazuke.Thing {
		out := make([]ochazuke.Thing, len(a.Header))
		for j, name := range a.Header {
			out[j] = ochazuke.Thing{"id": strconv.Itoa(j + 1), "label": name, "hidden": a.Hidden[j]}
		}
		return out
	}, nil)
	collection(r, "cell", []ochazuke.PropSpec{
		factKey("id", "The cell's place, ROW:COLUMN from 1"), factLabel("label", "The cell's value"), fact("row", "integer", "Its row, from 1"), fact("column", "integer", "Its column, from 1"),
	}, nil, func(key string) (ochazuke.Thing, error) {
		i, j, ok := parseCellKey(key)
		if !ok || i < 1 || j < 1 || i > len(a.Rows) || j > len(a.Header) {
			return nil, fmt.Errorf("no cell %q (ROW:COLUMN, from 1)", key)
		}
		return ochazuke.Thing{"id": key, "label": a.Cell(grid.Ref{Row: i - 1, Col: j - 1}), "row": i, "column": j}, nil
	})
	collection(r, "transformation", []ochazuke.PropSpec{factKey("id", "The transformation's name"), factLabel("label", "What it does")},
		func() []ochazuke.Thing {
			choices := TransformationChoices()
			out := make([]ochazuke.Thing, len(choices))
			for i, c := range choices {
				out[i] = ochazuke.Thing{"id": c.ID, "label": c.Label}
			}
			return out
		}, nil)
	return r.AddRemovedVerbs(csvRemovedVerbs...)
}

// parseCellKey reads ROW:COLUMN.
func parseCellKey(key string) (row, col int, ok bool) {
	rs, cs, found := strings.Cut(key, ":")
	if !found {
		return 0, 0, false
	}
	row, err1 := strconv.Atoi(rs)
	col, err2 := strconv.Atoi(cs)
	return row, col, err1 == nil && err2 == nil
}
