package storage

import (
	"crypto/md5"
	"encoding/hex"
)

func feverAPIKey(username, password string) string {
	sum := md5.Sum([]byte(username + ":" + password))
	return hex.EncodeToString(sum[:])
}
