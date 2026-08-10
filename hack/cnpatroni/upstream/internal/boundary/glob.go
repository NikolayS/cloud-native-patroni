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

package boundary

import (
	"fmt"
	"strings"
)

// Glob is one compiled boundary-manifest path pattern.
//
// The supported syntax is deliberately small, so that a maintainer reading the
// manifest can predict the classification of a path without consulting a
// matching library:
//
//   - patterns are `/`-separated and relative to the repository root; a leading
//     `/` or `./` is an error;
//   - `**` matches zero or more whole path segments and is legal only as a
//     complete segment;
//   - `*` matches zero or more characters inside one segment and never matches
//     `/`;
//   - `?` matches exactly one character other than `/`;
//   - a trailing `/` is shorthand for that pattern plus `**`.
//
// There are no character classes, no brace expansion and no negation. Negation
// is expressed by declaring a more specific rule.
type Glob struct {
	pattern  string
	segments []string
}

// CompileGlob validates a pattern and prepares it for matching.
func CompileGlob(pattern string) (*Glob, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	if strings.HasPrefix(pattern, "/") {
		return nil, fmt.Errorf("pattern %q must not start with %q", pattern, "/")
	}
	if strings.HasPrefix(pattern, "./") {
		return nil, fmt.Errorf("pattern %q must not start with %q", pattern, "./")
	}
	if strings.Contains(pattern, `\`) {
		return nil, fmt.Errorf("pattern %q must use %q as the separator", pattern, "/")
	}
	for _, bad := range []string{"[", "]", "{", "}", "!"} {
		if strings.Contains(pattern, bad) {
			return nil, fmt.Errorf("pattern %q contains unsupported metacharacter %q", pattern, bad)
		}
	}

	expanded := pattern
	if strings.HasSuffix(expanded, "/") {
		expanded += "**"
	}

	segments := strings.Split(expanded, "/")
	for _, segment := range segments {
		if segment == "" {
			return nil, fmt.Errorf("pattern %q contains an empty path segment", pattern)
		}
		if strings.Contains(segment, "**") && segment != "**" {
			return nil, fmt.Errorf("pattern %q: %q is only legal as a complete segment", pattern, "**")
		}
		if strings.Contains(segment, "***") {
			return nil, fmt.Errorf("pattern %q contains %q", pattern, "***")
		}
	}

	return &Glob{pattern: pattern, segments: segments}, nil
}

// Pattern returns the pattern as it was written in the manifest.
func (g *Glob) Pattern() string { return g.pattern }

// IsLiteral reports whether the pattern contains no metacharacters, in which
// case it names exactly one path.
func (g *Glob) IsLiteral() bool {
	return !strings.ContainsAny(g.pattern, "*?") && !strings.HasSuffix(g.pattern, "/")
}

// LiteralPrefix returns the leading part of the pattern up to the first
// metacharacter. It is the primary specificity key used to order rules.
func (g *Glob) LiteralPrefix() string {
	if i := strings.IndexAny(g.pattern, "*?"); i >= 0 {
		return g.pattern[:i]
	}

	return g.pattern
}

// Match reports whether a repository-relative path is covered by the pattern.
func (g *Glob) Match(path string) bool {
	return matchSegments(g.segments, strings.Split(path, "/"))
}

func matchSegments(patterns, parts []string) bool {
	switch {
	case len(patterns) == 0:
		return len(parts) == 0
	case patterns[0] == "**":
		// `**` consumes zero or more whole segments.
		for i := 0; i <= len(parts); i++ {
			if matchSegments(patterns[1:], parts[i:]) {
				return true
			}
		}

		return false
	case len(parts) == 0:
		return false
	case !matchSegment(patterns[0], parts[0]):
		return false
	default:
		return matchSegments(patterns[1:], parts[1:])
	}
}

// matchSegment matches one segment, where `*` spans any run of characters and
// `?` spans exactly one.
func matchSegment(pattern, part string) bool {
	if pattern == "*" {
		return true
	}

	var (
		p, s          int
		starP, starS  = -1, 0
		patternRunes  = []rune(pattern)
		segmentRunes  = []rune(part)
		patternLength = len(patternRunes)
		segmentLength = len(segmentRunes)
	)

	for s < segmentLength {
		switch {
		case p < patternLength && patternRunes[p] == '?':
			p++
			s++
		case p < patternLength && patternRunes[p] == '*':
			starP, starS = p, s
			p++
		case p < patternLength && patternRunes[p] == segmentRunes[s]:
			p++
			s++
		case starP >= 0:
			starS++
			p, s = starP+1, starS
		default:
			return false
		}
	}

	for p < patternLength && patternRunes[p] == '*' {
		p++
	}

	return p == patternLength
}
