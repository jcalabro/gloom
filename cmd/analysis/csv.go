package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
)

const resultsDir = "../../results"

type csvWriter struct {
	file *os.File
	w    *csv.Writer
}

func newCSVWriter(name string) (*csvWriter, error) {
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating results dir: %w", err)
	}
	path := filepath.Join(resultsDir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", path, err)
	}
	return &csvWriter{file: f, w: csv.NewWriter(f)}, nil
}

func (c *csvWriter) WriteHeader(fields ...string) error {
	return c.w.Write(fields)
}

func (c *csvWriter) WriteRow(values ...any) error {
	row := make([]string, len(values))
	for i, v := range values {
		switch val := v.(type) {
		case float64:
			row[i] = fmt.Sprintf("%.10g", val)
		default:
			row[i] = fmt.Sprintf("%v", val)
		}
	}
	return c.w.Write(row)
}

func (c *csvWriter) Close() error {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		c.file.Close()
		return err
	}
	return c.file.Close()
}
