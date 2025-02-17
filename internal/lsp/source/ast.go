package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/ast/astutil"
)

type action int

const (
	actionUnknown action = iota // None of the below
	actionExpr                  // FuncDecl, true Expr or Ident(types.{Const,Var})
	actionType                  // type Expr or Ident(types.TypeName).
	actionStmt                  // Stmt or Ident(types.Label)
	actionPackage               // Ident(types.Package) or ImportSpec
)

// findInterestingNode classifies the syntax node denoted by path as one of:
//    - an expression, part of an expression or a reference to a constant
//      or variable;
//    - a type, part of a type, or a reference to a named type;
//    - a statement, part of a statement, or a label referring to a statement;
//    - part of a package declaration or import spec.
//    - none of the above.
// and returns the most "interesting" associated node, which may be
// the same node, an ancestor or a descendent.
//
// Adapted from golang.org/x/tools/cmd/guru (Copyright (c) 2013 The Go Authors). All rights
// reserved. See NOTICE for full license.
func findInterestingNode(pkg Package, path []ast.Node) ([]ast.Node, action) {
	// TODO(adonovan): integrate with go/types/stdlib_test.go and
	// apply this to every AST node we can find to make sure it
	// doesn't crash.

	// TODO(adonovan): audit for ParenExpr safety, esp. since we
	// traverse up and down.

	// TODO(adonovan): if the users selects the "." in
	// "fmt.Fprintf()", they'll get an ambiguous selection error;
	// we won't even reach here.  Can we do better?

	// TODO(adonovan): describing a field within 'type T struct {...}'
	// describes the (anonymous) struct type and concludes "no methods".
	// We should ascend to the enclosing type decl, if any.

	for len(path) > 0 {
		switch n := path[0].(type) {
		case *ast.GenDecl:
			if len(n.Specs) == 1 {
				// Descend to sole {Import,Type,Value}Spec child.
				path = append([]ast.Node{n.Specs[0]}, path...)
				continue
			}
			return path, actionUnknown // uninteresting

		case *ast.FuncDecl:
			// Descend to function name.
			path = append([]ast.Node{n.Name}, path...)
			continue

		case *ast.ImportSpec:
			return path, actionPackage

		case *ast.ValueSpec:
			if len(n.Names) == 1 {
				// Descend to sole Ident child.
				path = append([]ast.Node{n.Names[0]}, path...)
				continue
			}
			return path, actionUnknown // uninteresting

		case *ast.TypeSpec:
			// Descend to type name.
			path = append([]ast.Node{n.Name}, path...)
			continue

		case ast.Stmt:
			return path, actionStmt

		case *ast.ArrayType,
			*ast.StructType,
			*ast.FuncType,
			*ast.InterfaceType,
			*ast.MapType,
			*ast.ChanType:
			return path, actionType

		case *ast.Comment, *ast.CommentGroup, *ast.File, *ast.KeyValueExpr, *ast.CommClause:
			return path, actionUnknown // uninteresting

		case *ast.Ellipsis:
			// Continue to enclosing node.
			// e.g. [...]T in ArrayType
			//      f(x...) in CallExpr
			//      f(x...T) in FuncType

		case *ast.Field:
			// TODO(adonovan): this needs more thought,
			// since fields can be so many things.
			if len(n.Names) == 1 {
				// Descend to sole Ident child.
				path = append([]ast.Node{n.Names[0]}, path...)
				continue
			}
			// Zero names (e.g. anon field in struct)
			// or multiple field or param names:
			// continue to enclosing field list.

		case *ast.FieldList:
			// Continue to enclosing node:
			// {Struct,Func,Interface}Type or FuncDecl.

		case *ast.BasicLit:
			if _, ok := path[1].(*ast.ImportSpec); ok {
				return path[1:], actionPackage
			}
			return path, actionExpr

		case *ast.SelectorExpr:
			// TODO(adonovan): use Selections info directly.
			if pkg.GetTypesInfo().Uses[n.Sel] == nil {
				// TODO(adonovan): is this reachable?
				return path, actionUnknown
			}
			// Descend to .Sel child.
			path = append([]ast.Node{n.Sel}, path...)
			continue

		case *ast.Ident:
			switch pkg.GetTypesInfo().ObjectOf(n).(type) {
			case *types.PkgName:
				return path, actionPackage

			case *types.Const:
				return path, actionExpr

			case *types.Label:
				return path, actionStmt

			case *types.TypeName:
				return path, actionType

			case *types.Var:
				// For x in 'struct {x T}', return struct type, for now.
				if _, ok := path[1].(*ast.Field); ok {
					_ = path[2].(*ast.FieldList) // assertion
					if _, ok := path[3].(*ast.StructType); ok {
						return path[3:], actionType
					}
				}
				return path, actionExpr

			case *types.Func:
				return path, actionExpr

			case *types.Builtin:
				// For reference to built-in function, return enclosing call.
				path = path[1:] // ascend to enclosing function call
				continue

			case *types.Nil:
				return path, actionExpr
			}

			// No object.
			switch path[1].(type) {
			case *ast.SelectorExpr:
				// Return enclosing selector expression.
				return path[1:], actionExpr

			case *ast.Field:
				// TODO(adonovan): test this.
				// e.g. all f in:
				//  struct { f, g int }
				//  interface { f() }
				//  func (f T) method(f, g int) (f, g bool)
				//
				// switch path[3].(type) {
				// case *ast.FuncDecl:
				// case *ast.StructType:
				// case *ast.InterfaceType:
				// }
				//
				// return path[1:], actionExpr
				//
				// Unclear what to do with these.
				// Struct.Fields             -- field
				// Interface.Methods         -- field
				// FuncType.{Params.Results} -- actionExpr
				// FuncDecl.Recv             -- actionExpr

			case *ast.File:
				// 'package foo'
				return path, actionPackage

			case *ast.ImportSpec:
				return path[1:], actionPackage

			default:
				// e.g. blank identifier
				// or y in "switch y := x.(type)"
				// or code in a _test.go file that's not part of the package.
				return path, actionUnknown
			}

		case *ast.StarExpr:
			if pkg.GetTypesInfo().Types[n].IsType() {
				return path, actionType
			}
			return path, actionExpr

		case ast.Expr:
			// All Expr but {BasicLit,Ident,StarExpr} are
			// "true" expressions that evaluate to a value.
			return path, actionExpr
		}

		// Ascend to parent.
		path = path[1:]
	}

	return nil, actionUnknown // unreachable
}

func isAlias(obj *types.TypeName) bool {
	return obj.IsAlias()
}

type InvalidNodeError struct {
	Node ast.Node
	msg  string
}

func (e *InvalidNodeError) Error() string {
	return e.msg
}

func newInvalidNodeError(fset *token.FileSet, node ast.Node) *InvalidNodeError {
	lineCol := func(p token.Pos) string {
		pp := fset.Position(p)
		return fmt.Sprintf("%d:%d", pp.Line, pp.Column)
	}
	return &InvalidNodeError{
		Node: node,
		msg: fmt.Sprintf("invalid node: %s (%s-%s)",
			reflect.TypeOf(node).Elem(), lineCol(node.Pos()), lineCol(node.End())),
	}
}

func getObjectPathNode(pkg Package, fset *token.FileSet, o types.Object) (nodes []ast.Node, ident *ast.Ident, err error) {
	nodes, _ = getPathNodes(pkg, fset, o.Pos(), o.Pos())
	if len(nodes) == 0 {
		ip := pkg.GetImport(o.Pkg().Path())
		if ip == nil {
			return nil, nil,
				fmt.Errorf("import package %s of package %s does not exist", o.Pkg().Path(), pkg.GetTypes().Path())
		}

		nodes, err = getPathNodes(ip, fset, o.Pos(), o.Pos())
		if err != nil {
			return nil, nil, err
		}
	}

	ident, err = fetchIdentFromPathNodes(fset, nodes)
	return
}

func getPathNodes(pkg Package, fset *token.FileSet, start, end token.Pos) ([]ast.Node, error) {
	nodes, _ := astPathEnclosingInterval(pkg, fset, start, end)
	if len(nodes) == 0 {
		s := fset.Position(start)
		return nodes, fmt.Errorf("no node found at %s offset %d", s, s.Offset)
	}

	return nodes, nil
}

func fetchIdentFromPathNodes(fset *token.FileSet, nodes []ast.Node) (*ast.Ident, error) {
	firstNode := nodes[0]
	switch node := firstNode.(type) {
	case *ast.Ident:
		return node, nil
	default:
		return nil, newInvalidNodeError(fset, firstNode)
	}
}

// pathEnclosingInterval returns the PackageInfo and ast.Node that
// contain source interval [start, end), and all the node's ancestors
// up to the AST root.  It searches all ast.Files of all packages in prog.
// exact is defined as for astutil.pathEnclosingInterval.
//
// The zero value is returned if not found.
//
func astPathEnclosingInterval(pkg Package, fset *token.FileSet, start, end token.Pos) (path []ast.Node, exact bool) {
	path, exact = doEnclosingInterval(pkg, fset, start, end)
	return
}

func doEnclosingInterval(pkg Package, fset *token.FileSet, start, end token.Pos) ([]ast.Node, bool) {
	if pkg == nil || pkg.GetSyntax() == nil {
		return nil, false
	}

	for _, f := range pkg.GetSyntax() {
		if f.Pos() == token.NoPos {
			// This can happen if the parser saw
			// too many errors and bailed out.
			// (Use parser.AllErrors to prevent that.)
			continue
		}
		if !tokenFileContainsPos(fset.File(f.Pos()), start) {
			continue
		}
		if path, exact := astutil.PathEnclosingInterval(f, start, end); path != nil {
			return path, exact
		}
	}

	return nil, false
}

func findObject(pkg Package, o types.Object) types.Object {
	for _, def := range pkg.GetTypesInfo().Defs {
		if def == nil {
			continue
		}
		if def.Name() == o.Name() {
			return def
		}
	}

	return nil
}
