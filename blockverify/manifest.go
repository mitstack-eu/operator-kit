package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// manifestSuite fails a manifest the kit's parser refuses, a metadata.id that is no block id, and a changed spec.version without a release class and note.
func manifestSuite(target, baseManifest string) SuiteResult {
	const name = "manifest"

	path := filepath.Join(target, manifestFile)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return SuiteResult{name, "skipped", fmt.Sprintf(
			"%s has no %s (trust-plumbing reports that), so there is no manifest to check", target, manifestFile)}
	}
	m, err := readManifest(path)
	if err != nil {
		return SuiteResult{name, "fail", err.Error()}
	}

	var problems []string
	if id := m.ID(); id != "" {
		if _, _, err := contract.ParseBlockID(id); err != nil {
			problems = append(problems, "metadata.id: "+err.Error())
		}
	}
	note, problem, err := compareVersion(m, baseManifest)
	switch {
	case err != nil:
		problems = append(problems, err.Error())
	case problem != "":
		problems = append(problems, problem)
	}
	if len(problems) > 0 {
		return SuiteResult{name, "fail", path + ": " + strings.Join(problems, "; ")}
	}
	return SuiteResult{name, "pass", "the " + m.APIVersion + " manifest parses; " + note}
}

func readManifest(path string) (contract.Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contract.Manifest{}, fmt.Errorf("%s is present but could not be read, so what it declares is unknown: %v", path, err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		return contract.Manifest{}, fmt.Errorf("%s: %v", path, err)
	}
	return m, nil
}

// compareVersion treats an unreadable base as an error, never an absence, and an unparsable base as an unknown version so a pull request fixing it is not held back.
func compareVersion(m contract.Manifest, base string) (note, problem string, err error) {
	if base == "" {
		return "no --base-manifest given, so spec.version was not compared", "", nil
	}
	if _, statErr := os.Lstat(base); errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Sprintf("base manifest %s does not exist, so the block is new and spec.version was not compared", base), "", nil
	}
	raw, err := os.ReadFile(base)
	if err != nil {
		return "", "", fmt.Errorf("base manifest %s is present but could not be read, so spec.version was not compared: %v", base, err)
	}
	version := m.Version()
	missing := missingRelease(m)
	parsed, parseErr := contract.ParseManifest(raw)
	if parseErr != nil {
		unknown := fmt.Sprintf("base manifest %s could not be parsed (%v), so its spec.version is unknown", base, parseErr)
		if len(missing) > 0 {
			return "", fmt.Sprintf("%s and spec.version %q needs %s", unknown, version, strings.Join(missing, " and ")), nil
		}
		return unknown + "; spec.version " + version + " carries a release class and note", "", nil
	}
	baseVersion := parsed.Version()
	if baseVersion == version {
		return fmt.Sprintf("spec.version %s is unchanged from the base manifest", version), "", nil
	}
	if len(missing) > 0 {
		return "", fmt.Sprintf("spec.version changed from %q in the base manifest to %q, which needs %s",
			baseVersion, version, strings.Join(missing, " and ")), nil
	}
	return fmt.Sprintf("spec.version changed from %s to %s with a release class and note", baseVersion, version), "", nil
}

func missingRelease(m contract.Manifest) []string {
	var missing []string
	if m.ReleaseClass() == "" {
		missing = append(missing, "spec.releaseClass")
	}
	if m.ReleaseNote() == "" {
		missing = append(missing, "spec.releaseNote")
	}
	return missing
}
