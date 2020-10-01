// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package godoc

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"

	"golang.org/x/pkgsite/internal/derrors"
)

// encodingType identifies the encoding being used, in case
// we ever use a different one and need to distinguish them
// when reading from the DB.
// It should be a four-byte string.
const encodingType = "AST1"

// Register ast types for gob, so it can decode concrete types that are stored
// in interface variables.
func init() {
	for _, n := range []interface{}{
		&ast.ArrayType{},
		&ast.AssignStmt{},
		&ast.BasicLit{},
		&ast.BinaryExpr{},
		&ast.BlockStmt{},
		&ast.BranchStmt{},
		&ast.CallExpr{},
		&ast.CaseClause{},
		&ast.CompositeLit{},
		&ast.DeclStmt{},
		&ast.DeferStmt{},
		&ast.Ellipsis{},
		&ast.ExprStmt{},
		&ast.ForStmt{},
		&ast.FuncDecl{},
		&ast.FuncLit{},
		&ast.FuncType{},
		&ast.GenDecl{},
		&ast.GoStmt{},
		&ast.KeyValueExpr{},
		&ast.IfStmt{},
		&ast.ImportSpec{},
		&ast.IncDecStmt{},
		&ast.IndexExpr{},
		&ast.InterfaceType{},
		&ast.MapType{},
		&ast.ParenExpr{},
		&ast.RangeStmt{},
		&ast.ReturnStmt{},
		&ast.Scope{},
		&ast.SelectorExpr{},
		&ast.SliceExpr{},
		&ast.StarExpr{},
		&ast.StructType{},
		&ast.SwitchStmt{},
		&ast.TypeAssertExpr{},
		&ast.TypeSpec{},
		&ast.TypeSwitchStmt{},
		&ast.UnaryExpr{},
		&ast.ValueSpec{},
		&ast.Ident{},
	} {
		gob.Register(n)
	}
}

// Encode encodes a Package into a byte slice.
// During its operation, Encode modifies the AST,
// but it restores it to a state suitable for
// rendering before it returns.
func (p *Package) Encode() (_ []byte, err error) {
	defer derrors.Wrap(&err, "godoc.Package.Encode()")

	if p.renderCalled {
		return nil, errors.New("can't Encode after Render")
	}

	for _, f := range p.Files {
		removeCycles(f.AST)
	}

	var buf bytes.Buffer
	io.WriteString(&buf, encodingType)
	enc := gob.NewEncoder(&buf)
	// Encode the fset using the Write method it provides.
	if err := p.Fset.Write(enc.Encode); err != nil {
		return nil, err
	}
	if err := enc.Encode(p.gobPackage); err != nil {
		return nil, err
	}
	for _, f := range p.Files {
		fixupObjects(f.AST)
	}
	return buf.Bytes(), nil
}

// DecodPackage decodes a byte slice encoded with Package.Encode into a Package.
func DecodePackage(data []byte) (_ *Package, err error) {
	defer derrors.Wrap(&err, "DecodePackage()")

	le := len(encodingType)
	if len(data) < le || string(data[:le]) != encodingType {
		return nil, fmt.Errorf("want initial bytes to be %q but they aren't", encodingType)
	}
	dec := gob.NewDecoder(bytes.NewReader(data[le:]))
	p := &Package{Fset: token.NewFileSet()}
	if err := p.Fset.Read(dec.Decode); err != nil {
		return nil, err
	}
	if err := dec.Decode(&p.gobPackage); err != nil {
		return nil, err
	}
	for _, f := range p.Files {
		fixupObjects(f.AST)
	}
	return p, nil
}

// removeCycles removes cycles from f. There are two sources of cycles
// in an ast.File: Scopes and Objects.
//
// removeCycles removes all Scopes, since doc generation doesn't use them. Doc
// generation does use Objects, and it needs object identity to be preserved
// (see internal/doc/example.go). It also needs the Object.Decl field, to create
// anchor links (see dochtml/internal/render/idents.go). The Object.Decl field
// is responsible for cycles. Doc generation It doesn't need the Data or Type
// fields of Object.
//
// We need to break the cycles, and preserve Object identity when decoding. For
// an example of the latter, if ast.Idents A and B both pointed to the same
// Object, gob would write them as two separate objects, and decoding would
// preserve that. (See TestObjectIdentity for a small example of this sort of
// sharing.)
//
// We solve both problems by assigning numbers to Decls and Objects. We first
// walk through the AST to assign the numbers, then walk it again to put the
// numbers into Ident.Objs. We take advantage of the fact that the Data and Decl
// fields are of type interface{}, storing the object number into Data and the
// Decl number into Decl.
func removeCycles(f *ast.File) {
	f.Scope.Objects = nil // doc doesn't use scopes

	// First pass: assign every Decl and Spec a number.
	// Since these aren't shared and Inspect is deterministic,
	// this walk will produce the same sequence of Decls after encoding/decoding.
	// Also assign a unique number to each Object we find in an Ident.
	// Objects may be shared; traversing the decoded AST would not
	// produce the same sequence. So we store their numbers separately.
	declNums := map[interface{}]int{}
	objNums := map[*ast.Object]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		if isRelevantDecl(n) {
			if _, ok := declNums[n]; ok {
				panic(fmt.Sprintf("duplicate decl %+v", n))
			}
			declNums[n] = len(declNums)
		} else if id, ok := n.(*ast.Ident); ok && id.Obj != nil {
			if _, ok := objNums[id.Obj]; !ok {
				objNums[id.Obj] = len(objNums)
			}
		}
		return true
	})

	// Second pass: put the numbers into Ident.Objs.
	// The Decl field gets a number from the declNums map, or nil
	// if it's not a relevant Decl.
	// The Data field gets a number from the objNums map. (This destroys
	// whatever might be in the Data field, but doc generation doesn't care.)
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj == nil {
			return true
		}
		if _, ok := id.Obj.Decl.(int); ok { // seen this object already
			return true
		}
		id.Obj.Type = nil // Not needed for doc gen.
		id.Obj.Data, ok = objNums[id.Obj]
		if !ok {
			panic(fmt.Sprintf("no number for Object %v", id.Obj))
		}
		d, ok := declNums[id.Obj.Decl]
		if !ok && isRelevantDecl(id.Obj.Decl) {
			panic(fmt.Sprintf("no number for Decl %v", id.Obj.Decl))
		}
		id.Obj.Decl = d
		return true
	})
}

// fixupObjects re-establishes the original Object and Decl relationships of the
// ast.File f.
//
// f is the result of EncodeASTFiles, which uses removeCycles (see above) to
// modify ast.Objects so that they are uniquely identified by their Data field,
// and refer to their Decl via a number in the Decl field. fixupObjects uses
// those values to reconstruct the same set of relationships.
func fixupObjects(f *ast.File) {
	// First pass: reconstruct the numbers of every Decl.
	var decls []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if isRelevantDecl(n) {
			decls = append(decls, n)
		}
		return true
	})

	// Second pass: replace the numbers in Ident.Objs with the right Nodes.
	var objs []*ast.Object
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Obj == nil {
			return true
		}
		obj := id.Obj
		if obj.Data == nil {
			// We've seen this object already.
			// Possible if fixing up without serializing/deserializing, because
			// Objects are still shared in that case.
			// Do nothing.
			return true
		}
		num := obj.Data.(int)
		switch {
		case num < len(objs):
			// We've seen this Object before.
			id.Obj = objs[num]
		case num == len(objs):
			// A new object; fix it up and remember it.
			obj.Data = nil
			if obj.Decl != nil {
				obj.Decl = decls[obj.Decl.(int)]
			}
			objs = append(objs, obj)
		case num > len(objs):
			panic("n > len(objs); shouldn't happen")
		}
		return true
	})
}

// isRelevantDecl reports whether n is a Node for a declaration relevant to
// documentation.
func isRelevantDecl(n interface{}) bool {
	switch n.(type) {
	case *ast.FuncDecl, *ast.GenDecl, *ast.ValueSpec, *ast.TypeSpec, *ast.ImportSpec:
		return true
	default:
		return false
	}
}