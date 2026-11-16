package tasks

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	StatusInbox  = "inbox"
	StatusTodo   = "todo"
	StatusDoing  = "doing"
	StatusReview = "review"
	StatusDone   = "done"

	PriorityNone   = 0
	PriorityLow    = 1
	PriorityMedium = 2
	PriorityHigh   = 3
)

var Statuses = []string{
	StatusInbox,
	StatusTodo,
	StatusDoing,
	StatusReview,
	StatusDone,
}

var Colors = []string{"sage", "clay", "ink", "sky", "plum"}

func ValidStatus(status string) bool {
	return slices.Contains(Statuses, status)
}

func ValidPriority(priority int) bool {
	return priority >= PriorityNone && priority <= PriorityHigh
}

func ValidColor(color string) bool {
	return slices.Contains(Colors, color)
}

func ColorForName(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(name))))
	return Colors[int(sum[0])%len(Colors)]
}

func NormalizeTitle(title string) string {
	return strings.TrimSpace(title)
}

func ValidateTitle(title string) (string, error) {
	title = NormalizeTitle(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	if len([]rune(title)) > 500 {
		return "", fmt.Errorf("title must be at most 500 characters")
	}
	return title, nil
}

func ValidateNotes(notes string) (string, error) {
	if len([]rune(notes)) > 100_000 {
		return "", fmt.Errorf("notes must be at most 100000 characters")
	}
	return notes, nil
}

func ValidateDueAt(due *time.Time) error {
	if due == nil {
		return nil
	}
	if due.Year() < 2000 || due.Year() > 2200 {
		return fmt.Errorf("due_at is out of range")
	}
	return nil
}

// PositionBetween returns a stable sort key between before and after.
// The caller should rebalance a column when adjacent positions are too close.
func PositionBetween(before, after *float64) float64 {
	switch {
	case before == nil && after == nil:
		return 1024
	case before == nil:
		return *after - 1024
	case after == nil:
		return *before + 1024
	default:
		return (*before + *after) / 2
	}
}

func NeedsRebalance(before, after *float64) bool {
	return before != nil && after != nil && (*after-*before) < 1e-9
}
