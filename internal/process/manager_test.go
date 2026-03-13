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

func TestServerHandleListToolsUsesTransientProcessAndCachesResults(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	t.Setenv("PATH", prependPath(t, writeFakeNPX(t)))

	handle := newTestHandle(t, startLog)
	handle.idleTimeout = 50 * time.Millisecond

	tools, err := handle.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fake__echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	if got := handle.Snapshot().State; got != "stopped" {
		t.Fatalf("expected stopped after transient tools/list, got %s", got)
	}

	starts := readStarts(t, startLog)
	if starts != 1 {
		t.Fatalf("expected 1 process start after tools/list, got %d", starts)
	}

	tools, err = handle.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list cached tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fake__echo" {
		t.Fatalf("unexpected cached tools: %+v", tools)
	}
	if starts = readStarts(t, startLog); starts != 1 {
		t.Fatalf("expected cached tools/list to avoid restart, got %d starts", starts)
	}
}

func TestServerHandleCallToolStartsOnDemandAndStopsAfterIdle(t *testing.T) {
	startLog := filepath.Join(t.TempDir(), "starts.log")
	t.Setenv("PATH", prependPath(t, writeFakeNPX(t)))

	handle := newTestHandle(t, startLog)
	handle.idleTimeout = 50 * time.Millisecond

	callCtx, cancelCall := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCall()
	result, err := handle.CallTool(callCtx, "fake__echo", map[string]any{"message": "ok"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0]["text"] != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := handle.Snapshot().State; got != "running" {
		t.Fatalf("expected running immediately after tools/call, got %s", got)
	}
	if starts := readStarts(t, startLog); starts != 1 {
		t.Fatalf("expected 1 process start after tools/call, got %d", starts)
	}

	waitForState(t, handle, "stopped", time.Second)
}

func newTestHandle(t *testing.T, startLog string) *ServerHandle {
	t.Helper()

	workDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "server.log")
	return &ServerHandle{
		server: config.Server{
			Name:    "fake",
			Command: "npx",
			Args:    []string{"fake-server"},
			Env: map[string]string{
				"START_LOG_PATH": startLog,
			},
		},
		workDir:     workDir,
		logPath:     logPath,
		logger:      log.New(os.Stderr, "", 0),
		install:     install.Result{Name: "fake", Installed: true},
		state:       "stopped",
		idleTimeout: defaultIdleTimeout,
	}
}

func writeFakeNPX(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "npx")
	script := `#!/bin/sh
if [ -n "$START_LOG_PATH" ]; then
  printf 'start\n' >> "$START_LOG_PATH"
fi
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fake","version":"1.0.0"}}}\n' "$id"
      ;;
    *'"method":"notifications/initialized"'*)
      ;;
    *'"method":"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","title":"Echo"}]}}\n' "$id"
      ;;
    *'"method":"tools/call"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}\n' "$id"
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

func readStarts(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read starts: %v", err)
	}
	return strings.Count(strings.TrimSpace(string(data)), "start")
}

func waitForState(t *testing.T, handle *ServerHandle, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := handle.Snapshot().State; got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %s, got %s", want, handle.Snapshot().State)
}
