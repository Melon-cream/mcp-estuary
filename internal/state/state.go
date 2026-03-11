package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

const defaultInstallConcurrency = 2

type Layout struct {
	Home           string
	RunDir         string
	ConfigDir      string
	LogDir         string
	ServerLogDir   string
	ServerWorkRoot string
	SettingsPath   string
	GatewayPIDPath string
	GatewayLogPath string
}

type Settings struct {
	InstallConcurrency int `json:"installConcurrency"`
}

type PIDFile struct {
	PID        int       `json:"pid"`
	ListenAddr string    `json:"listenAddr"`
	StartedAt  time.Time `json:"startedAt"`
}

func ResolveHome() (string, error) {
	if home := os.Getenv("MCPE_HOME"); home != "" {
		return filepath.Abs(home)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(userHome, ".mcp-estuary"), nil
}

func NewLayout(home string) Layout {
	return Layout{
		Home:           home,
		RunDir:         filepath.Join(home, "run"),
		ConfigDir:      filepath.Join(home, "config"),
		LogDir:         filepath.Join(home, "logs"),
		ServerLogDir:   filepath.Join(home, "logs", "servers"),
		ServerWorkRoot: filepath.Join(home, "mcp-servers"),
		SettingsPath:   filepath.Join(home, "config", "settings.json"),
		GatewayPIDPath: filepath.Join(home, "run", "gateway.pid"),
		GatewayLogPath: filepath.Join(home, "logs", "gateway.log"),
	}
}

func (l Layout) Ensure() error {
	for _, dir := range []string{l.Home, l.RunDir, l.ConfigDir, l.LogDir, l.ServerLogDir, l.ServerWorkRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

func (l Layout) ServerWorkDir(name string) string {
	return filepath.Join(l.ServerWorkRoot, name)
}

func (l Layout) ServerLogPath(name string) string {
	return filepath.Join(l.ServerLogDir, name+".log")
}

func LoadSettings(layout Layout) (Settings, error) {
	settings := Settings{InstallConcurrency: defaultInstallConcurrency}
	data, err := os.ReadFile(layout.SettingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return settings, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, fmt.Errorf("parse settings: %w", err)
	}
	if settings.InstallConcurrency <= 0 {
		settings.InstallConcurrency = defaultInstallConcurrency
	}
	return settings, nil
}

func SaveSettings(layout Layout, settings Settings) error {
	if settings.InstallConcurrency <= 0 {
		return errors.New("install concurrency must be greater than zero")
	}
	if err := layout.Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(layout.SettingsPath, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func SavePID(path string, pid PIDFile) error {
	data, err := json.MarshalIndent(pid, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pid: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	return nil
}

func LoadPID(path string) (PIDFile, error) {
	var pid PIDFile
	data, err := os.ReadFile(path)
	if err != nil {
		return pid, fmt.Errorf("read pid: %w", err)
	}
	if err := json.Unmarshal(data, &pid); err != nil {
		return pid, fmt.Errorf("parse pid: %w", err)
	}
	if pid.PID <= 0 {
		return pid, errors.New("pid file is invalid")
	}
	return pid, nil
}

func RemovePID(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func SignalProcess(pid int, signal syscall.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

func PruneManagedWorkdirs(layout Layout, keepNames []string) error {
	entries, err := os.ReadDir(layout.ServerWorkRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read workdirs: %w", err)
	}
	keep := make(map[string]struct{}, len(keepNames))
	for _, name := range keepNames {
		keep[name] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := keep[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(layout.ServerWorkRoot, entry.Name())); err != nil {
			return fmt.Errorf("remove stale workdir %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func CleanCache(layout Layout) error {
	if err := os.RemoveAll(layout.ServerWorkRoot); err != nil {
		return fmt.Errorf("remove %s: %w", layout.ServerWorkRoot, err)
	}
	return os.MkdirAll(layout.ServerWorkRoot, 0o755)
}

func SortedServerLogs(layout Layout) ([]string, error) {
	entries, err := os.ReadDir(layout.ServerLogDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}
