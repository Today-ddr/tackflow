package capture

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	tagPattern      = regexp.MustCompile(`#([\p{L}\p{N}_-]+)`)
	projectPattern  = regexp.MustCompile(`@([\p{L}\p{N}_-]+)`)
	priorityPattern = regexp.MustCompile(`(?i)!p([0-3])`)
	datePattern     = regexp.MustCompile(`\^([^\s]+)`)
)

type Result struct {
	Title    string
	Notes    string
	Tags     []string
	Project  string
	Priority int
	DueAt    *time.Time
	Raw      string
}

// Parse extracts the inline capture syntax from the first line and keeps the
// remaining lines as notes. Dates without a time are due at local end of day.
func Parse(raw string, now time.Time, location *time.Location) (Result, error) {
	if location == nil {
		location = time.Local
	}
	now = now.In(location)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	headline := lines[0]
	notes := strings.TrimSpace(strings.Join(lines[1:], "\n"))

	result := Result{
		Tags: make([]string, 0),
		Raw:  raw,
	}

	tagSeen := make(map[string]struct{})
	for _, match := range tagPattern.FindAllStringSubmatch(headline, -1) {
		name := strings.TrimSpace(match[1])
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, ok := tagSeen[key]; ok {
			continue
		}
		tagSeen[key] = struct{}{}
		result.Tags = append(result.Tags, name)
	}

	if match := projectPattern.FindStringSubmatch(headline); match != nil {
		result.Project = strings.TrimSpace(match[1])
	}
	if match := priorityPattern.FindStringSubmatch(headline); match != nil {
		result.Priority = int(match[1][0] - '0')
	}
	if match := datePattern.FindStringSubmatch(headline); match != nil {
		due, err := parseDueDate(match[1], now, location)
		if err == nil {
			result.DueAt = &due
		}
	}

	title := tagPattern.ReplaceAllString(headline, " ")
	title = projectPattern.ReplaceAllString(title, " ")
	title = priorityPattern.ReplaceAllString(title, " ")
	title = datePattern.ReplaceAllString(title, " ")
	title = collapseSpaces(title)
	title = strings.TrimSpace(title)
	if title == "" {
		return Result{}, fmt.Errorf("capture title is empty")
	}
	result.Title = title
	result.Notes = notes
	return result, nil
}

func parseDueDate(value string, now time.Time, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "今天", "today":
		return endOfDay(now, location), nil
	case "明天", "tomorrow":
		return endOfDay(now.AddDate(0, 0, 1), location), nil
	case "后天":
		return endOfDay(now.AddDate(0, 0, 2), location), nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid capture date")
	}
	return time.Date(
		parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, location,
	), nil
}

func endOfDay(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, location)
}

func collapseSpaces(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
