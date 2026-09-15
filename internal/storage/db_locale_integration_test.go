//go:build integration

package storage

import (
	"context"
	"testing"
)

// The CI database is created with an UTF-8 locale, so the probe must report
// folding; this is also what guards the Russian FTS tests in this package.
func TestIntegration_ProbeDatabaseLocale(t *testing.T) {
	store := testStore(t)
	l, err := ProbeDatabaseLocale(context.Background(), store.db)
	if err != nil {
		t.Fatal(err)
	}
	if l.Encoding != "UTF8" || l.Ctype == "" {
		t.Fatalf("unexpected probe result: %+v", l)
	}
	if !l.LowerOK {
		t.Fatalf("database ctype %q does not fold Cyrillic; the Russian FTS tests would fail on it", l.Ctype)
	}
}
