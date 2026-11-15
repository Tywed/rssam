package envfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// setMu serialises writers: two concurrent saves would otherwise each
// rewrite the file from its own stale copy and drop the other's keys.
var setMu sync.Mutex

func SetKeys(path string, keys map[string]string) error {
	if path == "" {
		return fmt.Errorf("empty env path")
	}
	setMu.Lock()
	defer setMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		raw = nil
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") && text != "" {
		text += "\n"
	}
	for k, v := range keys {
		if !validKey(k) {
			return fmt.Errorf("invalid key %q", k)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("invalid value for %s: control characters", k)
		}
		text = upsert(text, k, v)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(text)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func upsert(text, key, value string) string {
	prefix := key + "="
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, prefix) || trim == key {
			lines[i] = prefix + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, prefix+value)
	}
	return strings.Join(lines, "\n") + "\n"
}
