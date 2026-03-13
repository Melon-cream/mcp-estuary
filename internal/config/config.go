package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Melon-cream/mcp-estuary/internal/envfile"
)

var allowedCommands = map[string]struct{}{
	"docker": {},
	"npx":    {},
	"uvx":    {},
}

type Config struct {
	Path       string
	Servers    map[string]Server
	Errors     map[string]string
	Defined    map[string]struct{}
	EnvFile    string
	Repaired   bool
	RepairDiff string
}

type rawConfig struct {
	MCPServers map[string]rawServer `json:"mcpServers"`
}

type rawServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Cwd     string            `json:"cwd"`
}

type Server struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	EnvStatus map[string]EnvBinding
	Cwd       string
}

type EnvBinding struct {
	Status    string
	Source    string
	Reference string
}

func DefaultPath(cwd string) string {
	return filepath.Join(cwd, "mcpe.json")
}

func Load(path string) (*Config, error) {
	cfg, err := load(path, true)
	if err != nil {
		return nil, err
	}
	if len(cfg.Errors) > 0 {
		names := make([]string, 0, len(cfg.Errors))
		for name := range cfg.Errors {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s: %s", name, cfg.Errors[name]))
		}
		return nil, fmt.Errorf("config has invalid servers: %s", strings.Join(parts, "; "))
	}
	return cfg, nil
}

func LoadLenient(path string) (*Config, error) {
	return load(path, false)
}

func load(path string, strict bool) (*Config, error) {
	if path == "" {
		return nil, errors.New("config path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	normalized, repaired, diff, err := normalizeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if repaired {
		if err := os.WriteFile(absPath, normalized, 0o644); err != nil {
			return nil, fmt.Errorf("write repaired config: %w", err)
		}
	}

	var raw rawConfig
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg := &Config{
		Path:       absPath,
		Servers:    make(map[string]Server, len(raw.MCPServers)),
		Errors:     map[string]string{},
		Defined:    make(map[string]struct{}, len(raw.MCPServers)),
		Repaired:   repaired,
		RepairDiff: diff,
	}
	if len(raw.MCPServers) == 0 {
		return nil, errors.New("mcpServers must define at least one server")
	}

	configDir := filepath.Dir(absPath)
	dotEnv, envPath, err := envfile.Load(configDir)
	if err != nil {
		return nil, err
	}
	cfg.EnvFile = envPath

	for name, item := range raw.MCPServers {
		cfg.Defined[name] = struct{}{}
		server, err := resolveServer(name, item, configDir, dotEnv)
		if err != nil {
			cfg.Errors[name] = err.Error()
			continue
		}
		cfg.Servers[name] = server
	}

	if strict {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	if len(cfg.Servers) == 0 && len(cfg.Errors) > 0 {
		return cfg, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func resolveServer(name string, item rawServer, configDir string, dotEnv map[string]string) (Server, error) {
	server := Server{
		Name:      name,
		Command:   item.Command,
		Args:      append([]string(nil), item.Args...),
		Env:       make(map[string]string, len(item.Env)),
		EnvStatus: make(map[string]EnvBinding, len(item.Env)),
		Cwd:       item.Cwd,
	}
	if server.Cwd != "" && !filepath.IsAbs(server.Cwd) {
		server.Cwd = filepath.Join(configDir, server.Cwd)
	}

	for key, rawValue := range item.Env {
		resolved, binding, err := expandEnvValue(rawValue, dotEnv)
		if err != nil {
			return Server{}, fmt.Errorf("env %s: %w", key, err)
		}
		if resolved != "" && strings.HasSuffix(key, "_PATH") && !filepath.IsAbs(resolved) {
			resolved = filepath.Join(configDir, resolved)
			binding.Source = "config-relative-path"
		}
		server.Env[key] = resolved
		server.EnvStatus[key] = binding
	}
	if len(server.Env) == 0 {
		server.Env = nil
		server.EnvStatus = nil
	}
	if err := validateServer(server); err != nil {
		return Server{}, err
	}
	return server, nil
}

func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if len(c.Servers) == 0 {
		return errors.New("mcpServers must define at least one valid server")
	}
	for _, server := range c.Servers {
		if err := validateServer(server); err != nil {
			return err
		}
	}
	return nil
}

func validateServer(server Server) error {
	if server.Name == "" {
		return errors.New("server name must not be empty")
	}
	if _, ok := allowedCommands[server.Command]; !ok {
		return fmt.Errorf("server %q has unsupported command %q", server.Name, server.Command)
	}
	if len(server.Args) == 0 {
		return fmt.Errorf("server %q args must not be empty", server.Name)
	}
	if server.Cwd != "" {
		info, err := os.Stat(server.Cwd)
		if err != nil {
			return fmt.Errorf("server %q cwd: %w", server.Name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("server %q cwd must be a directory", server.Name)
		}
	}
	return nil
}

func (c *Config) Filter(names []string) (*Config, error) {
	if len(names) == 0 {
		copyCfg := &Config{
			Path:       c.Path,
			Servers:    make(map[string]Server, len(c.Servers)),
			Errors:     cloneErrorMap(c.Errors),
			Defined:    cloneSet(c.Defined),
			EnvFile:    c.EnvFile,
			Repaired:   c.Repaired,
			RepairDiff: c.RepairDiff,
		}
		for name, server := range c.Servers {
			copyCfg.Servers[name] = server
		}
		return copyCfg, nil
	}

	filtered := &Config{
		Path:       c.Path,
		Servers:    make(map[string]Server, len(names)),
		Errors:     make(map[string]string),
		Defined:    make(map[string]struct{}, len(names)),
		EnvFile:    c.EnvFile,
		Repaired:   c.Repaired,
		RepairDiff: c.RepairDiff,
	}
	for _, name := range names {
		filtered.Defined[name] = struct{}{}
		if server, ok := c.Servers[name]; ok {
			filtered.Servers[name] = server
			continue
		}
		if errMsg, ok := c.Errors[name]; ok {
			filtered.Errors[name] = errMsg
			continue
		}
		return nil, fmt.Errorf("unknown server %q in --use", name)
	}
	if len(filtered.Servers) == 0 && len(filtered.Errors) > 0 {
		return filtered, nil
	}
	return filtered, nil
}

func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func expandEnvValue(raw string, dotEnv map[string]string) (string, EnvBinding, error) {
	binding := EnvBinding{Status: "literal", Source: "config"}
	if raw == "" {
		binding.Status = "empty"
		return "", binding, nil
	}
	if !strings.Contains(raw, "${") {
		return raw, binding, nil
	}

	var unresolved string
	expanded := os.Expand(raw, func(key string) string {
		value, ok := envfile.Lookup(dotEnv, key)
		if !ok {
			unresolved = key
			return ""
		}
		binding.Status = "expanded"
		binding.Reference = key
		if _, ok := os.LookupEnv(key); ok {
			binding.Source = "process-env"
		} else {
			binding.Source = ".env"
		}
		return value
	})
	if unresolved != "" {
		return "", EnvBinding{Status: "missing", Source: "missing", Reference: unresolved}, fmt.Errorf("undefined variable %q", unresolved)
	}
	return expanded, binding, nil
}

func normalizeJSON(data []byte) ([]byte, bool, string, error) {
	trimmed := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(trimmed) {
		return nil, false, "", errors.New("config is not valid UTF-8")
	}
	if json.Valid(trimmed) {
		return append([]byte(nil), trimmed...), false, "", nil
	}
	repaired := stripTrailingCommas(trimmed)
	if !json.Valid(repaired) {
		return nil, false, "", errors.New("invalid JSON; automatic repair supports trailing commas only")
	}
	diff := buildRepairDiff(trimmed, repaired)
	return repaired, true, diff, nil
}

func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for ; j < len(data); j++ {
				if data[j] == ' ' || data[j] == '\n' || data[j] == '\r' || data[j] == '\t' {
					continue
				}
				break
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, ch)
	}
	return out
}

func buildRepairDiff(before, after []byte) string {
	beforeLines := strings.Split(strings.TrimRight(string(before), "\n"), "\n")
	afterLines := strings.Split(strings.TrimRight(string(after), "\n"), "\n")
	limit := len(beforeLines)
	if len(afterLines) > limit {
		limit = len(afterLines)
	}
	diff := make([]string, 0, limit*2)
	for i := 0; i < limit; i++ {
		var left string
		if i < len(beforeLines) {
			left = beforeLines[i]
		}
		var right string
		if i < len(afterLines) {
			right = afterLines[i]
		}
		if left == right {
			continue
		}
		if left != "" {
			diff = append(diff, "- "+left)
		}
		if right != "" {
			diff = append(diff, "+ "+right)
		}
	}
	if len(diff) == 0 {
		return ""
	}
	return strings.Join(diff, "\n")
}

func cloneErrorMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}
