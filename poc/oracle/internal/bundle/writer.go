/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Writer incrementally writes redacted run artifacts with owner-only modes.
type Writer struct {
	dir     string
	secrets []string
	mu      sync.Mutex
}

// NewWriter creates a bundle directory and retains secrets only for redaction.
func NewWriter(dir string, secrets []string) (*Writer, error) {
	if dir == "" {
		return nil, fmt.Errorf("bundle directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create bundle directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict bundle directory: %w", err)
	}
	return &Writer{dir: dir, secrets: append([]string(nil), secrets...)}, nil
}

// Dir returns the bundle root.
func (w *Writer) Dir() string { return w.dir }

// Write atomically replaces a redacted artifact.
func (w *Writer) Write(name string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	path, err := w.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	if err := os.WriteFile(path, redactSecrets(data, w.secrets), 0o600); err != nil {
		return fmt.Errorf("write artifact %q: %w", name, err)
	}
	return os.Chmod(path, 0o600)
}

// WriteJSON writes an indented JSON artifact through the redaction pass.
func (w *Writer) WriteJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact %q: %w", name, err)
	}
	data = append(data, '\n')
	return w.Write(name, data)
}

// AppendJSONL appends and syncs one redacted record so a harness crash leaves
// all previously completed evidence on disk.
func (w *Writer) AppendJSONL(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode JSONL artifact %q: %w", name, err)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	path, err := w.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open JSONL artifact %q: %w", name, err)
	}
	defer file.Close()
	if _, err := file.Write(redactSecrets(data, w.secrets)); err != nil {
		return fmt.Errorf("append JSONL artifact %q: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync JSONL artifact %q: %w", name, err)
	}
	return file.Chmod(0o600)
}

func (w *Writer) path(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("artifact path %q is invalid", name)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes bundle", name)
	}
	return filepath.Join(w.dir, clean), nil
}
