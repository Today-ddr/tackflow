package update

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

func ParseVersion(text string) (Version, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "v")
	if text == "" || text == "dev" {
		return Version{}, nil
	}
	main, pre, _ := strings.Cut(text, "-")
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version must be SemVer")
	}
	values := [3]int{}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("version must be SemVer")
		}
		values[i] = value
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2], Pre: pre}, nil
}

func CompareVersions(left, right string) (int, error) {
	a, err := ParseVersion(left)
	if err != nil {
		return 0, err
	}
	b, err := ParseVersion(right)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{
		{a.Major, b.Major},
		{a.Minor, b.Minor},
		{a.Patch, b.Patch},
	} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
	}
	return 0, nil
}

func IsNewer(current, candidate string) (bool, error) {
	comparison, err := CompareVersions(current, candidate)
	if err != nil {
		return false, err
	}
	return comparison < 0, nil
}

func IsReleaseTag(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}
	parsed, err := ParseVersion(tag)
	return err == nil && parsed.Pre == "" && strings.Count(tag, ".") == 2 && len(tag) <= 32
}
