package storage

import "rssam/internal/auth"

func hashPassword(password string) (string, error) {
	return auth.HashPassword(password)
}
