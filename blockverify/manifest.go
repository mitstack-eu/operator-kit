package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// manifestSuite appends each parser warning for a retired key to the pass detail and never fails on one.
func manifestSuite(target, baseManifest string) SuiteResult {
	const name = "manifest"

	path := filepath.Join(target, manifestFile)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return SuiteResult{name, "skipped", fmt.Sprintf(
			"%s has no %s (trust-plumbing reports that), so there are no outputs or reads to check", target, manifestFile)}
	}
	m, err := readManifest(path)
	if err != nil {
		return SuiteResult{name, "fail", err.Error()}
	}

	problems := registryProblems(m.V1Alpha)
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
	checked := "spec.outputs and spec.requires[].reads resolve through the resource registry; "
	if m.V1Alpha == nil {
		checked = "a " + m.APIVersion + " manifest declares no outputs or reads for the registry to resolve; "
	}
	return SuiteResult{name, "pass", checked + note + warningNotes(m.Warnings)}
}

func warningNotes(warnings []contract.ManifestWarning) string {
	var b strings.Builder
	for _, w := range warnings {
		fmt.Fprintf(&b, "; warning: %s: %s", w.Path, w.Message)
	}
	return b.String()
}

func readManifest(path string) (contract.Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contract.Manifest{}, fmt.Errorf("%s is present but could not be read, so its outputs and reads are unknown: %v", path, err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		return contract.Manifest{}, fmt.Errorf("%s: %v", path, err)
	}
	return m, nil
}

// registryProblems takes nil for a manifest under a version other than v1alpha, which declares neither outputs nor reads.
func registryProblems(m *contract.V1AlphaManifest) []string {
	if m == nil {
		return nil
	}
	var problems []string
	for _, out := range m.Outputs {
		if out == "" {
			problems = append(problems, "spec.outputs: a name must not be empty")
			continue
		}
		if _, ok := contract.LookupResource(out); !ok {
			problems = append(problems, fmt.Sprintf(
				"spec.outputs: %q is not in the resource registry (nearest registered name: %q)", out, contract.NearestResource(out)))
		}
	}
	for _, r := range m.Requires {
		for _, read := range r.Reads {
			if msg := readProblem(r.Capability, read); msg != "" {
				problems = append(problems, msg)
			}
		}
	}
	return problems
}

// readProblem judges a reads name with the same rule the platform applies at apply.
func readProblem(capability, name string) string {
	if name == "" {
		return fmt.Sprintf("spec.requires[%s].reads: a name must not be empty", capability)
	}
	entry, ok := contract.LookupResource(name)
	if !ok {
		return fmt.Sprintf("spec.requires[%s].reads: %q is not in the resource registry (nearest registered name: %q)",
			capability, name, contract.NearestResource(name))
	}
	for _, c := range v1alphaReadCapabilities[entry.Name] {
		if c == capability {
			return ""
		}
	}
	return fmt.Sprintf("spec.requires[%s].reads: %q is not an output of capability %q", capability, name, capability)
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
