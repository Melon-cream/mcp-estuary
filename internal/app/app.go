package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
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

const defaultListenAddr = ":8080"

func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	home, err := state.ResolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "resolve state home: %v\n", err)
		return 1
	}
	layout := state.NewLayout(home)

	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, layout, args[1:], stdout, stderr)
	case "stop":
		return runStop(layout, stdout, stderr)
	case "servers":
		return runServers(ctx, layout, args[1:], stdout, stderr)
	case "logs":
		return runLogs(layout, args[1:], stdout, stderr)
	case "cache":
		return runCache(layout, args[1:], stdout, stderr)
	case "config":
		return runConfig(layout, args[1:], stdout, stderr)
	case "--help", "-h", "help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func runServe(ctx context.Context, layout state.Layout, args []string, stdout io.Writer, stderr io.Writer) int {
	parsed, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "parse serve args: %v\n", err)
		return 1
	}
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

	cfgPath := parsed.configPath
	if cfgPath == "" {
		cwd, _ := os.Getwd()
		cfgPath = config.DefaultPath(cwd)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	selected, err := cfg.Filter(parsed.useServers)
	if err != nil {
		fmt.Fprintf(stderr, "select servers: %v\n", err)
		return 1
	}

	settings, err := state.LoadSettings(layout)
	if err != nil {
		fmt.Fprintf(stderr, "load settings: %v\n", err)
		return 1
	}
	concurrency := resolveInstallConcurrency(parsed.installConcurrency, settings.InstallConcurrency)
	if envValue := os.Getenv("INSTALL_CONCURRENCY"); envValue != "" {
		if parsedValue, err := strconv.Atoi(envValue); err == nil && parsedValue > 0 {
			concurrency = parsedValue
		}
	}

	if err := state.PruneManagedWorkdirs(layout, cfg.Names()); err != nil {
		fmt.Fprintf(stderr, "prune stale workdirs: %v\n", err)
		return 1
	}

	workDirs := make(map[string]string, len(selected.Servers))
	logPaths := make(map[string]string, len(selected.Servers))
	requests := make([]install.Request, 0, len(selected.Servers))
	for _, name := range selected.Names() {
		server := selected.Servers[name]
		workDir := layout.ServerWorkDir(name)
		if server.Cwd != "" {
			workDir = server.Cwd
		}
		workDirs[name] = workDir
		logPaths[name] = layout.ServerLogPath(name)
		requests = append(requests, install.Request{Server: server, WorkDir: workDir, LogPath: logPaths[name]})
	}

	gatewayLogger.Printf("recognized servers=%d selected=%d installConcurrency=%d", len(cfg.Servers), len(selected.Servers), concurrency)
	installs := install.Run(ctx, requests, concurrency, gatewayLogger)

	manager := process.NewManager(selected.Servers, installs, workDirs, logPaths, gatewayLogger)
	httpGateway := gateway.NewServer(gatewayLogger, manager)
	server := &http.Server{
		Addr:              parsed.listenAddr,
		Handler:           httpGateway.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := state.SavePID(layout.GatewayPIDPath, state.PIDFile{
		PID:        os.Getpid(),
		ListenAddr: parsed.listenAddr,
		StartedAt:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(stderr, "write pid file: %v\n", err)
		return 1
	}
	defer state.RemovePID(layout.GatewayPIDPath)

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func runStop(layout state.Layout, stdout io.Writer, stderr io.Writer) int {
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

func runServers(ctx context.Context, layout state.Layout, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "usage: mcpe servers list [--config PATH]")
		return 1
	}
	fs := flag.NewFlagSet("servers list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	if *configPath == "" {
		cwd, _ := os.Getwd()
		*configPath = config.DefaultPath(cwd)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	for _, name := range cfg.Names() {
		server := cfg.Servers[name]
		cwd := server.Cwd
		if cwd == "" {
			cwd = layout.ServerWorkDir(name)
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", name, server.Command, cwd)
	}
	_ = ctx
	return 0
}

func runLogs(layout state.Layout, args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	serverName := fs.String("server", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	target := layout.GatewayLogPath
	if *serverName != "" {
		target = layout.ServerLogPath(*serverName)
	}
	if err := logs.CopyFileTo(stdout, target); err != nil {
		fmt.Fprintf(stderr, "read log: %v\n", err)
		return 1
	}
	return 0
}

func runCache(layout state.Layout, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "clean" {
		fmt.Fprintln(stderr, "usage: mcpe cache clean")
		return 1
	}
	if err := state.CleanCache(layout); err != nil {
		fmt.Fprintf(stderr, "clean cache: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "cache cleaned")
	return 0
}

func runConfig(layout state.Layout, args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		fmt.Fprintln(stderr, "usage: mcpe config set --install-concurrency N")
		return 1
	}
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	value := fs.Int("install-concurrency", 0, "")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}
	if *value <= 0 {
		fmt.Fprintln(stderr, "--install-concurrency must be greater than zero")
		return 1
	}
	if err := state.SaveSettings(layout, state.Settings{InstallConcurrency: *value}); err != nil {
		fmt.Fprintf(stderr, "save settings: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "install concurrency set to %d\n", *value)
	return 0
}

type serveArgs struct {
	configPath         string
	listenAddr         string
	installConcurrency int
	useServers         []string
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
	return 2
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `mcpe is an MCP gateway for streamable HTTP.

Usage:
  mcpe serve [--config PATH] [--use NAME ...] [--install-concurrency N] [--listen ADDR]
  mcpe stop
  mcpe servers list [--config PATH]
  mcpe logs [--server NAME]
  mcpe cache clean
  mcpe config set --install-concurrency N
  mcpe --help
`)
}
