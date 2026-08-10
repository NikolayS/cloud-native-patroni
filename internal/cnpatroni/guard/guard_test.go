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

package guard

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// armedAtInit records the value of panicOnForbidden before any test mutates it,
// so that case "armed by default under go test" observes the real default.
var armedAtInit = panicOnForbidden

func TestNewForbiddenMessage(t *testing.T) {
	const sym = "github.com/cloudnative-pg/cloudnative-pg/pkg/management/postgres.(*Instance).PromoteAndWait"

	cases := []struct {
		name     string
		op       string
		contains []string
	}{
		{
			name:     "names the operation",
			op:       sym,
			contains: []string{sym},
		},
		{
			name:     "names the project",
			op:       sym,
			contains: []string{"CloudNativePatroni"},
		},
		{
			name:     "points at the classification data and the specification",
			op:       sym,
			contains: []string{"authority-classification.yaml", "7.4"},
		},
		{
			name:     "an empty operation is not silent",
			op:       "",
			contains: []string{"empty op"},
		},
		{
			name:     "a one-character operation is reported verbatim",
			op:       "x",
			contains: []string{"x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := newForbidden(tc.op)
			if err == nil {
				t.Fatal("newForbidden returned nil")
			}
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("error does not wrap ErrForbidden: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestErrForbiddenIsADistinctSentinel(t *testing.T) {
	if errors.Is(errors.New("unrelated"), ErrForbidden) {
		t.Error("an unrelated error matched ErrForbidden")
	}
}

func TestForbiddenSurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("reconciling instance: %w", newForbidden("pkg.(*T).M"))
	if !errors.Is(wrapped, ErrForbidden) {
		t.Errorf("errors.Is failed after %%w wrapping: %v", wrapped)
	}
}

func TestForbiddenPanicsWhenArmed(t *testing.T) {
	t.Cleanup(withPanicOnForbidden(t, true))

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Forbidden did not panic while armed")
		}
		err, ok := recovered.(error)
		if !ok {
			t.Fatalf("recovered value is %T, want error", recovered)
		}
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("recovered error does not wrap ErrForbidden: %v", err)
		}
	}()

	_ = Forbidden("pkg.(*T).M")
}

func TestForbiddenReturnsWhenNotArmed(t *testing.T) {
	t.Cleanup(withPanicOnForbidden(t, false))

	err := Forbidden("pkg.(*T).M")
	if err == nil {
		t.Fatal("Forbidden returned nil while disarmed")
	}
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("error does not wrap ErrForbidden: %v", err)
	}
}

func TestForbiddenIsArmedByDefaultUnderGoTest(t *testing.T) {
	if !armedAtInit {
		t.Error("panicOnForbidden is false under go test; a reached guard would be swallowed")
	}
}

func TestNewForbiddenIsSafeForConcurrentUse(t *testing.T) {
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			if err := newForbidden(fmt.Sprintf("pkg.(*T).M%d", i)); err == nil {
				t.Error("newForbidden returned nil")
			}
		}(i)
	}
	wg.Wait()
}

// withPanicOnForbidden sets panicOnForbidden and returns a function restoring it.
func withPanicOnForbidden(t *testing.T, armed bool) func() {
	t.Helper()

	previous := panicOnForbidden
	panicOnForbidden = armed
	return func() { panicOnForbidden = previous }
}
