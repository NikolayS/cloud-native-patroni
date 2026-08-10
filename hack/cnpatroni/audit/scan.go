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

package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Finding is one authority hit.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Spec     string `json:"spec,omitempty"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Package  string `json:"package"`
	Symbol   string `json:"symbol"`
	Detail   string `json:"detail"`
}

// Text renders the finding in the GNU error format, which editors, vim -q and
// the GitHub log viewer all understand.
func (f Finding) Text() string {
	return fmt.Sprintf("%s:%d:%d: [%s] %s in %s", f.Path, f.Line, f.Col, f.Rule, f.Detail, f.Symbol)
}

// GitHub renders the finding as a workflow command, so that it appears as an
// annotation on the pull request diff.
func (f Finding) GitHub() string {
	title := f.Rule
	if f.Spec != "" {
		title += " (specification section " + f.Spec + ")"
	}
	return fmt.Sprintf("::error file=%s,line=%d,col=%d,title=%s::%s in %s",
		f.Path, f.Line, f.Col, title, f.Detail, f.Symbol)
}

// ScanResult is everything one pass over the tree observed.
type ScanResult struct {
	Findings []Finding `json:"findings"`
	// Symbols is every function symbol declared in scope. Check uses it to tell
	// "this classification is stale" from "this classification has no hit".
	Symbols map[string]bool `json:"-"`
	// GuardOps maps an enclosing symbol to the op literals it passes to the
	// runtime guard, so that the guard wiring can be verified statically.
	GuardOps map[string][]string `json:"-"`
	// Packages is the number of packages loaded.
	Packages int `json:"-"`
	// Files is the number of Go files inspected after exclusions.
	Files int `json:"-"`
}

var generatedLine = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// ScanRepo scans every root in the rule set and returns repository-relative
// findings.
func ScanRepo(repoRoot string, rs *RuleSet) (*ScanResult, error) {
	merged := &ScanResult{Symbols: map[string]bool{}, GuardOps: map[string][]string{}}
	repoAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repository root %s: %w", repoRoot, err)
	}
	repoAbs = filepath.Clean(repoAbs)

	for _, root := range rs.Scope.Roots {
		rootAbs, err := filepath.Abs(filepath.Join(repoAbs, filepath.FromSlash(root)))
		if err != nil {
			return nil, fmt.Errorf("resolving scan root %q: %w", root, err)
		}
		if rootAbs != repoAbs && !strings.HasPrefix(rootAbs, repoAbs+string(filepath.Separator)) {
			return nil, fmt.Errorf("scan root %q leaves the repository: %s is outside %s", root, rootAbs, repoAbs)
		}

		prefix := path.Clean(filepath.ToSlash(root))
		res, err := Scan(rootAbs, rs)
		if err != nil {
			return nil, err
		}
		for _, f := range res.Findings {
			if prefix != "." && prefix != "" {
				f.Path = prefix + "/" + f.Path
			}
			merged.Findings = append(merged.Findings, f)
		}
		for s := range res.Symbols {
			merged.Symbols[s] = true
		}
		for s, ops := range res.GuardOps {
			merged.GuardOps[s] = append(merged.GuardOps[s], ops...)
		}
		merged.Packages += res.Packages
		merged.Files += res.Files
	}
	sortFindings(merged.Findings)

	return merged, nil
}

// Scan type-checks the Go module rooted at dir and returns every hit, with paths
// relative to dir.
func Scan(dir string, rs *RuleSet) (*ScanResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps,
		Dir:   absDir,
		Fset:  fset,
		Tests: rs.Scope.IncludeTests,
		// Parsing without comments is what makes comment immunity structural
		// rather than a heuristic: a comment is simply absent from the tree the
		// engines walk.
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
			return parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
		},
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading packages in %s: %w", dir, err)
	}
	if err := firstLoadError(pkgs); err != nil {
		return nil, err
	}

	sc := &scanner{
		rules:  rs,
		fset:   fset,
		dir:    absDir,
		result: &ScanResult{Symbols: map[string]bool{}, GuardOps: map[string][]string{}},
	}
	for _, pkg := range pkgs {
		sc.scanPackage(pkg)
	}
	sortFindings(sc.result.Findings)

	return sc.result, nil
}

func firstLoadError(pkgs []*packages.Package) error {
	var problems []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			problems = append(problems, fmt.Sprintf("%s: %s", p.PkgPath, e.Error()))
		}
	})
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("the tree does not type-check, so the audit cannot run: %s",
		strings.Join(problems[:min(len(problems), 5)], "; "))
}

type scanner struct {
	rules  *RuleSet
	fset   *token.FileSet
	dir    string
	result *ScanResult
}

func (s *scanner) scanPackage(pkg *packages.Package) {
	if pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
		return
	}
	s.result.Packages++

	for _, file := range pkg.Syntax {
		abs := s.fset.Position(file.Pos()).Filename
		rel, err := filepath.Rel(s.dir, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if s.skipFile(rel) {
			continue
		}
		s.result.Files++
		s.scanFile(pkg, file, rel)
	}
}

func (s *scanner) skipFile(rel string) bool {
	if strings.HasPrefix(rel, "../") {
		return true
	}
	if !s.rules.Scope.IncludeTests && strings.HasSuffix(rel, "_test.go") {
		return true
	}
	for _, pattern := range s.rules.Scope.ExcludePaths {
		if matchPath(pattern, rel) {
			return true
		}
	}
	return s.rules.Scope.ExcludeGenerated && isGenerated(filepath.Join(s.dir, filepath.FromSlash(rel)))
}

// matchPath supports the two forms the rule set needs: a "dir/**" prefix and a
// plain filepath.Match pattern.
func matchPath(pattern, rel string) bool {
	if strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(rel, strings.TrimSuffix(pattern, "**"))
	}
	ok, err := path.Match(pattern, rel)
	return err == nil && ok
}

// isGenerated looks for the standard generated-code marker anywhere before the
// package clause, rather than in the first few lines. In this repository the
// marker sits below an eighteen-line licence header, so a fixed window of the
// first lines would classify every generated file as hand-written and flood the
// audit with hits that no human can act on.
func isGenerated(abs string) bool {
	f, err := os.Open(abs) //nolint:gosec // the path comes from the loaded package set
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimRight(scan.Text(), "\r")
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if generatedLine.MatchString(line) {
			return true
		}
	}
	return false
}

func (s *scanner) scanFile(pkg *packages.Package, file *ast.File, rel string) {
	info := pkg.TypesInfo
	enclosing := newEnclosingIndex(pkg, file)
	for _, sym := range enclosing.symbols {
		s.result.Symbols[sym] = true
	}
	// The two pseudo-symbols a finding can be attributed to when it is not inside
	// a function: package-level initialisation, and the import list.
	s.result.Symbols[pkg.PkgPath+".init"] = true
	s.result.Symbols[pkg.PkgPath+".imports"] = true

	writes := lhsSelectors(file)

	s.scanImports(pkg, file, rel)

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			s.scanCall(pkg, node, rel, enclosing)
		case *ast.BasicLit:
			s.scanLiteral(node, rel, enclosing, pkg)
		case *ast.SelectorExpr:
			s.scanSelector(info, node, rel, enclosing, pkg, writes[node])
		}
		return true
	})
}

func (s *scanner) scanImports(pkg *packages.Package, file *ast.File, rel string) {
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		for _, rule := range s.rules.Rules {
			if slices.Contains(rule.Imports, value) {
				s.add(rule, rel, spec.Pos(), pkg.PkgPath+".imports", pkg.PkgPath,
					fmt.Sprintf("import of %s", value))
			}
		}
	}
}

func (s *scanner) scanCall(pkg *packages.Package, call *ast.CallExpr, rel string, enc *enclosingIndex) {
	fn := calleeFunc(pkg.TypesInfo, call)
	if fn == nil {
		return
	}
	symbol := canonicalFunc(fn)
	if symbol == "" {
		return
	}
	site := enc.at(call.Pos())

	if s.rules.GuardSymbol != "" && symbol == s.rules.GuardSymbol {
		s.result.GuardOps[site] = append(s.result.GuardOps[site], constantArgs(pkg.TypesInfo, call)...)
	}

	for _, rule := range s.rules.Rules {
		if !slices.Contains(rule.Calls, symbol) {
			continue
		}
		if len(rule.CallArgContains) > 0 && !argContains(pkg.TypesInfo, call, rule.CallArgContains) {
			continue
		}
		s.add(rule, rel, call.Pos(), site, pkg.PkgPath, "call to "+symbol)
	}
}

func (s *scanner) scanLiteral(lit *ast.BasicLit, rel string, enc *enclosingIndex, pkg *packages.Package) {
	if lit.Kind != token.STRING {
		return
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	for _, rule := range s.rules.Rules {
		for _, want := range rule.Literals {
			if strings.Contains(value, want) {
				s.add(rule, rel, lit.Pos(), enc.at(lit.Pos()), pkg.PkgPath,
					fmt.Sprintf("string literal containing %q", want))
				break
			}
		}
	}
}

func (s *scanner) scanSelector(
	info *types.Info, sel *ast.SelectorExpr, rel string, enc *enclosingIndex,
	pkg *packages.Package, isWrite bool,
) {
	field := canonicalField(info, sel)
	if field == "" {
		return
	}
	for _, rule := range s.rules.Rules {
		switch {
		case isWrite && slices.Contains(rule.FieldWrites, field):
			s.add(rule, rel, sel.Pos(), enc.at(sel.Pos()), pkg.PkgPath, "assignment to "+field)
		case !isWrite && slices.Contains(rule.FieldReads, field):
			s.add(rule, rel, sel.Pos(), enc.at(sel.Pos()), pkg.PkgPath, "read of "+field)
		}
	}
}

func (s *scanner) add(rule Rule, rel string, pos token.Pos, symbol, pkgPath, detail string) {
	p := s.fset.Position(pos)
	s.result.Findings = append(s.result.Findings, Finding{
		Rule:     rule.ID,
		Severity: rule.Severity,
		Spec:     rule.Spec,
		Path:     rel,
		Line:     p.Line,
		Col:      p.Column,
		Package:  pkgPath,
		Symbol:   symbol,
		Detail:   detail,
	})
}

func sortFindings(findings []Finding) {
	slices.SortStableFunc(findings, func(a, b Finding) int {
		return compareAll(
			strings.Compare(a.Path, b.Path),
			a.Line-b.Line,
			a.Col-b.Col,
			strings.Compare(a.Rule, b.Rule),
			strings.Compare(a.Symbol, b.Symbol),
		)
	})
}

func compareAll(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// lhsSelectors collects the selector expressions that are assigned to, so that a
// read and a write of the same field can be told apart.
func lhsSelectors(file *ast.File) map[*ast.SelectorExpr]bool {
	out := map[*ast.SelectorExpr]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr); ok {
					out[sel] = true
				}
			}
		case *ast.IncDecStmt:
			if sel, ok := ast.Unparen(node.X).(*ast.SelectorExpr); ok {
				out[sel] = true
			}
		}
		return true
	})
	return out
}

func calleeFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	expr := ast.Unparen(call.Fun)
	for {
		switch e := expr.(type) {
		case *ast.IndexExpr:
			expr = ast.Unparen(e.X)
			continue
		case *ast.IndexListExpr:
			expr = ast.Unparen(e.X)
			continue
		case *ast.Ident:
			fn, _ := info.Uses[e].(*types.Func)
			return fn
		case *ast.SelectorExpr:
			fn, _ := info.Uses[e.Sel].(*types.Func)
			return fn
		default:
			return nil
		}
	}
}

func constantArgs(info *types.Info, call *ast.CallExpr) []string {
	var out []string
	for _, arg := range call.Args {
		if tv, ok := info.Types[arg]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			if unquoted, err := strconv.Unquote(tv.Value.String()); err == nil {
				out = append(out, unquoted)
			}
		}
	}
	return out
}

func argContains(info *types.Info, call *ast.CallExpr, wanted []string) bool {
	for _, arg := range constantArgs(info, call) {
		for _, want := range wanted {
			if strings.Contains(arg, want) {
				return true
			}
		}
	}
	return false
}
