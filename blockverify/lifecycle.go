package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/kitbinding"
)

const kitV1Contract = "kit/v1"

const kitHandlerPackage = kitbinding.HandlerPackage

const manifestFile = "manifest.yaml"

// requireKitV1Contract fails a block whose manifest is absent, unreadable or declares a lifecycle contract other than kit/v1, the one contract blockverify judges.
func requireKitV1Contract(target string) (detail string, ok bool) {
	c, err := declaredContract(target)
	if err != nil {
		return err.Error(), false
	}
	if c != kitV1Contract {
		return fmt.Sprintf("%s declares lifecycle contract %q, which blockverify has no rule for (it knows %s)",
			filepath.Join(target, manifestFile), c, kitV1Contract), false
	}
	return "", true
}

// declaredContract treats a manifest that is absent, unreadable or refused as an error, because the contract that judges the block is then unknown.
func declaredContract(target string) (string, error) {
	path := filepath.Join(target, manifestFile)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s has no %s, so the lifecycle contract that judges it is unknown", target, manifestFile)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s is present but could not be read, so the lifecycle contract that judges %s is unknown: %v", path, target, err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %v, so the lifecycle contract that judges %s is unknown", path, err, target)
	}
	return m.Block.Spec.Lifecycle.Contract, nil
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
