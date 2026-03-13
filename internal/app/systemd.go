package app

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

const systemdServiceName = "mcpe.service"

type SystemdStatus struct {
	ServiceName string
	Available   bool
	Registered  bool
	Enabled     bool
	Active      bool
	LoadState   string
	UnitPath    string
	StatusHint  string
}

func enableSystemdService(configPath string, listenAddr string) error {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	executable, err := resolveServiceExecutable()
	if err != nil {
		return err
	}
	workDir := filepath.Dir(configPath)
	content := buildSystemdUnitContent(executable, workDir, configPath, listenAddr)
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return err
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	return runSystemctl("enable", "--now", systemdServiceName)
}

func disableSystemdService() error {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := runSystemctl("disable", "--now", systemdServiceName); err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return runSystemctl("daemon-reload")
}

func startSystemdService() error {
	return runSystemctl("start", systemdServiceName)
}

func stopSystemdService() error {
	return runSystemctl("stop", systemdServiceName)
}

func runSystemctl(args ...string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found")
	}
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func systemdUnitPath() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(currentUser.HomeDir, ".config", "systemd", "user", systemdServiceName), nil
}

func resolveServiceExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return validateServiceExecutablePath(executable)
}

func validateServiceExecutablePath(executable string) (string, error) {
	cleaned := filepath.Clean(executable)
	if strings.Contains(cleaned, string(filepath.Separator)+"go-build") || strings.Contains(cleaned, filepath.Join(os.TempDir(), "go-build")) {
		return "", fmt.Errorf("systemd service requires an installed mcpe binary; rerun this command with a stable executable instead of go run")
	}
	return cleaned, nil
}

func readSystemdStatus() SystemdStatus {
	status := SystemdStatus{
		ServiceName: systemdServiceName,
		StatusHint:  "systemctl --user status " + systemdServiceName,
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return status
	}
	status.Available = true
	unitPath, err := systemdUnitPath()
	if err == nil {
		status.UnitPath = unitPath
		if _, statErr := os.Stat(unitPath); statErr == nil {
			status.Registered = true
		}
	}
	status.Enabled = systemctlSuccess("is-enabled", systemdServiceName)
	status.Active = systemctlSuccess("is-active", systemdServiceName)
	status.LoadState = strings.TrimSpace(systemctlOutput("show", "--property=LoadState", "--value", systemdServiceName))
	return status
}

func systemctlSuccess(args ...string) bool {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	return cmd.Run() == nil
}

func systemctlOutput(args ...string) string {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func systemdCommandLine(args []string) string {
	escaped := make([]string, 0, len(args))
	for _, arg := range args {
		escaped = append(escaped, escapeSystemdArg(arg))
	}
	return strings.Join(escaped, " ")
}

func buildSystemdUnitContent(executable string, workDir string, configPath string, listenAddr string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=mcp-estuary gateway",
		"After=network.target",
		"",
		"[Service]",
		"Type=simple",
		"WorkingDirectory=" + workDir,
		"ExecStart=" + systemdCommandLine([]string{
			executable,
			"serve",
			"--foreground",
			"--config",
			configPath,
			"--listen",
			listenAddr,
		}),
		"Restart=on-failure",
		"RestartSec=2",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n")
}

func escapeSystemdArg(arg string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		" ", "\\ ",
		"\t", "\\t",
		"\n", "\\n",
		`"`, `\"`,
		"'", "\\'",
	)
	return replacer.Replace(arg)
}
