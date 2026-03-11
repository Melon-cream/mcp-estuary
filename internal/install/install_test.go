package install

import (
	"path/filepath"
	"testing"

	"github.com/Melon-cream/mcp-estuary/internal/config"
)

func TestBuildInstallCommandForNPX(t *testing.T) {
	server := config.Server{
		Name:    "thinking",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
	}

	cmd, args, env, skip, err := BuildInstallCommand(server, "/tmp/work")
	if err != nil {
		t.Fatalf("BuildInstallCommand returned error: %v", err)
	}
	if skip {
		t.Fatal("expected npx install not to be skipped")
	}
	if cmd != "npm" {
		t.Fatalf("unexpected command: %s", cmd)
	}
	if len(args) != 3 || args[2] != "@modelcontextprotocol/server-sequential-thinking" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if got := env["npm_config_cache"]; got != filepath.Join("/tmp/work", ".npm-cache") {
		t.Fatalf("unexpected npm cache: %s", got)
	}
}

func TestBuildRunCommandForDocker(t *testing.T) {
	server := config.Server{
		Name:    "docker-server",
		Command: "docker",
		Args:    []string{"run", "-i", "--rm", "ghcr.io/example/server"},
	}

	cmd, args, env, err := BuildRunCommand(server, "/tmp/work")
	if err != nil {
		t.Fatalf("BuildRunCommand returned error: %v", err)
	}
	if cmd != "docker" {
		t.Fatalf("unexpected command: %s", cmd)
	}
	if len(args) != 4 {
		t.Fatalf("unexpected args: %#v", args)
	}
	if env != nil {
		t.Fatalf("expected nil env, got %#v", env)
	}
}
