package install

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Melon-cream/mcp-estuary/internal/config"
)

type Result struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Skipped   bool   `json:"skipped"`
	Error     string `json:"error,omitempty"`
}

type Request struct {
	Server  config.Server
	WorkDir string
	LogPath string
}

func Run(ctx context.Context, requests []Request, concurrency int, logger *log.Logger) map[string]Result {
	if concurrency <= 0 {
		concurrency = 2
	}
	results := make(map[string]Result, len(requests))
	if len(requests) == 0 {
		return results
	}
	sort.Slice(requests, func(i, j int) bool {
		return requests[i].Server.Name < requests[j].Server.Name
	})
	jobs := make(chan Request)
	type outcome struct {
		name   string
		result Result
	}
	outcomes := make(chan outcome, len(requests))
	var workers sync.WaitGroup
	workerCount := concurrency
	if workerCount > len(requests) {
		workerCount = len(requests)
	}
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for request := range jobs {
				outcomes <- outcome{name: request.Server.Name, result: installOne(ctx, request, logger)}
			}
		}()
	}
	go func() {
		for _, request := range requests {
			jobs <- request
		}
		close(jobs)
		workers.Wait()
		close(outcomes)
	}()
	for outcome := range outcomes {
		results[outcome.name] = outcome.result
	}
	return results
}

func installOne(ctx context.Context, request Request, logger *log.Logger) Result {
	server := request.Server
	result := Result{Name: server.Name}
	if err := os.MkdirAll(request.WorkDir, 0o755); err != nil {
		result.Error = fmt.Sprintf("create workdir: %v", err)
		return result
	}
	cmdName, args, env, skip, err := BuildInstallCommand(server, request.WorkDir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if skip {
		result.Installed = true
		result.Skipped = true
		if logger != nil {
			logger.Printf("install skipped server=%s runtime=%s", server.Name, server.Command)
		}
		return result
	}
	logFile, err := os.OpenFile(request.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		result.Error = fmt.Sprintf("open install log: %v", err)
		return result
	}
	defer logFile.Close()
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = request.WorkDir
	cmd.Env = mergeEnv(os.Environ(), env, server.Env)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if logger != nil {
		logger.Printf("install start server=%s runtime=%s", server.Name, server.Command)
	}
	if err := cmd.Run(); err != nil {
		result.Error = fmt.Sprintf("install command failed: %v", err)
		return result
	}
	result.Installed = true
	return result
}

func BuildInstallCommand(server config.Server, workDir string) (string, []string, map[string]string, bool, error) {
	switch server.Command {
	case "docker":
		return "", nil, nil, true, nil
	case "npx":
		pkg, err := detectNPXPackage(server.Args)
		if err != nil {
			return "", nil, nil, false, err
		}
		return "npm", []string{"install", "--no-save", pkg}, map[string]string{
			"npm_config_cache": filepath.Join(workDir, ".npm-cache"),
		}, false, nil
	case "uvx":
		pkg, err := detectUVXPackage(server.Args)
		if err != nil {
			return "", nil, nil, false, err
		}
		return "uv", []string{"tool", "install", "--force", pkg}, map[string]string{
			"UV_TOOL_DIR": filepath.Join(workDir, ".uv-tools"),
		}, false, nil
	default:
		return "", nil, nil, false, fmt.Errorf("unsupported command %q", server.Command)
	}
}

func BuildRunCommand(server config.Server, workDir string) (string, []string, map[string]string, error) {
	switch server.Command {
	case "docker":
		return server.Command, append([]string(nil), server.Args...), nil, nil
	case "npx":
		args := append([]string(nil), server.Args...)
		if !containsExact(args, "--prefix") {
			args = append([]string{"--prefix", workDir}, args...)
		}
		return server.Command, args, map[string]string{
			"npm_config_cache": filepath.Join(workDir, ".npm-cache"),
		}, nil
	case "uvx":
		return server.Command, append([]string(nil), server.Args...), map[string]string{
			"UV_TOOL_DIR": filepath.Join(workDir, ".uv-tools"),
		}, nil
	default:
		return "", nil, nil, fmt.Errorf("unsupported command %q", server.Command)
	}
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
	merged := make([]string, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, key+"="+values[key])
	}
	return merged
}

func detectNPXPackage(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--package", "-p", "--cache", "--userconfig":
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i], nil
	}
	return "", errors.New("could not determine package for npx server")
}

func detectUVXPackage(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "-f":
			if i+1 >= len(args) {
				return "", errors.New("uvx --from requires a value")
			}
			return args[i+1], nil
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		return args[i], nil
	}
	return "", errors.New("could not determine package for uvx server")
}

func containsExact(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
