package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsUnsupportedCommand(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]Server{
			"bad": {Name: "bad", Command: "python", Args: []string{"server.py"}},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unsupported command")
	}
}

func TestValidateRejectsEmptyArgs(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]Server{
			"bad": {Name: "bad", Command: "npx"},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for empty args")
	}
}

func TestFilterRejectsUnknownServer(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]Server{
			"fetch": {Name: "fetch", Command: "uvx", Args: []string{"mcp-server-fetch"}},
		},
	}

	if _, err := cfg.Filter([]string{"missing"}); err == nil {
		t.Fatal("expected unknown server error")
	}
}

func TestLoadResolvesRelativePathEnvAgainstConfigDir(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(configDir, ".memory"), 0o755); err != nil {
		t.Fatalf("mkdir .memory: %v", err)
	}

	configPath := filepath.Join(configDir, "mcpe.json")
	configBody := `{
  "mcpServers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
      "env": {
        "MEMORY_FILE_PATH": ".memory/memory.json",
        "TAVILY_API_KEY": "token"
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	server := cfg.Servers["memory"]
	if got, want := server.Env["MEMORY_FILE_PATH"], filepath.Join(configDir, ".memory", "memory.json"); got != want {
		t.Fatalf("MEMORY_FILE_PATH=%q want %q", got, want)
	}
	if got := server.Env["TAVILY_API_KEY"]; got != "token" {
		t.Fatalf("TAVILY_API_KEY=%q want token", got)
	}
}

func TestLoadExpandsEnvFromDotEnvBeforeResolvingPath(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("MEMORY_FILE=.cache/memory.json\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	configPath := filepath.Join(configDir, "mcpe.json")
	configBody := `{
  "mcpServers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
      "env": {
        "MEMORY_FILE_PATH": "${MEMORY_FILE}"
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := cfg.Servers["memory"].Env["MEMORY_FILE_PATH"]
	want := filepath.Join(configDir, ".cache", "memory.json")
	if got != want {
		t.Fatalf("MEMORY_FILE_PATH=%q want %q", got, want)
	}
}

func TestLoadRejectsUndefinedExpandedEnv(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "mcpe.json")
	configBody := `{
  "mcpServers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
      "env": {
        "MEMORY_FILE_PATH": "${MISSING_VALUE}"
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil {
		t.Fatal("expected missing env expansion to fail")
	}
}

func TestLoadAutoRepairsTrailingComma(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "mcpe.json")
	configBody := `{
  "mcpServers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"],
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Repaired {
		t.Fatal("expected config to be marked as repaired")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read repaired config: %v", err)
	}
	if string(data) == configBody {
		t.Fatal("expected repaired config to be written back")
	}
}
