// Command declspans prints each top-level declaration of the non-test Go
// files named on the command line, with its line range, its doc comment
// included, so a reader can assign every line of a file to a purpose.
//
// Usage:
//
//	declspans <file.go>...
//
// Each output line is `<file>\t<first>-<last>\t<lines>\t<name>`. The lines
// before the first declaration (package clause and imports) print as
// `header`.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

func main() {
	fset := token.NewFileSet()
	for _, path := range os.Args[1:] {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		name := filepath.Base(path)
		total := fset.File(f.Pos()).LineCount()
		prev := 0
		for _, d := range f.Decls {
			start, end := d.Pos(), d.End()
			label := ""
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
				label = "func " + x.Name.Name
				if x.Recv != nil && len(x.Recv.List) > 0 {
					label = "method " + recv(x.Recv.List[0].Type) + "." + x.Name.Name
				}
			case *ast.GenDecl:
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
				label = x.Tok.String()
				for _, s := range x.Specs {
					switch y := s.(type) {
					case *ast.TypeSpec:
						label += " " + y.Name.Name
					case *ast.ValueSpec:
						for _, n := range y.Names {
							label += " " + n.Name
						}
					case *ast.ImportSpec:
						label = "import"
					}
				}
			}
			first, last := fset.Position(start).Line, fset.Position(end).Line
			if prev == 0 && first > 1 {
				fmt.Printf("%s\t1-%d\t%d\theader\n", name, first-1, first-1)
			}
			fmt.Printf("%s\t%d-%d\t%d\t%s\n", name, first, last, last-first+1, label)
			prev = last
		}
		if prev < total {
			fmt.Printf("%s\t%d-%d\t%d\ttrailer\n", name, prev+1, total, total-prev)
		}
	}
}

func recv(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recv(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}
