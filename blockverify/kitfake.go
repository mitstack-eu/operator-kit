package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const kitFakeType = "FakeResource"

var kittestImportRe = regexp.MustCompile(`^github\.com/mitstack-eu/operator-kit(/v[0-9]+)?/kittest$`)

// refuseKitFake fails a block whose mandatory test, named by testPrefix, sits in a file that builds the kit's FakeResource, since such a test exercises the kit's fake instead of the block.
func refuseKitFake(target, testPrefix string) (detail string, ok bool) {
	files, err := testFilesBuildingKitFake(target, testPrefix)
	if err != nil {
		return fmt.Sprintf("could not scan %s for a %s test built over the kit's %s: %v", target, testPrefix, kitFakeType, err), false
	}
	if len(files) == 0 {
		return "", true
	}
	refusals := make([]string, 0, len(files))
	for _, f := range files {
		refusals = append(refusals, fmt.Sprintf("%s builds the kit's %s beside a %s* test", f, kitFakeType, testPrefix))
	}
	return strings.Join(refusals, "; ") + ", so that test judges the kit's fake rather than this block", false
}

func testFilesBuildingKitFake(target, testPrefix string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		builds, err := fileBuildsKitFake(path, testPrefix)
		if err != nil || !builds {
			return err
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		found = append(found, rel)
		return nil
	})
	return found, err
}

func fileBuildsKitFake(path, testPrefix string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	if !declaresTest(file, testPrefix) {
		return false, nil
	}
	pkg, ok := kittestImportName(file)
	if !ok {
		return false, nil
	}
	builds := false
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == kitFakeType {
			if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == pkg {
				builds = true
			}
		}
		return !builds
	})
	return builds, nil
}

func declaresTest(file *ast.File, prefix string) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, prefix) {
			return true
		}
	}
	return false
}

func kittestImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !kittestImportRe.MatchString(path) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return "kittest", true
	}
	return "", false
}
