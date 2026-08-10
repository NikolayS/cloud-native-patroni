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

// Package replay loads recorded evidence and calls the production detector.
package replay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/verdict"
)

type fixtureManifest struct {
	ExpectedNodes []string `json:"expected_nodes"`
}

// LoadAndDetect reads a fixture directory and invokes verdict.Detect.
func LoadAndDetect(dir string) (verdict.RunVerdict, error) {
	harnessFailure := verdict.RunVerdict{Status: verdict.HarnessError, ExitCode: 3}
	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return harnessFailure, fmt.Errorf("read fixture manifest: %w", err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return harnessFailure, fmt.Errorf("decode fixture manifest: %w", err)
	}
	file, err := os.Open(filepath.Join(dir, "rounds.jsonl"))
	if err != nil {
		return harnessFailure, fmt.Errorf("open fixture rounds: %w", err)
	}
	defer file.Close()

	var attempts []model.Attempt
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		var attempt model.Attempt
		if err := json.Unmarshal(scanner.Bytes(), &attempt); err != nil {
			return harnessFailure, fmt.Errorf("decode fixture round line %d: %w", line, err)
		}
		if !attempt.Outcome.Valid() || !attempt.Resolution.Valid() {
			return harnessFailure, fmt.Errorf("fixture round line %d has invalid outcome or resolution", line)
		}
		attempts = append(attempts, attempt)
	}
	if err := scanner.Err(); err != nil {
		return harnessFailure, fmt.Errorf("read fixture rounds: %w", err)
	}
	return verdict.Detect(verdict.Input{ExpectedNodes: manifest.ExpectedNodes, Attempts: attempts})
}
