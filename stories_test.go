package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Ownership stories are data under cmd/o-story/stories/ownership. The shared
// o-story runner cannot construct an o-csv app, so this adapter replays each
// script on the real page and checks the interaction after the named steps,
// read through the runtime's accessors.
func TestOwnershipStories(t *testing.T) {
	files, err := filepath.Glob("cmd/o-story/stories/ownership/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no ownership stories")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			var story struct {
				Name   string
				Skip   string
				File   string
				Script []string
				Expect []struct {
					After int
					Focus string
					Mode  string
				}
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &story); err != nil {
				t.Fatal(err)
			}
			if story.Skip != "" {
				t.Skip(story.Skip)
			}
			a := open(t, story.File, false)
			for i, key := range story.Script {
				press(t, a, key)
				for _, want := range story.Expect {
					if want.After != i+1 {
						continue
					}
					if got := a.r.FocusID(); got != want.Focus {
						t.Fatalf("%s: after step %d (%q) focus %q, want %q", story.Name, want.After, key, got, want.Focus)
					}
					if got := a.r.CurrentMode().Name; got != want.Mode {
						t.Fatalf("%s: after step %d (%q) mode %q, want %q", story.Name, want.After, key, got, want.Mode)
					}
				}
			}
		})
	}
}
