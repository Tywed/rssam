package reader

import (
	"errors"
	"strings"
)

// IsTLSCertError reports whether err is a TLS certificate verification failure.
func IsTLSCertError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "x509") ||
		strings.Contains(msg, "unknown authority") ||
		errors.Is(err, errTLSCert)
}

var errTLSCert = errors.New("tls: certificate verify failed")

// TLSCertErrorMessage returns a user-facing Russian message for TLS cert failures.
func TLSCertErrorMessage() string {
	return "Сертификат сайта не доверен (часто у гос-сайтов РФ с отечественным CA). " +
		"Включите «Не проверять сертификат» для этой ленты, если доверяете источнику."
}
