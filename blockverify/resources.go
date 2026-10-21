package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	resourcesTestNeedle       = "func TestDeclaredResources("
	resourcesBlockCheckNeedle = "kittest.AssertBlock("
	guardsRanMarker           = "kittest.AssertBlock guards ran:"
)

func resourcesSuite(target string) SuiteResult {
	const name = "resources"

	path := filepath.Join(target, manifestFile)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return SuiteResult{name, "skipped", fmt.Sprintf(
			"%s has no %s (trust-plumbing reports that), so there are no declared resources to drive", target, manifestFile)}
	}
	m, err := readManifest(path)
	if err != nil {
		return SuiteResult{name, "fail", err.Error()}
	}

	tests := concatGoTestFiles(target)
	if !strings.Contains(tests, resourcesTestNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf(
			"%s declares its resources but has no TestDeclaredResources test to drive them through kittest.AssertBlock", target)}
	}
	if !strings.Contains(tests, resourcesBlockCheckNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf(
			"%s defines TestDeclaredResources but never calls kittest.AssertBlock, so no declared resource is driven", target)}
	}

	ok, output := runGoTest(target, "TestDeclaredResources", "-v")
	if !ok {
		return SuiteResult{name, "fail", resourceProblemLine(output)}
	}
	_, ran, logged := strings.Cut(output, guardsRanMarker)
	if !logged {
		return SuiteResult{name, "pass", "TestDeclaredResources passed, but kittest.AssertBlock logged no guards, so which ran is unknown"}
	}
	ran, _, _ = strings.Cut(ran, "\n")
	return SuiteResult{name, "pass", fmt.Sprintf(
		"TestDeclaredResources proves the block's %d declared resources hold the guards that ran: %s",
		len(m.Block.Spec.Resources), strings.TrimSpace(ran))}
}

func resourceProblemLine(output string) string {
	for _, l := range strings.Split(output, "\n") {
		if strings.Contains(l, `resource "`) {
			return strings.TrimSpace(l)
		}
	}
	return detailFromGoTestOutput(output)
}
