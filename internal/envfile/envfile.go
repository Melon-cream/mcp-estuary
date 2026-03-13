package envfile

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Load(dir string) (map[string]string, string, error) {
	if dir == "" {
		return nil, "", nil
	}
	path := filepath.Join(dir, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read .env: %w", err)
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, "", fmt.Errorf("parse .env line %d: missing '='", lineNo)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, "", fmt.Errorf("parse .env line %d: empty key", lineNo)
		}
		value = strings.TrimSpace(value)
		if unquoted, err := unquote(value); err == nil {
			value = unquoted
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("scan .env: %w", err)
	}
	return values, path, nil
}

func Lookup(values map[string]string, key string) (string, bool) {
	if value, ok := os.LookupEnv(key); ok {
		return value, true
	}
	value, ok := values[key]
	return value, ok
}

func unquote(value string) (string, error) {
	if len(value) < 2 {
		return value, nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return strings.ReplaceAll(strings.Trim(value, "\""), `\n`, "\n"), nil
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.Trim(value, "'"), nil
	}
	return value, nil
}
