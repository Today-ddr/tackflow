package tasks

import (
	"strings"
	"testing"
	"time"
)

func TestValidateTitle(t *testing.T) {
	t.Parallel()

	title, err := ValidateTitle("  修登录跳转  ")
	if err != nil {
		t.Fatalf("ValidateTitle: %v", err)
	}
	if title != "修登录跳转" {
		t.Fatalf("title = %q", title)
	}
	if _, err := ValidateTitle("   "); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := ValidateTitle(strings.Repeat("字", 501)); err == nil {
		t.Fatal("long title accepted")
	}
}

func TestValidStatusPriorityColor(t *testing.T) {
	t.Parallel()

	for _, status := range Statuses {
		if !ValidStatus(status) {
			t.Fatalf("status %q rejected", status)
		}
	}
	if ValidStatus("archive") {
		t.Fatal("unknown status accepted")
	}
	if !ValidPriority(0) || !ValidPriority(3) || ValidPriority(4) {
		t.Fatal("priority bounds are wrong")
	}
	for _, color := range Colors {
		if !ValidColor(color) {
			t.Fatalf("color %q rejected", color)
		}
	}
}

func TestPositionBetween(t *testing.T) {
	t.Parallel()

	before := 1024.0
	after := 2048.0
	got := PositionBetween(&before, &after)
	if got != 1536 {
		t.Fatalf("middle position = %v", got)
	}
	if PositionBetween(nil, &after) != 1024 {
		t.Fatal("first position is wrong")
	}
	if PositionBetween(&before, nil) != 2048 {
		t.Fatal("last position is wrong")
	}
	if !NeedsRebalance(&before, func() *float64 { v := before + 1e-10; return &v }()) {
		t.Fatal("tiny gap not detected")
	}
}

func TestValidateDueAt(t *testing.T) {
	t.Parallel()

	ok := time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC)
	if err := ValidateDueAt(&ok); err != nil {
		t.Fatalf("valid date rejected: %v", err)
	}
	bad := time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := ValidateDueAt(&bad); err == nil {
		t.Fatal("invalid date accepted")
	}
}
