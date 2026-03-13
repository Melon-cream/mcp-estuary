package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/gateway"
	"github.com/Melon-cream/mcp-estuary/internal/install"
	"github.com/Melon-cream/mcp-estuary/internal/logs"
	"github.com/Melon-cream/mcp-estuary/internal/process"
	"github.com/Melon-cream/mcp-estuary/internal/state"
)

const (
	defaultListenAddr         = ":8080"
	defaultInstallConcurrency = 2
)

func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "stop":
		return runStop(args[1:], stdout, stderr)
	case "logs":
		return runLogs(args[1:], stdout, stderr)
	case "cache":
		return runCache(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "--help", "-h", "help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func runServe(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	parsed, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "parse serve args: %v\n", err)
		return 1
	}
	layout, configPath, err := resolveLayout(parsed.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	parsed.configPath = configPath

	settings, err := state.LoadSettings(layout)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "load settings: %v\n", err)
		return 1
	}

	if !parsed.foreground {
		return runServeBackground(layout, parsed, settings, stdout, stderr)
	}
	return runServeForeground(ctx, layout, parsed, settings, stdout, stderr)
}

func runServeBackground(layout state.Layout, parsed serveArgs, settings state.Settings, stdout io.Writer, stderr io.Writer) int {
	if settings.SystemdEnabled {
		if err := layout.Ensure(); err != nil {
			fmt.Fprintf(stderr, "prepare state dirs: %v\n", err)
			return 1
		}
		if err := stopManagedGateway(layout); err != nil {
			fmt.Fprintf(stderr, "stop existing gateway before systemd start: %v\n", err)
			return 1
		}
		stopFollow := make(chan struct{})
		go func() {
			_ = logs.FollowFile(stdout, layout.GatewayLogPath, true, stopFollow)
		}()
		defer close(stopFollow)
		if err := startSystemdService(); err != nil {
			fmt.Fprintf(stderr, "start systemd service: %v\n", err)
			return 1
		}
		if err := waitForGatewayHealthy(parsed.listenAddr, nil); err != nil {
			_ = stopSystemdService()
			fmt.Fprintf(stderr, "systemd service did not become healthy: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "started %s via user systemd\n", systemdServiceName)
		fmt.Fprintf(stdout, "status: %s\n", readSystemdStatus().StatusHint)
		return 0
	}

	if err := layout.Ensure(); err != nil {
		fmt.Fprintf(stderr, "prepare state dirs: %v\n", err)
		return 1
	}

	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "resolve executable: %v\n", err)
		return 1
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "open %s: %v\n", os.DevNull, err)
		return 1
	}
	defer devNull.Close()

	cmdArgs := []string{"serve", "--foreground", "--config", parsed.configPath, "--listen", parsed.listenAddr}
	if parsed.installConcurrency > 0 {
		cmdArgs = append(cmdArgs, "--install-concurrency", strconv.Itoa(parsed.installConcurrency))
	}
	for _, name := range parsed.useServers {
		cmdArgs = append(cmdArgs, "--use", name)
	}
	cmd := exec.Command(executable, cmdArgs...)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Env = append(os.Environ(), "MCPE_INTERNAL_FOREGROUND=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stopFollow := make(chan struct{})
	go func() {
		_ = logs.FollowFile(stdout, layout.GatewayLogPath, true, stopFollow)
	}()
	defer close(stopFollow)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "start background process: %v\n", err)
		return 1
	}

	if err := waitForGatewayHealthy(parsed.listenAddr, cmd.Process); err == nil {
		fmt.Fprintf(stdout, "gateway is ready in background pid=%d addr=%s\n", cmd.Process.Pid, parsed.listenAddr)
		return 0
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		fmt.Fprintf(stderr, "background process failed before readiness: %v\n", err)
		return 1
	}
}

func runServeForeground(ctx context.Context, layout state.Layout, parsed serveArgs, settings state.Settings, stdout io.Writer, stderr io.Writer) int {
	if err := layout.Ensure(); err != nil {
		fmt.Fprintf(stderr, "prepare state dirs: %v\n", err)
		return 1
	}

	gatewayLogger, gatewayLogFile, err := logs.NewFileLogger(layout.GatewayLogPath, stdout, "[gateway] ")
	if err != nil {
		fmt.Fprintf(stderr, "open gateway log: %v\n", err)
		return 1
	}
	defer gatewayLogFile.Close()

	cfg, err := config.LoadLenient(parsed.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	if cfg.Repaired && cfg.RepairDiff != "" {
		gatewayLogger.Printf("auto-repaired mcpe.json\n%s", cfg.RepairDiff)
	}

	selected := selectServers(cfg, parsed.useServers)
	if len(parsed.useServers) > 0 && len(selected.Errors) > 0 {
		fmt.Fprintf(stderr, "select servers: %s\n", formatErrorMap(selected.Errors))
		return 1
	}
	if len(selected.Servers) == 0 {
		fmt.Fprintf(stderr, "load config: no valid servers selected")
		if len(selected.Errors) > 0 {
			fmt.Fprintf(stderr, " (%s)", formatErrorMap(selected.Errors))
		}
		fmt.Fprintln(stderr)
		return 1
	}

	if err := state.EnsureServerLogSymlink(parsed.configPath, layout); err != nil {
		gatewayLogger.Printf("create log symlink: %v", err)
	}

	concurrency := resolveInstallConcurrency(parsed.installConcurrency, settings.InstallConcurrency)
	if envValue := os.Getenv("INSTALL_CONCURRENCY"); envValue != "" {
		if parsedValue, err := strconv.Atoi(envValue); err == nil && parsedValue > 0 {
			concurrency = parsedValue
		}
	}

	if err := state.PruneManagedWorkdirs(layout, selected.Names()); err != nil {
		fmt.Fprintf(stderr, "prune stale workdirs: %v\n", err)
		return 1
	}

	workDirs, logPaths, requests := buildRequests(layout, selected.Servers)
	gatewayLogger.Printf("recognized servers=%d selected=%d installConcurrency=%d", len(cfg.Defined), len(selected.Servers), concurrency)
	if len(selected.Errors) > 0 {
		gatewayLogger.Printf("skipped invalid servers: %s", formatErrorMap(selected.Errors))
	}
	installs := install.Run(ctx, requests, concurrency, gatewayLogger)

	runtime := state.RuntimeStatus{
		Gateway: state.GatewayStatus{
			PID:        os.Getpid(),
			ListenAddr: parsed.listenAddr,
			ConfigPath: parsed.configPath,
			StartedAt:  time.Now().UTC(),
			Mode:       "foreground",
		},
	}
	publish := func(snapshot map[string]state.ServerRuntimeStatus) {
		runtime.Servers = snapshot
		if err := state.SaveRuntimeStatus(layout.RuntimeStatusPath, runtime); err != nil && gatewayLogger != nil {
			gatewayLogger.Printf("save runtime status: %v", err)
		}
	}

	manager := process.NewManager(selected.Servers, installs, workDirs, logPaths, gatewayLogger, publish)
	_ = manager.Reconcile(ctx, selected.Servers, installs, workDirs, logPaths, selected.Defined, selected.Errors)

	httpGateway := gateway.NewServer(gatewayLogger, manager)
	server := &http.Server{
		Addr:              parsed.listenAddr,
		Handler:           httpGateway.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := state.SavePID(layout.GatewayPIDPath, state.PIDFile{
		PID:        os.Getpid(),
		ListenAddr: parsed.listenAddr,
		StartedAt:  runtime.Gateway.StartedAt,
		ConfigPath: parsed.configPath,
	}); err != nil {
		fmt.Fprintf(stderr, "write pid file: %v\n", err)
		return 1
	}
	defer state.RemovePID(layout.GatewayPIDPath)
	defer state.RemoveRuntimeStatus(layout.RuntimeStatusPath)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go watchConfig(sigCtx, gatewayLogger, layout, parsed, concurrency, manager)

	serverErr := make(chan error, 1)
	go func() {
		gatewayLogger.Printf("http gateway listening addr=%s", parsed.listenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = manager.StopAll(shutdownCtx)
		gatewayLogger.Printf("gateway stopped")
		return 0
	case err := <-serverErr:
		if err != nil {
			fmt.Fprintf(stderr, "gateway failed: %v\n", err)
			return 1
		}
		return 0
	}
}

func runStop(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	layout, _, err := resolveLayout(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	settings, _ := state.LoadSettings(layout)
	if settings.SystemdEnabled {
		if err := stopSystemdService(); err == nil {
			fmt.Fprintln(stdout, "stopped mcpe.service")
			return 0
		}
	}
	pidFile, err := state.LoadPID(layout.GatewayPIDPath)
	if err != nil {
		fmt.Fprintf(stderr, "load pid file: %v\n", err)
		return 1
	}
	if err := state.SignalProcess(pidFile.PID, syscall.SIGTERM); err != nil {
		fmt.Fprintf(stderr, "stop gateway: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sent SIGTERM to gateway pid=%d\n", pidFile.PID)
	return 0
}

func runLogs(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	serverName := fs.String("server", "", "")
	follow := fs.Bool("follow", false, "")
	followShort := fs.Bool("f", false, "")
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	layout, _, err := resolveLayout(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	target := layout.GatewayLogPath
	if *serverName != "" {
		target = layout.ServerLogPath(*serverName)
	}
	if err := logs.CopyFileTo(stdout, target); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "read log: %v\n", err)
		return 1
	}
	if *follow || *followShort {
		stop := make(chan struct{})
		defer close(stop)
		if err := logs.FollowFile(stdout, target, true, stop); err != nil {
			fmt.Fprintf(stderr, "follow log: %v\n", err)
			return 1
		}
	}
	return 0
}

func runCache(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "clean" {
		fmt.Fprintln(stderr, "usage: mcpe cache clean")
		return 1
	}
	layout, _, err := resolveLayout("")
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	if err := state.CleanCache(layout); err != nil {
		fmt.Fprintf(stderr, "clean cache: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "cache cleaned")
	return 0
}

func runConfig(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		fmt.Fprintln(stderr, "usage: mcpe config set --install-concurrency N | --systemd enable|disable [--config PATH] [--listen ADDR]")
		return 1
	}
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	value := fs.Int("install-concurrency", 0, "")
	systemdMode := fs.String("systemd", "", "")
	configPath := fs.String("config", "", "")
	listenAddr := fs.String("listen", defaultListenAddr, "")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	layout, resolvedConfig, err := resolveLayout(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	settings, err := state.LoadSettings(layout)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "load settings: %v\n", err)
		return 1
	}
	if *value > 0 {
		settings.InstallConcurrency = *value
	}
	switch *systemdMode {
	case "":
	case "enable":
		settings.SystemdEnabled = true
		if err := stopManagedGateway(layout); err != nil {
			fmt.Fprintf(stderr, "stop existing gateway before enabling systemd: %v\n", err)
			return 1
		}
		if err := enableSystemdService(resolvedConfig, *listenAddr); err != nil {
			fmt.Fprintf(stderr, "enable systemd: %v\n", err)
			return 1
		}
	case "disable":
		settings.SystemdEnabled = false
		if err := disableSystemdService(); err != nil {
			fmt.Fprintf(stderr, "disable systemd: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintln(stderr, "--systemd must be enable or disable")
		return 1
	}
	if settings.InstallConcurrency <= 0 {
		settings.InstallConcurrency = defaultInstallConcurrency
	}
	if err := state.SaveSettings(layout, settings); err != nil {
		fmt.Fprintf(stderr, "save settings: %v\n", err)
		return 1
	}
	switch *systemdMode {
	case "enable":
		fmt.Fprintf(stdout, "registered %s in user systemd\n", systemdServiceName)
		fmt.Fprintf(stdout, "status: %s\n", readSystemdStatus().StatusHint)
	case "disable":
		fmt.Fprintln(stdout, "systemd service disabled")
	default:
		fmt.Fprintf(stdout, "install concurrency set to %d\n", settings.InstallConcurrency)
	}
	return 0
}

type serveArgs struct {
	configPath         string
	listenAddr         string
	installConcurrency int
	useServers         []string
	foreground         bool
}

func parseServeArgs(args []string) (serveArgs, error) {
	normalized, useServers, err := extractUseArgs(args)
	if err != nil {
		return serveArgs{}, err
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	listenAddr := fs.String("listen", defaultListenAddr, "")
	installConcurrency := fs.Int("install-concurrency", 0, "")
	foreground := fs.Bool("foreground", false, "")
	if err := fs.Parse(normalized); err != nil {
		return serveArgs{}, err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return serveArgs{}, fmt.Errorf("unexpected arguments: %s", strings.Join(extra, " "))
	}
	return serveArgs{
		configPath:         *configPath,
		listenAddr:         *listenAddr,
		installConcurrency: *installConcurrency,
		useServers:         useServers,
		foreground:         *foreground || os.Getenv("MCPE_INTERNAL_FOREGROUND") == "1",
	}, nil
}

func extractUseArgs(args []string) ([]string, []string, error) {
	normalized := make([]string, 0, len(args))
	useServers := make([]string, 0)
	for i := 0; i < len(args); i++ {
		if args[i] != "--use" {
			normalized = append(normalized, args[i])
			continue
		}
		if i+1 >= len(args) {
			return nil, nil, errors.New("--use requires at least one server name")
		}
		i++
		for ; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") {
				i--
				break
			}
			for _, item := range strings.Split(args[i], ",") {
				item = strings.TrimSpace(item)
				if item != "" {
					useServers = append(useServers, item)
				}
			}
		}
	}
	return normalized, useServers, nil
}

func resolveInstallConcurrency(flagValue, settingsValue int) int {
	if flagValue > 0 {
		return flagValue
	}
	if settingsValue > 0 {
		return settingsValue
	}
	return defaultInstallConcurrency
}

type selectedConfig struct {
	Servers map[string]config.Server
	Errors  map[string]string
	Defined map[string]struct{}
}

func (s selectedConfig) Names() []string {
	names := make([]string, 0, len(s.Servers))
	for name := range s.Servers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

func selectServers(cfg *config.Config, names []string) selectedConfig {
	selected := selectedConfig{
		Servers: make(map[string]config.Server),
		Errors:  make(map[string]string),
		Defined: make(map[string]struct{}),
	}
	if len(names) == 0 {
		for name, server := range cfg.Servers {
			selected.Servers[name] = server
		}
		for name, errMsg := range cfg.Errors {
			selected.Errors[name] = errMsg
		}
		for name := range cfg.Defined {
			selected.Defined[name] = struct{}{}
		}
		return selected
	}
	for _, name := range names {
		selected.Defined[name] = struct{}{}
		if server, ok := cfg.Servers[name]; ok {
			selected.Servers[name] = server
			continue
		}
		if errMsg, ok := cfg.Errors[name]; ok {
			selected.Errors[name] = errMsg
			continue
		}
		selected.Errors[name] = "not defined in config"
	}
	return selected
}

func buildRequests(layout state.Layout, servers map[string]config.Server) (map[string]string, map[string]string, []install.Request) {
	workDirs := make(map[string]string, len(servers))
	logPaths := make(map[string]string, len(servers))
	requests := make([]install.Request, 0, len(servers))
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		server := servers[name]
		workDir := layout.ServerWorkDir(name)
		if server.Cwd != "" {
			workDir = server.Cwd
		}
		workDirs[name] = workDir
		logPaths[name] = layout.ServerLogPath(name)
		requests = append(requests, install.Request{Server: server, WorkDir: workDir, LogPath: logPaths[name]})
	}
	return workDirs, logPaths, requests
}

func watchConfig(ctx context.Context, logger *log.Logger, layout state.Layout, parsed serveArgs, concurrency int, manager *process.Manager) {
	info, err := os.Stat(parsed.configPath)
	if err != nil {
		logger.Printf("watch config stat failed: %v", err)
		return
	}
	lastMod := info.ModTime()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(parsed.configPath)
			if err != nil {
				logger.Printf("watch config stat failed: %v", err)
				continue
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			lastMod = info.ModTime()
			cfg, err := config.LoadLenient(parsed.configPath)
			if err != nil {
				logger.Printf("config reload failed: %v", err)
				continue
			}
			if cfg.Repaired && cfg.RepairDiff != "" {
				logger.Printf("auto-repaired mcpe.json on reload\n%s", cfg.RepairDiff)
			}
			selected := selectServers(cfg, parsed.useServers)
			if len(selected.Servers) == 0 {
				logger.Printf("config reload skipped: no valid servers selected (%s)", formatErrorMap(selected.Errors))
				continue
			}
			workDirs, logPaths, requests := buildRequests(layout, selected.Servers)
			installs := install.Run(ctx, requests, concurrency, logger)
			if err := manager.Reconcile(ctx, selected.Servers, installs, workDirs, logPaths, selected.Defined, selected.Errors); err != nil {
				logger.Printf("config reconcile failed: %v", err)
				continue
			}
			if err := state.PruneManagedWorkdirs(layout, mapKeys(manager.Snapshot())); err != nil {
				logger.Printf("prune stale workdirs after reload: %v", err)
			}
			logger.Printf("config reloaded selected=%d invalid=%d", len(selected.Servers), len(selected.Errors))
		}
	}
}

func resolveLayout(configPath string) (state.Layout, string, error) {
	if configPath == "" {
		cwd, _ := os.Getwd()
		configPath = config.DefaultPath(cwd)
	}
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return state.Layout{}, "", err
	}
	home, err := state.ResolveHome(absConfigPath)
	if err != nil {
		return state.Layout{}, "", err
	}
	return state.NewLayout(home), absConfigPath, nil
}

func gatewayHealthURL(listenAddr string) string {
	host := listenAddr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host + "/healthz"
}

func waitForGatewayHealthy(listenAddr string, process *os.Process) error {
	client := &http.Client{Timeout: time.Second}
	healthURL := gatewayHealthURL(listenAddr)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if process != nil {
			if err := process.Signal(syscall.Signal(0)); err != nil {
				return errors.New("process exited before becoming healthy")
			}
		}
	}
	return errors.New("timed out waiting for /healthz")
}

func stopManagedGateway(layout state.Layout) error {
	pidFile, err := state.LoadPID(layout.GatewayPIDPath)
	if err != nil {
		return nil
	}
	if pidFile.PID <= 0 {
		return nil
	}
	if err := state.SignalProcess(pidFile.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := state.SignalProcess(pidFile.PID, syscall.Signal(0)); err != nil {
			_ = state.RemovePID(layout.GatewayPIDPath)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("gateway pid=%d did not stop within timeout", pidFile.PID)
}

func formatErrorMap(errors map[string]string) string {
	if len(errors) == 0 {
		return ""
	}
	names := mapKeys(errors)
	sortStrings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%s", name, errors[name]))
	}
	return strings.Join(parts, ", ")
}

func mapKeys[T any](in map[string]T) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `mcpe is a minimal MCP gateway for streamable HTTP.

Usage:
  mcpe serve [--config PATH] [--use NAME ...] [--install-concurrency N] [--listen ADDR] [--foreground]
  mcpe stop [--config PATH]
  mcpe logs [--server NAME] [--follow]
  mcpe doctor [--config PATH] [--use NAME ...]
  mcpe status [--config PATH]
  mcpe cache clean
  mcpe config set --install-concurrency N
  mcpe config set --systemd enable|disable [--config PATH] [--listen ADDR]
  mcpe --help
`)
}
