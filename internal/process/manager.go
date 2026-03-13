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
	"time"

	"github.com/Melon-cream/mcp-estuary/internal/config"
	"github.com/Melon-cream/mcp-estuary/internal/install"
	"github.com/Melon-cream/mcp-estuary/internal/mcp"
	"github.com/Melon-cream/mcp-estuary/internal/state"
)

type Manager struct {
	logger   *log.Logger
	mu       sync.RWMutex
	servers  map[string]*ServerHandle
	installs map[string]install.Result
	invalid  map[string]state.ServerRuntimeStatus
	onChange func(map[string]state.ServerRuntimeStatus)
}

type ServerHandle struct {
	server  config.Server
	workDir string
	logPath string
	logger  *log.Logger
	install install.Result

	mu          sync.Mutex
	client      *mcp.Client
	toolMap     map[string]string
	processStop context.CancelFunc
	processLog  *os.File
	state       string
	lastStartAt time.Time
	lastError   string
}

func NewManager(servers map[string]config.Server, installs map[string]install.Result, workDirs map[string]string, logPaths map[string]string, logger *log.Logger, onChange func(map[string]state.ServerRuntimeStatus)) *Manager {
	handles := make(map[string]*ServerHandle, len(servers))
	for name, server := range servers {
		handles[name] = newHandle(server, installs[name], workDirs[name], logPaths[name], logger)
	}
	manager := &Manager{
		logger:   logger,
		servers:  handles,
		installs: cloneInstalls(installs),
		invalid:  map[string]state.ServerRuntimeStatus{},
		onChange: onChange,
	}
	manager.publish()
	return manager
}

func newHandle(server config.Server, result install.Result, workDir string, logPath string, logger *log.Logger) *ServerHandle {
	status := "stopped"
	lastError := ""
	if !result.Installed {
		status = "failed"
		lastError = result.Error
	}
	return &ServerHandle{
		server:    server,
		workDir:   workDir,
		logPath:   logPath,
		logger:    logger,
		install:   result,
		state:     status,
		lastError: lastError,
	}
}

func (m *Manager) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	handles := m.handlesSnapshot()
	all := make([]mcp.Tool, 0)
	for _, handle := range handles {
		tools, err := handle.ListTools(ctx)
		if err != nil {
			if m.logger != nil {
				m.logger.Printf("tools list skipped server=%s error=%v", handle.server.Name, err)
			}
			m.publish()
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
	m.mu.RLock()
	handle, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return toolError(fmt.Sprintf("unknown server %q", serverName)), nil
	}
	result, err := handle.CallTool(ctx, name, args)
	m.publish()
	return result, err
}

func (m *Manager) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	failed := make([]string, 0)
	installed := 0
	running := 0
	for name, handle := range m.servers {
		snapshot := handle.Snapshot()
		if snapshot.Installed {
			installed++
		}
		if snapshot.State == "running" {
			running++
		}
		if snapshot.LastError != "" {
			failed = append(failed, fmt.Sprintf("%s: %s", name, snapshot.LastError))
		}
	}
	for name, snapshot := range m.invalid {
		failed = append(failed, fmt.Sprintf("%s: %s", name, snapshot.LastError))
	}
	sort.Strings(failed)
	return map[string]any{
		"configuredServers": len(m.servers) + len(m.invalid),
		"installedServers":  installed,
		"runningServers":    running,
		"failedServers":     failed,
	}
}

func (m *Manager) StopAll(ctx context.Context) error {
	handles := m.handlesSnapshot()
	var firstErr error
	for _, handle := range handles {
		if err := handle.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.publish()
	return firstErr
}

func (m *Manager) Reconcile(ctx context.Context, servers map[string]config.Server, installs map[string]install.Result, workDirs map[string]string, logPaths map[string]string, defined map[string]struct{}, invalid map[string]string) error {
	m.mu.Lock()
	toStop := make([]*ServerHandle, 0)
	for name, handle := range m.servers {
		if _, ok := defined[name]; ok {
			continue
		}
		toStop = append(toStop, handle)
		delete(m.servers, name)
		delete(m.installs, name)
	}
	for name := range m.invalid {
		if _, ok := defined[name]; ok {
			continue
		}
		delete(m.invalid, name)
	}
	for name, errMsg := range invalid {
		if handle, ok := m.servers[name]; ok {
			handle.RecordConfigError(errMsg)
			delete(m.invalid, name)
			continue
		}
		m.invalid[name] = state.ServerRuntimeStatus{
			Name:      name,
			State:     "failed",
			LastError: errMsg,
			UpdatedAt: time.Now().UTC(),
		}
	}
	for name, server := range servers {
		result := installs[name]
		if existing, ok := m.servers[name]; ok {
			if existing.SameConfig(server, result, workDirs[name], logPaths[name]) {
				existing.UpdateInstall(result)
				delete(m.invalid, name)
				m.installs[name] = result
				continue
			}
			toStop = append(toStop, existing)
		}
		m.servers[name] = newHandle(server, result, workDirs[name], logPaths[name], m.logger)
		m.installs[name] = result
		delete(m.invalid, name)
	}
	m.mu.Unlock()

	for _, handle := range toStop {
		_ = handle.Stop(ctx)
	}
	m.publish()
	return nil
}

func (m *Manager) Snapshot() map[string]state.ServerRuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]state.ServerRuntimeStatus, len(m.servers)+len(m.invalid))
	for name, handle := range m.servers {
		out[name] = handle.Snapshot()
	}
	for name, snapshot := range m.invalid {
		out[name] = snapshot
	}
	return out
}

func (m *Manager) handlesSnapshot() []*ServerHandle {
	m.mu.RLock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	handles := make([]*ServerHandle, 0, len(names))
	for _, name := range names {
		handles = append(handles, m.servers[name])
	}
	m.mu.RUnlock()
	return handles
}

func (m *Manager) publish() {
	if m.onChange == nil {
		return
	}
	m.onChange(m.Snapshot())
}

func (h *ServerHandle) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	if !h.install.Installed {
		h.setState("failed", h.install.Error, time.Time{})
		return nil, fmt.Errorf("server %s is unavailable: %s", h.server.Name, h.install.Error)
	}
	client, err := h.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	var result mcp.ListToolsResult
	if err := client.Call(ctx, "tools/list", mcp.ListToolsParams{}, &result); err != nil {
		h.setState("failed", err.Error(), time.Time{})
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
	h.setState("running", "", time.Time{})
	return tools, nil
}

func (h *ServerHandle) CallTool(ctx context.Context, prefixedName string, args map[string]any) (mcp.CallToolResult, error) {
	if !h.install.Installed {
		h.setState("failed", h.install.Error, time.Time{})
		return toolError(fmt.Sprintf("server %s is unavailable: %s", h.server.Name, h.install.Error)), nil
	}
	client, err := h.ensureStarted(ctx)
	if err != nil {
		h.setState("failed", err.Error(), time.Time{})
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
		h.setState("failed", err.Error(), time.Time{})
		return toolError(err.Error()), nil
	}
	h.setState("running", "", time.Time{})
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
	h.toolMap = nil
	if h.install.Installed {
		h.state = "stopped"
	} else {
		h.state = "failed"
	}
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
	h.state = "starting"
	h.mu.Unlock()

	logFile, err := os.OpenFile(h.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.setState("failed", fmt.Sprintf("open log file: %v", err), time.Time{})
		return nil, fmt.Errorf("open log file: %w", err)
	}
	cmdName, args, runtimeEnv, err := install.BuildRunCommand(h.server, h.workDir)
	if err != nil {
		logFile.Close()
		h.setState("failed", err.Error(), time.Time{})
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
		h.setState("failed", fmt.Sprintf("stdout pipe: %v", err), time.Time{})
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		processStop()
		logFile.Close()
		h.setState("failed", fmt.Sprintf("stdin pipe: %v", err), time.Time{})
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	client, err := mcp.Start(processCtx, cmd, stdout, stdin)
	if err != nil {
		processStop()
		logFile.Close()
		h.setState("failed", err.Error(), time.Time{})
		return nil, err
	}
	startedAt := time.Now().UTC()
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		processStop()
		logFile.Close()
		h.setState("failed", fmt.Sprintf("initialize server: %v", err), time.Time{})
		return nil, fmt.Errorf("initialize server: %w", err)
	}
	h.mu.Lock()
	if h.client == nil {
		h.client = client
		h.processStop = processStop
		h.processLog = logFile
		h.state = "running"
		h.lastStartAt = startedAt
		h.lastError = ""
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

func (h *ServerHandle) Snapshot() state.ServerRuntimeStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	env := make(map[string]string, len(h.server.EnvStatus))
	for key, meta := range h.server.EnvStatus {
		status := meta.Status
		if status == "expanded" || status == "literal" || status == "empty" {
			status = "set"
		}
		env[key] = status
	}
	return state.ServerRuntimeStatus{
		Name:        h.server.Name,
		Command:     h.server.Command,
		Args:        append([]string(nil), h.server.Args...),
		Cwd:         h.server.Cwd,
		Env:         env,
		Installed:   h.install.Installed,
		InstallSkip: h.install.Skipped,
		InstallErr:  h.install.Error,
		State:       h.state,
		LastStartAt: h.lastStartAt,
		LastError:   h.lastError,
		UpdatedAt:   time.Now().UTC(),
	}
}

func (h *ServerHandle) SameConfig(server config.Server, result install.Result, workDir string, logPath string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.server.Command != server.Command || h.server.Cwd != server.Cwd || h.workDir != workDir || h.logPath != logPath {
		return false
	}
	if !equalStrings(h.server.Args, server.Args) {
		return false
	}
	if !equalStringMap(h.server.Env, server.Env) {
		return false
	}
	return h.install == result
}

func (h *ServerHandle) UpdateInstall(result install.Result) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.install = result
	if !result.Installed {
		h.state = "failed"
		h.lastError = result.Error
	}
}

func (h *ServerHandle) RecordConfigError(message string) {
	h.setState("", "config reload skipped: "+message, time.Time{})
}

func (h *ServerHandle) setState(nextState string, lastError string, startedAt time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if nextState != "" {
		h.state = nextState
	}
	if !startedAt.IsZero() {
		h.lastStartAt = startedAt
	}
	if lastError != "" {
		h.lastError = lastError
	}
	if lastError == "" && nextState == "running" {
		h.lastError = ""
	}
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

func cloneInstalls(in map[string]install.Result) map[string]install.Result {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]install.Result, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalStringMap(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
