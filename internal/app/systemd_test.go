package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateServiceExecutablePathRejectsGoRunBinary(t *testing.T) {
	t.Parallel()

	path := filepath.Join(os.TempDir(), "go-build", "1234", "b001", "exe", "mcpe")
	if _, err := validateServiceExecutablePath(path); err == nil {
		t.Fatal("expected go run temporary binary to be rejected")
	}
}

func TestValidateServiceExecutablePathAcceptsStableBinary(t *testing.T) {
	t.Parallel()

	path := filepath.Join("/usr", "local", "bin", "mcpe")
	got, err := validateServiceExecutablePath(path)
	if err != nil {
		t.Fatalf("validateServiceExecutablePath() error = %v", err)
	}
	if got != path {
		t.Fatalf("validateServiceExecutablePath() = %q, want %q", got, path)
	}
}

func TestBuildSystemdUnitContentUsesAbsoluteWorkingDirectoryWithoutQuotes(t *testing.T) {
	t.Parallel()

	content := buildSystemdUnitContent("/home/user/mcp-estuary/.tmp/mcpe", "/home/user/mcp-estuary/.tmp", "/home/user/mcp-estuary/.tmp/mcpe.json", "127.0.0.1:8080")
	if !strings.Contains(content, "WorkingDirectory=/home/user/mcp-estuary/.tmp\n") {
		t.Fatalf("unexpected WorkingDirectory entry: %s", content)
	}
	if strings.Contains(content, `WorkingDirectory="/home/user/mcp-estuary/.tmp"`) {
		t.Fatalf("WorkingDirectory should not be quoted: %s", content)
	}
}
