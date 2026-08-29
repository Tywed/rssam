package model

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// NormalizeURL предназначен для dedup по URL (MVP).
// Политику нормализации можно расширять по мере появления reader-слоя.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// Не валидируем строго на этом этапе: пусть upstream решит.
		return raw
	}

	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Убираем "пустой" путь.
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String()
}

func DedupHashFromURL(raw string) string {
	n := NormalizeURL(raw)
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])
}

// DedupHashFromString hashes an arbitrary stable key (e.g. owner_id_post_id).
func DedupHashFromString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
