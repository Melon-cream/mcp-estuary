package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHomeReadsMCPEHomeFromDotEnvNearConfig(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "mcpe.json")
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("MCPE_HOME=.mcpe-state\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	home, err := ResolveHome(configPath)
	if err != nil {
		t.Fatalf("ResolveHome() error = %v", err)
	}
	want := filepath.Join(configDir, ".mcpe-state")
	if home != want {
		t.Fatalf("ResolveHome() = %q, want %q", home, want)
	}
}

func TestEnsureServerLogSymlinkRefusesToReplaceExistingFile(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "mcpe.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	layout := NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatalf("layout.Ensure() error = %v", err)
	}
	linkPath := filepath.Join(configDir, "mcp-servers-logs")
	if err := os.WriteFile(linkPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write conflict file: %v", err)
	}

	if err := EnsureServerLogSymlink(configPath, layout); err == nil {
		t.Fatal("expected conflicting path to be rejected")
	}
	data, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("read conflict file: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing file was modified: %q", data)
	}
}
