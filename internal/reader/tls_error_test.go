package reader

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsTLSCertError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection refused"), false},
		{errors.New(`Get "https://x": x509: certificate signed by unknown authority`), true},
		{errors.New("http request: tls: failed to verify certificate"), true},
		{fmt.Errorf("wrap: %w", errors.New("x509: unknown authority")), true},
	}
	for _, tc := range tests {
		if got := IsTLSCertError(tc.err); got != tc.want {
			t.Errorf("IsTLSCertError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
