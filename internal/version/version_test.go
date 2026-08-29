package version

import "testing"

func TestCompareSemver(t *testing.T) {
	if CompareSemver("v1.0.0", "1.0.0") != 0 {
		t.Fatal("equal")
	}
	if CompareSemver("1.2.0", "1.10.0") >= 0 {
		t.Fatal("1.2 < 1.10")
	}
	if CompareSemver("dev", "0.1.0") >= 0 {
		t.Fatal("dev < tagged")
	}
	if CompareSemver("1.0.1", "1.0.0") <= 0 {
		t.Fatal("patch")
	}
}
