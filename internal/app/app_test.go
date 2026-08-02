package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/state"
)

func TestParseServeArgsSupportsUseList(t *testing.T) {
	t.Parallel()

	opts, err := parseServeArgs([]string{
		"--config", "alt.json",
		"--use", "fetch", "github",
		"--listen", "0.0.0.0:8080",
		"--install-concurrency", "4",
	})
	if err != nil {
		t.Fatalf("parseServeArgs() error = %v", err)
	}

	if opts.configPath != "alt.json" {
		t.Fatalf("configPath = %q, want alt.json", opts.configPath)
	}
	if opts.listenAddr != "0.0.0.0:8080" {
		t.Fatalf("listenAddr = %q, want 0.0.0.0:8080", opts.listenAddr)
	}
	if opts.installConcurrency != 4 {
		t.Fatalf("installConcurrency = %d, want 4", opts.installConcurrency)
	}
	if len(opts.useServers) != 2 || opts.useServers[0] != "fetch" || opts.useServers[1] != "github" {
		t.Fatalf("useServers = %#v, want [fetch github]", opts.useServers)
	}
}

func TestResolveConcurrencyPrecedence(t *testing.T) {
	t.Parallel()

	if got := resolveInstallConcurrency(7, 3); got != 7 {
		t.Fatalf("cli should win, got %d", got)
	}
	if got := resolveInstallConcurrency(0, 3); got != 3 {
		t.Fatalf("saved config should be used, got %d", got)
	}
	if got := resolveInstallConcurrency(0, 0); got != 2 {
		t.Fatalf("default should be used, got %d", got)
	}
}

func TestSelectServersMarksUnknownRequestedServerAsError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Servers: map[string]config.Server{
			"fetch": {Name: "fetch", Command: "uvx", Args: []string{"mcp-server-fetch"}},
		},
		Defined: map[string]struct{}{"fetch": {}},
	}

	selected := selectServers(cfg, []string{"fetch", "missing"})
	if _, ok := selected.Servers["fetch"]; !ok {
		t.Fatal("expected fetch to be selected")
	}
	if got := selected.Errors["missing"]; got == "" {
		t.Fatal("expected missing server error")
	}
}

func TestStatusLabelMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   string
	}{
		{"info", "INFO"},
		{"ok", "OK"},
		{"running", "OK"},
		{"available", "OK"},
		{"starting", "WARN"},
		{"failed", "ERR"},
		{"", "ERR"},
		{"unknown", "INFO"},
	}
	for _, tt := range tests {
		if got := statusLabel(tt.status); got != tt.want {
			t.Fatalf("statusLabel(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestDoctorLabelMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   string
	}{
		{"info", "INFO"},
		{"ok", "OK"},
		{"running", "OK"},
		{"warn", "WARN"},
		{"starting", "WARN"},
		{"stopped", "WARN"},
		{"error", "ERR"},
		{"", "ERR"},
	}
	for _, tt := range tests {
		if got := doctorLabel(tt.status); got != tt.want {
			t.Fatalf("doctorLabel(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestRenderStatusShowsAvailableAsOK(t *testing.T) {
	t.Parallel()

	runtime := state.RuntimeStatus{
		Servers: map[string]state.ServerRuntimeStatus{
			"fetch": {
				Name:  "fetch",
				State: "available",
				Env:   map[string]string{},
			},
		},
	}
	settings := state.Settings{SystemdEnabled: true}
	systemd := SystemdStatus{
		ServiceName: "mcpe.service",
		Available:   true,
		Registered:  true,
		Enabled:     true,
		Active:      true,
	}

	var out bytes.Buffer
	renderStatus(&out, "/tmp/config.json", runtime, settings, systemd)
	got := out.String()

	if !strings.Contains(got, "[INFO] gateway") || !strings.Contains(got, "state: not running") {
		t.Fatalf("gateway not-running block missing:\n%s", got)
	}
	if !strings.Contains(got, "[OK] systemd") {
		t.Fatalf("systemd should be shown as OK:\n%s", got)
	}
	if !strings.Contains(got, "[INFO] env: no env bindings") {
		t.Fatalf("env no-bindings message missing:\n%s", got)
	}
	if !strings.Contains(got, "[OK] fetch") {
		t.Fatalf("available server should be shown as OK:\n%s", got)
	}
}

func TestRenderStatusShowsUnknownAsInfo(t *testing.T) {
	t.Parallel()

	runtime := state.RuntimeStatus{
		Servers: map[string]state.ServerRuntimeStatus{
			"fetch": {
				Name:  "fetch",
				State: "unknown",
				Env:   map[string]string{},
			},
		},
	}
	settings := state.Settings{SystemdEnabled: true}
	systemd := SystemdStatus{
		ServiceName: "mcpe.service",
		Available:   true,
		Registered:  true,
		Enabled:     true,
		Active:      true,
	}

	var out bytes.Buffer
	renderStatus(&out, "/tmp/config.json", runtime, settings, systemd)
	got := out.String()

	if !strings.Contains(got, "[INFO] fetch") {
		t.Fatalf("unknown server should be shown as INFO:\n%s", got)
	}
	if !strings.Contains(got, "  installed: false") {
		t.Fatalf("installed should remain false:\n%s", got)
	}
}

func TestRenderDoctorKeepsLabels(t *testing.T) {
	t.Parallel()

	report := doctorServerReport{
		Name:      "fetch",
		State:     "warn",
		ToolCount: 2,
		Checks: []doctorCheck{
			{Label: "cwd", Status: "warn", Details: "isolated temp cwd"},
			{Label: "install", Status: "ok", Details: "ready"},
			{Label: "command npx", Status: "error", Details: "not found in PATH"},
			{Label: "env", Status: "info", Details: "no env bindings"},
		},
	}
	cfg := &config.Config{}

	var out bytes.Buffer
	renderDoctor(&out, "/tmp/config.json", cfg, []doctorServerReport{report})
	got := out.String()

	wants := []string{
		"[WARN] fetch",
		"  - [WARN] cwd: isolated temp cwd",
		"  - [OK] install: ready",
		"  - [ERR] command npx: not found in PATH",
		"  - [INFO] env: no env bindings",
		"  - tools discovered: 2",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
