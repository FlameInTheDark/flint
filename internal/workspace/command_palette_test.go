package workspace

import "testing"

func TestCommandPaletteFiltersWordsAndResetsSelectionInput(t *testing.T) {
	p := &CommandPaletteState{Sel: 7}
	p.Filter.Set("toggle terminal")
	entries := p.visible()
	if len(entries) != 1 || entries[0].ID != commandToggleTerminal {
		t.Fatalf("terminal query = %#v, want just Toggle Terminal", entries)
	}

	p.Filter.Set("open")
	entries = p.visible()
	if len(entries) < 3 {
		t.Fatalf("open query returned %d entries, want file, folder and settings", len(entries))
	}
	for _, entry := range entries {
		if entry.ID == commandSave {
			t.Fatal("unrelated Save action appeared in open query")
		}
	}
}

func TestCommandPaletteHasUniqueCommandIDs(t *testing.T) {
	seen := map[commandID]bool{}
	for _, entry := range commandEntries {
		if seen[entry.ID] {
			t.Fatalf("duplicate command ID %d", entry.ID)
		}
		seen[entry.ID] = true
	}
}
