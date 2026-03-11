package logs

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

func NewFileLogger(path string, stdout io.Writer, prefix string) (*log.Logger, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	writer := io.Writer(file)
	if stdout != nil {
		writer = io.MultiWriter(stdout, file)
	}
	return log.New(writer, prefix, log.LstdFlags|log.Lmicroseconds), file, nil
}

func CopyFileTo(dst io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(dst, file)
	return err
}
