package ops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// releaseTagRe is the only shape of version accepted for self-update. The
// value ends up in the GitHub download URL that rssam-update fetches as
// root; "v1.0.0/../../other/repo/releases/download/v1" would otherwise
// resolve to another repository's asset.
var releaseTagRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// probeTimeout bounds the short systemctl probes that run inside HTTP
// handlers. A hung D-Bus call must not pin the request goroutine forever.
var probeTimeout = 15 * time.Second

func probeCommand(name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	return exec.CommandContext(ctx, name, args...), cancel
}

// ValidReleaseTag reports whether ver is a plain semver tag like v0.1.5.
func ValidReleaseTag(ver string) bool {
	return len(ver) <= 64 && releaseTagRe.MatchString(ver)
}

func EnvFilePath() string {
	if p := strings.TrimSpace(os.Getenv("RSSAM_ENV_FILE")); p != "" {
		return p
	}
	if _, err := os.Stat("/opt/rssam/.env"); err == nil {
		return "/opt/rssam/.env"
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/opt/rssam/.env"
}

func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return os.Getenv("container") != ""
}

// Restart and update are requested through a file, not through sudo. The
// service writes /opt/rssam/run/ops.request ("restart" or "update vX.Y.Z");
// the root-owned rssam-ops.path unit sees it and starts rssam-ops.service,
// which runs rssam-update --request in its own cgroup. The service process
// therefore never gains privileges (NoNewPrivileges= in the unit holds),
// and a restart of rssam does not kill the updater.
const (
	opsUnit        = "rssam-ops"
	requestDir     = "/opt/rssam/run"
	requestFile    = "ops.request"
	updateLogFile  = "/var/log/rssam-update.log"
	legacyLogFile  = "/opt/rssam/log/update.log"
	migrateHint    = "Выполните на сервере: curl -fsSL https://raw.githubusercontent.com/Tywed/rssam/main/install.sh | sudo sh -s -- --migrate-layout"
	opsUnitMissing = "Юнит rssam-ops.path не активен. " + migrateHint
)

// RequestPath is the file the ops helper watches (overridable for tests).
var RequestPath = filepath.Join(requestDir, requestFile)

func systemctlState(unit string) string {
	cmd, cancel := probeCommand("systemctl", "is-active", unit)
	defer cancel()
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// opsAvailable reports whether the request channel to root exists: the path
// unit is watching and the request directory is writable by this process.
func opsAvailable() (bool, string) {
	if InDocker() {
		return false, "В Docker перезапуск и обновление из UI недоступны."
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, "systemctl не найден."
	}
	if st := systemctlState(opsUnit + ".path"); st != "active" {
		return false, opsUnitMissing
	}
	probe := filepath.Join(filepath.Dir(RequestPath), ".probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return false, fmt.Sprintf("Каталог %s недоступен для записи: %v", filepath.Dir(RequestPath), err)
	}
	_ = os.Remove(probe)
	return true, ""
}

func CanRestart() (bool, string) { return opsAvailable() }

func CanUpdate() (bool, string) { return opsAvailable() }

// writeRequest publishes one request atomically; a request already waiting
// or an ops run in progress is reported, not overwritten.
func writeRequest(content string) error {
	if ok, reason := opsAvailable(); !ok {
		return errors.New(reason)
	}
	if UpdateRunning() {
		return errors.New("уже выполняется предыдущая операция")
	}
	return writeRequestFile(content)
}

func writeRequestFile(content string) error {
	dir := filepath.Dir(RequestPath)
	tmp, err := os.CreateTemp(dir, requestFile+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, RequestPath); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func Restart() error {
	return writeRequest("restart")
}

func StartUpdate(ver string) error {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		return fmt.Errorf("не указана версия")
	}
	if !ValidReleaseTag(ver) {
		return fmt.Errorf("недопустимая версия %q", ver)
	}
	return writeRequest("update " + ver)
}

func UpdateLogPath() string {
	if _, err := os.Stat(updateLogFile); err == nil {
		return updateLogFile
	}
	if _, err := os.Stat(legacyLogFile); err == nil {
		return legacyLogFile
	}
	return updateLogFile
}

// UpdateRunning: a request is waiting to be picked up, or the ops service is
// executing one.
func UpdateRunning() bool {
	if _, err := os.Stat(RequestPath); err == nil {
		return true
	}
	switch systemctlState(opsUnit + ".service") {
	case "active", "activating", "reloading", "deactivating":
		return true
	}
	return false
}

func ReadUpdateLog() string {
	b, err := os.ReadFile(UpdateLogPath())
	if err != nil {
		return ""
	}
	const max = 64 << 10
	if len(b) > max {
		b = b[len(b)-max:]
	}
	return string(b)
}

// ParseUpdateLog reads the outcome of the last run from its tail: the helper
// ends every run with "ok <ver>" or "error: <reason>". Restarts log
// "restart" lines only and count as done.
func ParseUpdateLog(log string, running bool) (done, ok bool, errMsg string) {
	log = strings.TrimSpace(log)
	if running {
		return false, false, ""
	}
	if log == "" {
		return false, false, ""
	}
	lines := strings.Split(log, "\n")
	last := ""
	for _, line := range slices.Backward(lines) {
		s := strings.TrimSpace(line)
		if s != "" {
			last = s
			break
		}
	}
	if strings.HasPrefix(last, "ok ") {
		return true, true, ""
	}
	if after, ok0 := strings.CutPrefix(last, "error:"); ok0 {
		return true, false, strings.TrimSpace(after)
	}
	return true, false, last
}

func SystemdActive() string {
	cmd, cancel := probeCommand("systemctl", "is-active", "rssam")
	defer cancel()
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil && s == "" {
		return "unknown"
	}
	return s
}

func BinaryPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func DualProcessWarning() string {
	cmd, cancel := probeCommand("pgrep", "-a", "-x", "rssam")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	n := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n++
	}
	if n > 1 {
		return "Несколько процессов rssam. Опрос ведёт только тот, кто первым взял блокировку на БД; остальные обслуживают HTTP."
	}
	return ""
}

func RedactDatabaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable)"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.Redacted()
}
