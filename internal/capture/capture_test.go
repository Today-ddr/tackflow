package capture

import (
	"testing"
	"time"
)

func TestParseCaptureSyntax(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	now := time.Date(2026, 11, 16, 9, 30, 0, 0, loc)
	result, err := Parse("修一下登录后的跳转 #bug #前端 @codeg !p2 ^明天\n补充说明\n第二行", now, loc)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if result.Title != "修一下登录后的跳转" {
		t.Fatalf("title = %q", result.Title)
	}
	if result.Notes != "补充说明\n第二行" {
		t.Fatalf("notes = %q", result.Notes)
	}
	if len(result.Tags) != 2 || result.Tags[0] != "bug" || result.Tags[1] != "前端" {
		t.Fatalf("tags = %#v", result.Tags)
	}
	if result.Project != "codeg" {
		t.Fatalf("project = %q", result.Project)
	}
	if result.Priority != 2 {
		t.Fatalf("priority = %d", result.Priority)
	}
	want := time.Date(2026, 11, 17, 23, 59, 59, 0, loc)
	if result.DueAt == nil || !result.DueAt.Equal(want) {
		t.Fatalf("due = %v, want %v", result.DueAt, want)
	}
}

func TestParseCaptureDatesAndEmptyTitle(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	now := time.Date(2026, 11, 16, 9, 30, 0, 0, loc)
	for input, wantDate := range map[string]string{
		"今天交周报 ^今天":        "2026-11-16",
		"整理桌面 ^2026-11-20": "2026-11-20",
	} {
		result, err := Parse(input, now, loc)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if result.DueAt == nil || result.DueAt.Format("2006-01-02") != wantDate {
			t.Fatalf("Parse(%q) due = %v", input, result.DueAt)
		}
	}
	if _, err := Parse("#onlytag", now, loc); err == nil {
		t.Fatal("empty title accepted")
	}
}
