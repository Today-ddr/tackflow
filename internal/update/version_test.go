package update

import "testing"

func TestVersionComparison(t *testing.T) {
	t.Parallel()

	comparison, err := CompareVersions("0.1.0", "v0.2.0")
	if err != nil || comparison != -1 {
		t.Fatalf("CompareVersions = %d, %v", comparison, err)
	}
	comparison, err = CompareVersions("1.0.0", "1.0.0")
	if err != nil || comparison != 0 {
		t.Fatalf("CompareVersions equal = %d, %v", comparison, err)
	}
	comparison, err = CompareVersions("1.2.3", "1.2.2")
	if err != nil || comparison != 1 {
		t.Fatalf("CompareVersions greater = %d, %v", comparison, err)
	}
	if newer, err := IsNewer("0.1.0", "v0.1.1"); err != nil || !newer {
		t.Fatalf("IsNewer = %v, %v", newer, err)
	}
	if _, err := CompareVersions("dev", "0.1.0"); err != nil {
		t.Fatalf("dev version rejected: %v", err)
	}
	if _, err := ParseVersion("0.1"); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func TestIsReleaseTag(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"v0.1.0", "v1.12.3"} {
		if !IsReleaseTag(tag) {
			t.Fatalf("release tag %q rejected", tag)
		}
	}
	for _, tag := range []string{"latest", "0.1.0", "v1.2", "v1.2.3-rc.1", "v1.2.3.4"} {
		if IsReleaseTag(tag) {
			t.Fatalf("non-release tag %q accepted", tag)
		}
	}
}
