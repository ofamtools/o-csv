package main

import (
	"io"
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/ochazuke/lexiconcheck"
)

// o-csv's registered operations are held to the lexicon's naming rules
// (lexicon plan): one word has one part of speech, nouns are singular, a
// namespace is not a noun or action word, a key has at most three parts.
// testdata/lexicon-words.tsv is a copy of ochazuke's word list.
func TestLexicon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir+"/config")
	t.Setenv("XDG_STATE_HOME", dir+"/state")
	t.Setenv("XDG_DATA_HOME", dir+"/data")
	a, err := newSheet(Options{}, false)
	if err != nil {
		t.Fatal(err)
	}
	sh := newShell(strings.NewReader(""), io.Discard, io.Discard)
	for name, rt := range map[string]*ochazuke.Runtime{"sheet": a.r, "shell": sh.Runtime} {
		t.Run(name, func(t *testing.T) {
			lexiconcheck.Check(t, lexiconcheck.Config{
				Operations: &rt.Operations,
				MainNouns:  map[string]string{"csv": "sheet"},
				Words:      "testdata/lexicon-words.tsv",
				Baseline:   "testdata/lexiconcheck.txt",
			})
		})
	}
}

// The sheet's state is its props (get), not a state verb.
func TestSheetStateIsProps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir+"/config")
	t.Setenv("XDG_STATE_HOME", dir+"/state")
	t.Setenv("XDG_DATA_HOME", dir+"/data")
	a, err := newSheet(Options{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.r.Operations.Find(ochazuke.Key("csv.sheet.state")); ok {
		t.Fatal("sheet state is still a verb")
	}
	got, err := a.r.Execute(ochazuke.Request{Noun: "sheet", Verb: "get", Args: ochazuke.Arguments{"prop": "undo_steps"}})
	if err != nil || got != 0 {
		t.Fatalf("sheet get --prop undo_steps = %v, %v", got, err)
	}
}
