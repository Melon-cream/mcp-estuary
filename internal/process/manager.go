package process

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/install"
	"github.com/Melon-cream/mcp-estuary/internal/mcp"
)

type Manager struct {
	logger   *log.Logger
	servers  map[string]*ServerHandle
	installs map[string]install.Result
}

type ServerHandle struct {
	server  config.Server
	workDir string
	logPath string
	logger  *log.Logger
	install install.Result

	mu           sync.Mutex
	client       *mcp.Client
	toolMap      map[string]string
	processStop  context.CancelFunc
	processLog   *os.File
}

func NewManager(servers map[string]config.Server, installs map[string]install.Result, workDirs map[string]string, logPaths map[string]string, logger *log.Logger) *Manager {
	handles := make(map[string]*ServerHandle, len(servers))
	for name, server := range servers {
		handles[name] = &ServerHandle{
			server:  server,
			workDir: workDirs[name],
			logPath: logPaths[name],
			logger:  logger,
			install: installs[name],
		}
	}
	return &Manager{logger: logger, servers: handles, installs: installs}
}

func (m *Manager) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	all := make([]mcp.Tool, 0)
	for _, name := range names {
		tools, err := m.servers[name].ListTools(ctx)
		if err != nil {
			if m.logger != nil {
				m.logger.Printf("tools list skipped server=%s error=%v", name, err)
			}
			continue
		}
		all = append(all, tools...)
	}
	return all, nil
}

func (m *Manager) CallTool(ctx context.Context, name string, args map[string]any) (mcp.CallToolResult, error) {
	serverName, _, ok := strings.Cut(name, "__")
	if !ok || serverName == "" {
		return toolError("tool name must use <server>__<tool> format"), nil
	}
	handle, ok := m.servers[serverName]
	if !ok {
		return toolError(fmt.Sprintf("unknown server %q", serverName)), nil
	}
	return handle.CallTool(ctx, name, args)
}

func (m *Manager) Stats() map[string]any {
	failed := make([]string, 0)
	installed := 0
	for name, result := range m.installs {
		if result.Installed {
			installed++
			continue
		}
		failed = append(failed, fmt.Sprintf("%s: %s", name, result.Error))
	}
	sort.Strings(failed)
	return map[string]any{
		"configuredServers": len(m.servers),
		"installedServers":  installed,
		"failedServers":     failed,
	}
}

func (m *Manager) StopAll(ctx context.Context) error {
	var firstErr error
	for _, handle := range m.servers {
		if err := handle.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *ServerHandle) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	if !h.install.Installed {
		return nil, fmt.Errorf("server %s is unavailable: %s", h.server.Name, h.install.Error)
	}
	client, err := h.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	var result mcp.ListToolsResult
	if err := client.Call(ctx, "tools/list", mcp.ListToolsParams{}, &result); err != nil {
		return nil, err
	}
	tools := make([]mcp.Tool, 0, len(result.Tools))
	toolMap := make(map[string]string, len(result.Tools))
	for _, tool := range result.Tools {
		prefixed := h.server.Name + "__" + tool.Name
		toolMap[prefixed] = tool.Name
		meta := cloneMap(tool.Meta)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["upstreamServer"] = h.server.Name
		meta["upstreamToolName"] = tool.Name
		tool.Name = prefixed
		tool.Meta = meta
		if tool.Title == "" {
			tool.Title = prefixed
		}
		tools = append(tools, tool)
	}
	h.mu.Lock()
	h.toolMap = toolMap
	h.mu.Unlock()
	return tools, nil
}

func (h *ServerHandle) CallTool(ctx context.Context, prefixedName string, args map[string]any) (mcp.CallToolResult, error) {
	if !h.install.Installed {
		return toolError(fmt.Sprintf("server %s is unavailable: %s", h.server.Name, h.install.Error)), nil
	}
	client, err := h.ensureStarted(ctx)
	if err != nil {
		return toolError(fmt.Sprintf("server %s failed to start: %v", h.server.Name, err)), nil
	}
	h.mu.Lock()
	upstreamName := h.toolMap[prefixedName]
	h.mu.Unlock()
	if upstreamName == "" {
		upstreamName = strings.TrimPrefix(prefixedName, h.server.Name+"__")
	}
	var result mcp.CallToolResult
	if err := client.Call(ctx, "tools/call", mcp.CallToolParams{Name: upstreamName, Arguments: args}, &result); err != nil {
		return toolError(err.Error()), nil
	}
	return result, nil
}

func (h *ServerHandle) Stop(ctx context.Context) error {
	h.mu.Lock()
	client := h.client
	stop := h.processStop
	logFile := h.processLog
	h.client = nil
	h.processStop = nil
	h.processLog = nil
	h.mu.Unlock()
	if stop != nil {
		stop()
	}
	if logFile != nil {
		defer logFile.Close()
	}
	if client == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- client.Close()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (h *ServerHandle) ensureStarted(ctx context.Context) (*mcp.Client, error) {
	h.mu.Lock()
	if h.client != nil {
		client := h.client
		h.mu.Unlock()
		return client, nil
	}
	h.mu.Unlock()

	logFile, err := os.OpenFile(h.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	cmdName, args, runtimeEnv, err := install.BuildRunCommand(h.server, h.workDir)
	if err != nil {
		logFile.Close()
		return nil, err
	}
	processCtx, processStop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processCtx, cmdName, args...)
	if h.server.Cwd != "" {
		cmd.Dir = h.server.Cwd
	} else {
		cmd.Dir = h.workDir
	}
	cmd.Env = mergeEnv(os.Environ(), runtimeEnv, h.server.Env)
	cmd.Stderr = logFile
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		processStop()
		logFile.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		processStop()
		logFile.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	client, err := mcp.Start(processCtx, cmd, stdout, stdin)
	if err != nil {
		processStop()
		logFile.Close()
		return nil, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		processStop()
		logFile.Close()
		return nil, fmt.Errorf("initialize server: %w", err)
	}
	h.mu.Lock()
	if h.client == nil {
		h.client = client
		h.processStop = processStop
		h.processLog = logFile
		h.mu.Unlock()
		return client, nil
	}
	active := h.client
	h.mu.Unlock()
	processStop()
	_ = client.Close()
	logFile.Close()
	return active, nil
}

func toolError(message string) mcp.CallToolResult {
	return mcp.CallToolResult{
		Content: []map[string]any{{"type": "text", "text": message}},
		IsError: true,
	}
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeEnv(base []string, overrides ...map[string]string) []string {
	values := make(map[string]string, len(base))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for _, override := range overrides {
		for key, value := range override {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
