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
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

// canonicalFunc renders a function or method as
// <package path>.Func, <package path>.(Type).Method or <package path>.(*Type).Method.
//
// The form is chosen so that a symbol read from the classification file is
// unambiguous: the receiver's package is part of the name, which is what makes
// (*postgres.Instance).Shutdown distinguishable from (*http.Server).Shutdown.
func canonicalFunc(fn *types.Func) string {
	if fn == nil || fn.Pkg() == nil {
		return ""
	}

	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return fn.Pkg().Path() + "." + fn.Name()
	}

	recv := sig.Recv().Type()
	pointer := false
	if p, ok := recv.(*types.Pointer); ok {
		pointer = true
		recv = p.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return fn.Pkg().Path() + ".(?)." + fn.Name()
	}
	obj := named.Origin().Obj()

	star := ""
	if pointer {
		star = "*"
	}
	return obj.Pkg().Path() + ".(" + star + obj.Name() + ")." + fn.Name()
}

// canonicalField renders a struct field selection as
// <package path>.<Type>.<Field>, or "" when the selection is not a field of a
// named struct type.
func canonicalField(info *types.Info, sel *ast.SelectorExpr) string {
	selection := info.Selections[sel]
	if selection == nil || selection.Kind() != types.FieldVal {
		return ""
	}
	field, ok := selection.Obj().(*types.Var)
	if !ok {
		return ""
	}

	recv := selection.Recv()
	for {
		p, ok := recv.(*types.Pointer)
		if !ok {
			break
		}
		recv = p.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	obj := named.Origin().Obj()

	return obj.Pkg().Path() + "." + obj.Name() + "." + field.Name()
}

// enclosingIndex answers "which declaration is this position inside".
type enclosingIndex struct {
	pkgPath string
	spans   []span
	symbols []string
}

type span struct {
	start, end token.Pos
	symbol     string
}

func newEnclosingIndex(pkg *packages.Package, file *ast.File) *enclosingIndex {
	idx := &enclosingIndex{pkgPath: pkg.PkgPath}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		obj, _ := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
		symbol := canonicalFunc(obj)
		if symbol == "" {
			symbol = pkg.PkgPath + "." + fn.Name.Name
		}
		idx.spans = append(idx.spans, span{start: fn.Pos(), end: fn.End(), symbol: symbol})
		idx.symbols = append(idx.symbols, symbol)
	}
	sort.Slice(idx.spans, func(i, j int) bool { return idx.spans[i].start < idx.spans[j].start })

	return idx
}

// at returns the symbol enclosing pos. Package-level declarations, such as a var
// initialised by a call, are attributed to the package initialiser, because that
// is where the code actually runs.
func (i *enclosingIndex) at(pos token.Pos) string {
	for _, s := range i.spans {
		if pos >= s.start && pos < s.end {
			return s.symbol
		}
	}
	return i.pkgPath + ".init"
}
