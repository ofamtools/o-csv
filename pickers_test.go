package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

// The tool's noun pickers are declared, so its catalog lists them.
func TestDeclaredPickers(t *testing.T) {
	a, e := New(Options{}, false)
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]bool{}
	for _, d := range a.r.CatalogPickerDefs() {
		got[d.Name] = d.Noun != "" && d.About != ""
	}
	for _, name := range []string{"sheet", "row", "column", "cell"} {
		if !got[name] {
			t.Errorf("catalog does not list the %s picker: %v", name, got)
		}
	}
}

// ctrl+o in a prompt opens the path picker (picker.open, the path picker's
// find source) and esc closes it, leaving the prompt as it was.
func TestPromptCtrlOOpensPathPicker(t *testing.T) {
	a := open(t, cities, true)
	if err := a.prompt("Path", "", "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	press(t, a, "ctrl+o")
	if a.r.Chooser(ochazuke.PathChooserKind) == nil {
		t.Fatal("ctrl+o in the prompt did not open the path picker")
	}
	press(t, a, "esc")
	if a.r.Chooser(ochazuke.PathChooserKind) != nil {
		t.Fatal("esc did not close the path picker")
	}
}
