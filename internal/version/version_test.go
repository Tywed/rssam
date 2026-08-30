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
	if CompareSemver("v0.1.0-1-gc3a0daf", "v0.1.0") <= 0 {
		t.Fatal("git describe is newer than its tag")
	}
	if CompareSemver("v0.1.0-1-gc3a0daf", "v0.1.1") >= 0 {
		t.Fatal("git describe after 0.1.0 is older than 0.1.1")
	}
	if CompareSemver("0.1.0-alpha", "0.1.0") >= 0 {
		t.Fatal("prerelease < release")
	}
}
