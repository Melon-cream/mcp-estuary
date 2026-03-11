package app

import "testing"

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
