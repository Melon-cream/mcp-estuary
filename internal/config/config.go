package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var allowedCommands = map[string]struct{}{
	"docker": {},
	"npx":    {},
	"uvx":    {},
}

type Config struct {
	Path    string
	Servers map[string]Server
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
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string
}

func DefaultPath(cwd string) string {
	return filepath.Join(cwd, "mcpe.json")
}

func Load(path string) (*Config, error) {
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

	var raw rawConfig
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if len(raw.MCPServers) == 0 {
		return nil, errors.New("mcpServers must define at least one server")
	}

	cfg := &Config{
		Path:    absPath,
		Servers: make(map[string]Server, len(raw.MCPServers)),
	}

	configDir := filepath.Dir(absPath)
	for name, item := range raw.MCPServers {
		server := Server{
			Name:    name,
			Command: item.Command,
			Args:    append([]string(nil), item.Args...),
			Env:     cloneMap(item.Env),
			Cwd:     item.Cwd,
		}
		if server.Cwd != "" && !filepath.IsAbs(server.Cwd) {
			server.Cwd = filepath.Join(configDir, server.Cwd)
		}
		server.Env = resolvePathEnv(server.Env, configDir)
		cfg.Servers[name] = server
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if len(c.Servers) == 0 {
		return errors.New("mcpServers must define at least one server")
	}

	for name, server := range c.Servers {
		if name == "" {
			return errors.New("server name must not be empty")
		}
		if _, ok := allowedCommands[server.Command]; !ok {
			return fmt.Errorf("server %q has unsupported command %q", name, server.Command)
		}
		if len(server.Args) == 0 {
			return fmt.Errorf("server %q args must not be empty", name)
		}
		if server.Cwd != "" {
			info, err := os.Stat(server.Cwd)
			if err != nil {
				return fmt.Errorf("server %q cwd: %w", name, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("server %q cwd must be a directory", name)
			}
		}
	}
	return nil
}

func (c *Config) Filter(names []string) (*Config, error) {
	if len(names) == 0 {
		copyCfg := &Config{
			Path:    c.Path,
			Servers: make(map[string]Server, len(c.Servers)),
		}
		for name, server := range c.Servers {
			copyCfg.Servers[name] = server
		}
		return copyCfg, nil
	}

	filtered := &Config{
		Path:    c.Path,
		Servers: make(map[string]Server, len(names)),
	}
	for _, name := range names {
		server, ok := c.Servers[name]
		if !ok {
			return nil, fmt.Errorf("unknown server %q in --use", name)
		}
		filtered.Servers[name] = server
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

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func resolvePathEnv(env map[string]string, baseDir string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	resolved := cloneMap(env)
	for key, value := range resolved {
		if value == "" || filepath.IsAbs(value) || !strings.HasSuffix(key, "_PATH") {
			continue
		}
		resolved[key] = filepath.Join(baseDir, value)
	}
	return resolved
}
