package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/install"
	"github.com/Melon-cream/mcp-estuary/internal/process"
	"github.com/Melon-cream/mcp-estuary/internal/state"
)

type doctorServerReport struct {
	Name      string
	State     string
	Checks    []doctorCheck
	ToolCount int
}

type doctorCheck struct {
	Label   string
	Status  string
	Details string
}

func runDoctor(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	normalized, useServers, err := extractUseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "parse doctor args: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	if err := fs.Parse(normalized); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	_, resolvedConfig, err := resolveLayout(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	cfg, err := config.LoadLenient(resolvedConfig)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	selected := selectServers(cfg, useServers)

	reports := make([]doctorServerReport, 0, len(selected.Defined))
	names := mapKeys(selected.Defined)
	for _, name := range names {
		if errMsg, ok := selected.Errors[name]; ok {
			reports = append(reports, doctorServerReport{
				Name:  name,
				State: "error",
				Checks: []doctorCheck{
					{Label: "config", Status: "error", Details: errMsg},
				},
			})
			continue
		}
		reports = append(reports, diagnoseServer(ctx, selected.Servers[name]))
	}

	renderDoctor(stdout, resolvedConfig, cfg, reports)
	for _, report := range reports {
		if report.State == "error" {
			return 1
		}
	}
	return 0
}

func diagnoseServer(ctx context.Context, server config.Server) doctorServerReport {
	report := doctorServerReport{Name: server.Name, State: "ok"}
	for _, command := range requiredCommands(server) {
		if _, err := exec.LookPath(command); err != nil {
			report.State = "error"
			report.Checks = append(report.Checks, doctorCheck{
				Label:   "command " + command,
				Status:  "error",
				Details: "not found in PATH",
			})
		} else {
			report.Checks = append(report.Checks, doctorCheck{
				Label:   "command " + command,
				Status:  "ok",
				Details: "available",
			})
		}
	}

	if len(server.EnvStatus) == 0 {
		report.Checks = append(report.Checks, doctorCheck{Label: "env", Status: "info", Details: "no env bindings"})
	} else {
		envStates := make([]string, 0, len(server.EnvStatus))
		envNames := make([]string, 0, len(server.EnvStatus))
		for name := range server.EnvStatus {
			envNames = append(envNames, name)
		}
		sortStrings(envNames)
		for _, name := range envNames {
			meta := server.EnvStatus[name]
			status := "set"
			if meta.Status == "missing" {
				status = "missing"
				report.State = "error"
			}
			envStates = append(envStates, fmt.Sprintf("%s=%s", name, status))
		}
		report.Checks = append(report.Checks, doctorCheck{Label: "env", Status: "ok", Details: strings.Join(envStates, ", ")})
	}

	tempRoot, err := os.MkdirTemp("", "mcpe-doctor-*")
	if err != nil {
		report.State = "error"
		report.Checks = append(report.Checks, doctorCheck{Label: "workspace", Status: "error", Details: err.Error()})
		return report
	}
	defer os.RemoveAll(tempRoot)

	tempLayout := state.NewLayout(tempRoot)
	_ = tempLayout.Ensure()
	serverForDoctor := server
	workDir := tempLayout.ServerWorkDir(server.Name)
	if server.Cwd != "" {
		serverForDoctor.Cwd = workDir
		report.Checks = append(report.Checks, doctorCheck{
			Label:   "cwd",
			Status:  "warn",
			Details: "doctor uses an isolated temp cwd to avoid mutating the configured path",
		})
		if report.State == "ok" {
			report.State = "warn"
		}
	}
	logPath := tempLayout.ServerLogPath(server.Name)
	installCtx, installCancel := context.WithTimeout(ctx, 30*time.Second)
	defer installCancel()
	installs := install.Run(installCtx, []install.Request{{Server: serverForDoctor, WorkDir: workDir, LogPath: logPath}}, 1, nil)
	result := installs[server.Name]
	if !result.Installed {
		report.State = "error"
		report.Checks = append(report.Checks, doctorCheck{Label: "install", Status: "error", Details: result.Error})
		return report
	}
	report.Checks = append(report.Checks, doctorCheck{Label: "install", Status: "ok", Details: "ready"})

	manager := process.NewManager(
		map[string]config.Server{server.Name: serverForDoctor},
		installs,
		map[string]string{server.Name: workDir},
		map[string]string{server.Name: logPath},
		nil,
		nil,
	)
	defer manager.StopAll(context.Background())

	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tools, err := manager.ListTools(checkCtx)
	snapshot := manager.Snapshot()[server.Name]
	if err != nil {
		report.State = "error"
		report.Checks = append(report.Checks, doctorCheck{Label: "tools/list", Status: "error", Details: err.Error()})
		return report
	}
	if snapshot.State == "failed" && snapshot.LastError != "" {
		report.State = "error"
		report.Checks = append(report.Checks, doctorCheck{Label: "tools/list", Status: "error", Details: snapshot.LastError})
		return report
	}
	report.ToolCount = len(tools)
	report.Checks = append(report.Checks, doctorCheck{Label: "tools/list", Status: "ok", Details: fmt.Sprintf("%d tools", len(tools))})
	return report
}

func requiredCommands(server config.Server) []string {
	required := []string{server.Command}
	switch server.Command {
	case "npx":
		required = append(required, "npm")
	case "uvx":
		required = append(required, "uv")
	}
	return required
}

func runStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	layout, resolvedConfig, err := resolveLayout(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}

	settings, settingsErr := state.LoadSettings(layout)
	if settingsErr != nil && !errors.Is(settingsErr, os.ErrNotExist) {
		fmt.Fprintf(stderr, "load settings: %v\n", settingsErr)
		return 1
	}

	cfg, cfgErr := config.LoadLenient(resolvedConfig)
	runtime, runtimeErr := state.LoadRuntimeStatus(layout.RuntimeStatusPath)
	if runtimeErr != nil && errors.Is(runtimeErr, os.ErrNotExist) {
		runtimeErr = nil
	}
	if runtimeErr != nil {
		fmt.Fprintf(stderr, "load runtime status: %v\n", runtimeErr)
		return 1
	}

	if runtime.Servers == nil {
		runtime.Servers = make(map[string]state.ServerRuntimeStatus)
	}
	if cfgErr == nil && len(runtime.Servers) == 0 {
		selected := selectServers(cfg, nil)
		for name, server := range selected.Servers {
			envState := make(map[string]string, len(server.EnvStatus))
			for key := range server.EnvStatus {
				envState[key] = "set"
			}
			runtime.Servers[name] = state.ServerRuntimeStatus{
				Name:      name,
				Command:   server.Command,
				Args:      server.Args,
				Cwd:       server.Cwd,
				Env:       envState,
				State:     "stopped",
				UpdatedAt: time.Now().UTC(),
			}
		}
		for name, errMsg := range selected.Errors {
			runtime.Servers[name] = state.ServerRuntimeStatus{
				Name:      name,
				State:     "failed",
				LastError: errMsg,
				UpdatedAt: time.Now().UTC(),
			}
		}
	}
	renderStatus(stdout, resolvedConfig, runtime, settings, readSystemdStatus())
	return 0
}

func renderDoctor(w io.Writer, configPath string, cfg *config.Config, reports []doctorServerReport) {
	total := len(reports)
	okCount := 0
	warnCount := 0
	errCount := 0
	for _, report := range reports {
		switch report.State {
		case "ok":
			okCount++
		case "warn":
			warnCount++
		default:
			errCount++
		}
	}

	fmt.Fprintf(w, "== mcpe doctor ==\nconfig: %s\nservers: %d  ok: %d  warn: %d  error: %d\n", configPath, total, okCount, warnCount, errCount)
	if cfg.EnvFile != "" {
		fmt.Fprintf(w, ".env: %s\n", cfg.EnvFile)
	}
	if cfg.Repaired {
		fmt.Fprintln(w, "config: auto-repaired before validation")
	}
	fmt.Fprintln(w)
	for _, report := range reports {
		fmt.Fprintf(w, "[%s] %s\n", statusLabel(report.State), report.Name)
		for _, check := range report.Checks {
			fmt.Fprintf(w, "  - [%s] %s: %s\n", statusLabel(check.Status), check.Label, check.Details)
		}
		if report.ToolCount > 0 {
			fmt.Fprintf(w, "  - tools discovered: %d\n", report.ToolCount)
		}
		fmt.Fprintln(w)
	}
}

func renderStatus(w io.Writer, configPath string, runtime state.RuntimeStatus, settings state.Settings, systemdStatus SystemdStatus) {
	fmt.Fprintf(w, "== mcpe status ==\nconfig: %s\n", configPath)
	if runtime.Gateway.PID > 0 {
		fmt.Fprintf(w, "\n[%s] gateway\n", statusLabel("ok"))
		fmt.Fprintf(w, "  pid: %d\n", runtime.Gateway.PID)
		fmt.Fprintf(w, "  addr: %s\n", runtime.Gateway.ListenAddr)
		fmt.Fprintf(w, "  started_at: %s\n", runtime.Gateway.StartedAt.Format(time.RFC3339))
	} else {
		fmt.Fprintf(w, "\n[%s] gateway\n", statusLabel("info"))
		fmt.Fprintln(w, "  state: not running")
	}
	fmt.Fprintf(w, "\n[%s] systemd\n", systemdStatusLabel(settings, systemdStatus))
	fmt.Fprintf(w, "  configured: %t\n", settings.SystemdEnabled)
	fmt.Fprintf(w, "  registered: %t\n", systemdStatus.Registered)
	fmt.Fprintf(w, "  enabled: %t\n", systemdStatus.Enabled)
	fmt.Fprintf(w, "  active: %t\n", systemdStatus.Active)
	fmt.Fprintf(w, "  service: %s\n", systemdStatus.ServiceName)
	if systemdStatus.StatusHint != "" {
		fmt.Fprintf(w, "  status_cmd: %s\n", systemdStatus.StatusHint)
	}
	names := mapKeys(runtime.Servers)
	for _, name := range names {
		server := runtime.Servers[name]
		fmt.Fprintf(w, "\n[%s] %s\n", statusLabel(server.State), name)
		if server.Command != "" {
			fmt.Fprintf(w, "  command: %s %s\n", server.Command, strings.Join(server.Args, " "))
		}
		if server.Cwd != "" {
			fmt.Fprintf(w, "  cwd: %s\n", server.Cwd)
		}
		if len(server.Env) > 0 {
			envNames := mapKeys(server.Env)
			parts := make([]string, 0, len(envNames))
			for _, envName := range envNames {
				parts = append(parts, fmt.Sprintf("%s=%s", envName, server.Env[envName]))
			}
			fmt.Fprintf(w, "  [%s] env: %s\n", statusLabel("ok"), strings.Join(parts, ", "))
		} else {
			fmt.Fprintf(w, "  [%s] env: no env bindings\n", statusLabel("info"))
		}
		fmt.Fprintf(w, "  installed: %t\n", server.Installed)
		if !server.LastStartAt.IsZero() {
			fmt.Fprintf(w, "  last_start_at: %s\n", server.LastStartAt.Format(time.RFC3339))
		}
		if server.LastError != "" {
			fmt.Fprintf(w, "  last_error: %s\n", server.LastError)
		}
		if server.InstallErr != "" {
			fmt.Fprintf(w, "  install_error: %s\n", server.InstallErr)
		}
		fmt.Fprintln(w)
	}
}

func statusLabel(status string) string {
	switch status {
	case "info":
		return "INFO"
	case "ok", "running":
		return "OK"
	case "warn", "starting", "stopped":
		return "WARN"
	default:
		return "ERR"
	}
}

func systemdStatusLabel(settings state.Settings, status SystemdStatus) string {
	if !status.Available {
		if settings.SystemdEnabled {
			return "WARN"
		}
		return "INFO"
	}
	if settings.SystemdEnabled && (!status.Registered || !status.Enabled) {
		return "ERR"
	}
	if status.Active {
		return "OK"
	}
	if status.Registered || settings.SystemdEnabled {
		return "WARN"
	}
	return "INFO"
}
