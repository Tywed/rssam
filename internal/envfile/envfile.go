package envfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func SetKeys(path string, keys map[string]string) error {
	if path == "" {
		return fmt.Errorf("empty env path")
	}
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
		text = upsert(text, k, v)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
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

func Dir(path string) string {
	return filepath.Dir(path)
}
