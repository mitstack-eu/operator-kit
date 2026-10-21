// Package kitbinding, part of mitstack's own tooling, holds the one definition of a block importing the kit's handler, so blockverify and the platform's operator gates cannot disagree about a source tree.
package kitbinding

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// HandlerPackage is the import path of the kit's handler package.
const HandlerPackage = "github.com/mitstack-eu/operator-kit/v3/handler"

// earlierHandlerPackages are still bound, since a block on an earlier major release keeps working until its next change moves it.
var earlierHandlerPackages = []string{
	"github.com/mitstack-eu/operator-kit/handler",
	"github.com/mitstack-eu/operator-kit/v2/handler",
}

// HandlerImportFiles returns the sorted, slash-separated paths under dir of non-test files a default build compiles that import HandlerPackage or its path in an earlier major release, and fails the scan on any file it cannot judge.
func HandlerImportFiles(dir string) ([]string, error) {
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && skippedDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		match, err := build.Default.MatchFile(filepath.Dir(path), name)
		if err != nil {
			return fmt.Errorf("%s: matching build constraints: %w", path, err)
		}
		if !match {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parsing imports of %s: %w", path, err)
		}
		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err == nil && (p == HandlerPackage || slices.Contains(earlierHandlerPackages, p)) {
				rel, err := filepath.Rel(dir, path)
				if err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				found = append(found, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

func skippedDir(name string) bool {
	return name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")
}
