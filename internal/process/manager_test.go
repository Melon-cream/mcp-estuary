package process

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/install"
)

func TestServerHandleKeepsProcessAliveAfterRequestContextEnds(t *testing.T) {
	t.Setenv("PATH", prependPath(t, writeFakeNPX(t)))

	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "server.log")
	handle := &ServerHandle{
		server: config.Server{
			Name:    "fake",
			Command: "npx",
			Args:    []string{"fake-server"},
		},
		workDir: workDir,
		logPath: logPath,
		logger:  log.New(os.Stderr, "", 0),
		install: install.Result{Name: "fake", Installed: true},
	}

	listCtx, cancelList := context.WithCancel(context.Background())
	tools, err := handle.ListTools(listCtx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	cancelList()

	if len(tools) != 1 || tools[0].Name != "fake__echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	callCtx, cancelCall := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCall()
	result, err := handle.CallTool(callCtx, "fake__echo", map[string]any{"message": "ok"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0]["text"] != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := handle.Stop(stopCtx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func writeFakeNPX(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "npx")
	script := `#!/bin/sh
count=0
while IFS= read -r line; do
  count=$((count + 1))
  case "$count" in
    1)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fake","version":"1.0.0"}}}'
      ;;
    2)
      ;;
    3)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","title":"Echo"}]}}'
      ;;
    4)
      printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ok"}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}
	return binDir
}

func prependPath(t *testing.T, dir string) string {
	t.Helper()

	current := os.Getenv("PATH")
	if current == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + strings.TrimSpace(current)
}
