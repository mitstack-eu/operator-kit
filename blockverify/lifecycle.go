package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/kitbinding"
)

const kitV1Contract = "kit/v1"

const kitHandlerPackage = kitbinding.HandlerPackage

const manifestFile = "manifest.yaml"

// contractRule never falls through to the legacy rule on an unreadable manifest or unknown value, which would judge the block by a contract it never declared.
func contractRule(target string) (rule, detail string, ok bool) {
	c, err := declaredContract(target)
	if err != nil {
		return "", err.Error(), false
	}
	switch c {
	case "", kitV1Contract:
		return c, "", true
	}
	return "", fmt.Sprintf(
		"%s declares lifecycle contract %q, which blockverify has no rule for (it knows %s, and no declared contract for the legacy rule)",
		filepath.Join(target, manifestFile), c, kitV1Contract), false
}

// declaredContract treats a present manifest that cannot be read or parsed as an error, because a read that could not run is never an absence.
func declaredContract(target string) (string, error) {
	path := filepath.Join(target, manifestFile)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s is present but could not be read, so the lifecycle contract that judges %s is unknown: %v", path, target, err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %v, so the lifecycle contract that judges %s is unknown", path, err, target)
	}
	switch {
	case m.Block != nil:
		return m.Block.Spec.Lifecycle.Contract, nil
	case m.V1Alpha != nil && m.V1Alpha.Spec.Lifecycle != nil:
		return m.V1Alpha.Spec.Lifecycle.Contract, nil
	}
	return "", nil
}

// checkKitHandlerImport fails a scan that could not run rather than reading it as an absent import.
func checkKitHandlerImport(target string) (detail string, ok bool) {
	files, err := kitbinding.HandlerImportFiles(target)
	if err != nil {
		return fmt.Sprintf("%s rule: could not scan %s for a non-test .go file importing %s: %v",
			kitV1Contract, target, kitHandlerPackage, err), false
	}
	if len(files) == 0 {
		return fmt.Sprintf("%s rule: no non-test .go file under %s imports %s, which is what binds a %s block to the kit",
			kitV1Contract, target, kitHandlerPackage, kitV1Contract), false
	}
	return files[0], true
}
