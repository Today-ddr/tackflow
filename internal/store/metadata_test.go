package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTaskAStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st, time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
}

func TestTaskATagUpdatePreservesBindings(t *testing.T) {
	st, now := newTaskAStore(t)
	tag, err := st.CreateTag(t.Context(), "old", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(t.Context(), CreateTaskParams{Title: "tagged", TagIDs: []int64{tag.ID}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	name, color := " #new ", "sky"
	updated, err := st.UpdateTag(t.Context(), tag.ID, &name, &color)
	if err != nil || updated.Name != "new" || updated.Color != "sky" || updated.ID != tag.ID {
		t.Fatalf("updated = %+v, err = %v", updated, err)
	}
	loaded, err := st.TaskByID(t.Context(), task.ID)
	if err != nil || len(loaded.Tags) != 1 || loaded.Tags[0].Name != "new" {
		t.Fatalf("bindings = %+v, err = %v", loaded.Tags, err)
	}
	if err := st.DeleteTag(t.Context(), tag.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.TaskByID(t.Context(), task.ID)
	if err != nil || len(loaded.Tags) != 0 {
		t.Fatalf("task after tag deletion = %+v, err = %v", loaded, err)
	}
}

func TestTaskATagUpdateRejectsConflictInvalidAndMissing(t *testing.T) {
	st, now := newTaskAStore(t)
	first, err := st.CreateTag(t.Context(), "first", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTag(t.Context(), "taken", "clay", now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		color string
		want  error
	}{
		{name: "taken", color: "sky", want: ErrTagExists},
		{name: " ", color: "sky", want: ErrInvalidTag},
		{name: strings.Repeat("界", 81), color: "sky", want: ErrInvalidTag},
		{name: "valid", color: "#fff", want: ErrInvalidTag},
	} {
		if _, err := st.UpdateTag(t.Context(), first.ID, &test.name, &test.color); !errors.Is(err, test.want) {
			t.Errorf("UpdateTag(%q, %q) = %v, want %v", test.name, test.color, err, test.want)
		}
	}
	unchanged, err := st.TagByID(t.Context(), first.ID)
	if err != nil || unchanged.Name != "first" || unchanged.Color != "sage" {
		t.Fatalf("invalid update mutated tag: %+v, %v", unchanged, err)
	}
	color := "plum"
	updated, err := st.UpdateTag(t.Context(), first.ID, nil, &color)
	if err != nil || updated.Name != "first" || updated.Color != "plum" {
		t.Fatalf("partial update = %+v, err = %v", updated, err)
	}
	if _, err := st.UpdateTag(t.Context(), 9999, nil, &color); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag error = %v", err)
	}
}
