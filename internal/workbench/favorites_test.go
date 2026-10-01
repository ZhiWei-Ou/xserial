package workbench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFavoritesPersistAcrossSessionsAndRejectCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xserial", "commands.json")
	if got, err := LoadFavorites(path); err != nil || len(got) != 0 {
		t.Fatalf("missing file = %v, %v", got, err)
	}
	want := []Favorite{{Name: "Query", Hex: "01 03 00 00 00 02 C4 0B"}}
	if err := SaveFavorites(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFavorites(path)
	if err != nil || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("reloaded favorites = %v, %v", got, err)
	}
	if err := SaveFavorites(path, []Favorite{{Name: "bad", Hex: "GG"}}); err == nil {
		t.Fatal("invalid command replaced saved favorites")
	}
	got, err = LoadFavorites(path)
	if err != nil || got[0] != want[0] {
		t.Fatal("saved commands changed after failed validation")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFavorites(path); err == nil {
		t.Fatal("corrupt favorites accepted")
	}
}
