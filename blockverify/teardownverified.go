package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const verifiedTeardownNeedle = "func TestUnsubscribeVerifies"

var (
	teardownImportRe   = regexp.MustCompile(`^github\.com/mitstack-eu/operator-kit(/v[0-9]+)?/teardown$`)
	verificationFuncRe = regexp.MustCompile(`^(Survivor|Verify|Absent)$`)
)

// verificationCall is one call into the kit's teardown rule, at file:line.
type verificationCall struct {
	at    string
	gated bool
}

// checkVerifiedTeardown holds a legacy block to asking its provider again after a delete, because a teardown that never asks reports the same success as one that did.
func checkVerifiedTeardown(target string) (detail string, ok bool) {
	calls, err := teardownVerificationCalls(target)
	if err != nil {
		return fmt.Sprintf("could not scan %s for a call into the kit's teardown rule: %v", target, err), false
	}

	var unconditional, gated []string
	for _, c := range calls {
		if c.gated {
			gated = append(gated, c.at)
		} else {
			unconditional = append(unconditional, c.at)
		}
	}
	if len(gated) > 0 {
		return fmt.Sprintf("%s calls the kit's teardown rule inside a comma-ok type assertion block at %s, so a client without that capability reports teardown success unverified",
			target, strings.Join(gated, ", ")), false
	}
	if len(unconditional) == 0 {
		return fmt.Sprintf("no non-test .go file under %s calls the kit's teardown Survivor, Verify or Absent, so a reported teardown success is never checked with the provider",
			target), false
	}

	if !strings.Contains(concatGoTestFiles(target), verifiedTeardownNeedle) {
		return fmt.Sprintf("%s is missing a %s... test proving a surviving resource fails the teardown", target, verifiedTeardownNeedle), false
	}
	passed, output := runGoTest(target, "TestUnsubscribeVerifies")
	if !passed {
		return detailFromGoTestOutput(output), false
	}

	file, _, _ := strings.Cut(unconditional[0], ":")
	return fmt.Sprintf("%s asks the provider again through the kit's teardown rule, outside any comma-ok block, and TestUnsubscribeVerifies* passes", file), true
}

func teardownVerificationCalls(target string) ([]verificationCall, error) {
	sources, err := nonTestGoSources(target)
	if err != nil {
		return nil, err
	}
	var calls []verificationCall
	for _, s := range sources {
		found, err := verificationCallsIn(s)
		if err != nil {
			return nil, err
		}
		calls = append(calls, found...)
	}
	return calls, nil
}

func verificationCallsIn(s goSource) ([]verificationCall, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, s.rel, s.content, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.rel, err)
	}
	pkg, ok := teardownImportName(file)
	if !ok {
		return nil, nil
	}
	finder := &verificationFinder{fset: fset, rel: s.rel, pkg: pkg}
	ast.Walk(finder, file)
	return finder.calls, nil
}

func teardownImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !teardownImportRe.MatchString(path) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return filepath.Base(path), true
	}
	return "", false
}

// verificationFinder records every teardown-rule call, marking one gated when an enclosing if opened a comma-ok type assertion.
type verificationFinder struct {
	fset  *token.FileSet
	rel   string
	pkg   string
	gated bool
	calls []verificationCall
}

func (f *verificationFinder) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.IfStmt:
		if opensCommaOkAssertion(n.Init) {
			f.walkGatedIf(n)
			return nil
		}
	case *ast.CallExpr:
		if f.isVerificationCall(n) {
			f.calls = append(f.calls, verificationCall{
				at:    fmt.Sprintf("%s:%d", f.rel, f.fset.Position(n.Pos()).Line),
				gated: f.gated,
			})
		}
	}
	return f
}

// walkGatedIf gates only the body, since an else branch runs when the assertion failed and is not reached through the optional capability.
func (f *verificationFinder) walkGatedIf(n *ast.IfStmt) {
	ast.Walk(f, n.Init)
	ast.Walk(f, n.Cond)
	wasGated := f.gated
	f.gated = true
	ast.Walk(f, n.Body)
	f.gated = wasGated
	if n.Else != nil {
		ast.Walk(f, n.Else)
	}
}

func (f *verificationFinder) isVerificationCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && recv.Name == f.pkg && verificationFuncRe.MatchString(sel.Sel.Name)
}

func opensCommaOkAssertion(init ast.Stmt) bool {
	assign, ok := init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return false
	}
	assertion, ok := assign.Rhs[0].(*ast.TypeAssertExpr)
	return ok && assertion.Type != nil
}
