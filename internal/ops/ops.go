package ops

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

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
	out, err := exec.Command("sudo", "-n", "-l").Output()
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
	cmd := exec.Command("sudo", "-n", "systemctl", "restart", "--no-block", "rssam")
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
	if err := exec.Command("sudo", "-n", helper, "--quiet", "--help").Run(); err != nil {
		out, e2 := exec.Command("sudo", "-n", "-l").Output()
		if e2 == nil && strings.Contains(string(out), "rssam-update") {
			return true, ""
		}
		_ = err
		return false, "Нет sudo на rssam-update."
	}
	return true, ""
}

func StartUpdate(ver string) error {
	ok, reason := CanUpdate()
	if !ok {
		return fmt.Errorf("%s", reason)
	}
	helper := UpdateHelperPath()
	args := []string{"-n", helper, "--update", "--quiet"}
	if strings.TrimSpace(ver) != "" {
		args = append(args, strings.TrimSpace(ver))
	}
	cmd := exec.Command("sudo", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logDir := "/opt/rssam/log"
	_ = os.MkdirAll(logDir, 0755)
	f, err := os.OpenFile(filepath.Join(logDir, "update.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
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
	go func() {
		_ = cmd.Wait()
		if f != nil {
			f.Close()
		}
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

func UpdateLogPath() string {
	return "/opt/rssam/log/update.log"
}
