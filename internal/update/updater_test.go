package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdaterStateRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := UpdaterState{
		CurrentTag:  "v0.2.0",
		PreviousTag: "v0.1.0",
		Status:      "success",
		Message:     "ok",
	}
	if err := WriteUpdaterState(dir, want); err != nil {
		t.Fatalf("WriteUpdaterState: %v", err)
	}
	got, ok, err := ReadUpdaterState(dir)
	if err != nil {
		t.Fatalf("ReadUpdaterState: %v", err)
	}
	if !ok || got.CurrentTag != want.CurrentTag || got.PreviousTag != want.PreviousTag {
		t.Fatalf("state = %+v, ok = %v", got, ok)
	}
	info, err := os.Stat(filepath.Join(dir, "updater", "state.json"))
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("state mode = %v", info.Mode().Perm())
	}
}
