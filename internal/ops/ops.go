package ops

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// releaseTagRe is the only shape of version accepted for self-update. The
// value ends up in the GitHub download URL that rssam-update fetches as
// root; "v1.0.0/../../other/repo/releases/download/v1" would otherwise
// resolve to another repository's asset.
var releaseTagRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// probeTimeout bounds the short sudo/systemctl probes that run inside HTTP
// handlers. A hung sudo (e.g. PAM waiting on something) must not pin the
// request goroutine forever. StartUpdate is exempt: it detaches on purpose.
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

func CanRestart() (bool, string) {
	if InDocker() {
		return false, "В Docker перезапуск из UI недоступен."
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, "systemctl не найден."
	}
	if lookSudoers() {
		return true, ""
	}
	return false, "Нет sudo NOPASSWD для systemctl restart rssam."
}

func lookSudoers() bool {
	cmd, cancel := probeCommand("sudo", "-n", "-l")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "systemctl") && strings.Contains(s, "rssam")
}

func Restart() error {
	if InDocker() {
		return fmt.Errorf("docker")
	}
	cmd, cancel := probeCommand("sudo", "-n", "systemctl", "restart", "--no-block", "rssam")
	defer cancel()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func UpdateHelperPath() string {
	for _, p := range []string{"/usr/local/sbin/rssam-update", "/opt/rssam/bin/rssam-update"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("rssam-update"); err == nil {
		return p
	}
	return "/usr/local/sbin/rssam-update"
}

func CanUpdate() (bool, string) {
	if InDocker() {
		return false, "В Docker обновление из UI недоступно."
	}
	helper := UpdateHelperPath()
	if _, err := os.Stat(helper); err != nil {
		return false, "Не найден rssam-update."
	}
	help, cancelHelp := probeCommand("sudo", "-n", helper, "--quiet", "--help")
	defer cancelHelp()
	if err := help.Run(); err != nil {
		list, cancelList := probeCommand("sudo", "-n", "-l")
		defer cancelList()
		out, e2 := list.Output()
		if e2 == nil && strings.Contains(string(out), "rssam-update") {
			return true, ""
		}
		_ = err
		return false, "Нет sudo на rssam-update."
	}
	return true, ""
}

func UpdateLogPath() string {
	return "/opt/rssam/log/update.log"
}

func updatePIDPath() string {
	return "/opt/rssam/log/update.pid"
}

func UpdateRunning() bool {
	b, err := os.ReadFile(updatePIDPath())
	if err != nil {
		return false
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
	if convErr != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
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
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s != "" {
			last = s
			break
		}
	}
	if strings.HasPrefix(last, "ok ") {
		return true, true, ""
	}
	if strings.HasPrefix(last, "error:") {
		return true, false, strings.TrimSpace(strings.TrimPrefix(last, "error:"))
	}
	return true, false, last
}

func StartUpdate(ver string) error {
	ok, reason := CanUpdate()
	if !ok {
		return fmt.Errorf("%s", reason)
	}
	if UpdateRunning() {
		return fmt.Errorf("уже идёт обновление")
	}
	ver = strings.TrimSpace(ver)
	if ver == "" {
		return fmt.Errorf("не указана версия")
	}
	if !ValidReleaseTag(ver) {
		return fmt.Errorf("недопустимая версия %q", ver)
	}
	helper := UpdateHelperPath()
	args := []string{"-n", helper, "--update", "--quiet", ver}
	cmd := exec.Command("sudo", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logDir := "/opt/rssam/log"
	_ = os.MkdirAll(logDir, 0755)
	f, err := os.OpenFile(UpdateLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		cmd.Stdout = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		if f != nil {
			f.Close()
		}
		return err
	}
	_ = os.WriteFile(updatePIDPath(), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0644)
	go func() {
		_ = cmd.Wait()
		if f != nil {
			f.Close()
		}
		_ = os.Remove(updatePIDPath())
	}()
	return nil
}

func SystemdActive() string {
	out, err := exec.Command("systemctl", "is-active", "rssam").CombinedOutput()
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
	out, err := exec.Command("pgrep", "-a", "-x", "rssam").Output()
	if err != nil {
		return ""
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n++
	}
	if n > 1 {
		return "Несколько процессов rssam. Два poller’а на одной БД делят очередь jobs."
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
