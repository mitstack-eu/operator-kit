package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const kitShimCanonicalFile = "kitshim.go.canonical"

//go:embed kitshim.go.canonical
var canonicalKitShim string

const kitV2ImportPrefix = kitModulePath + "/v2/"

// expectedKitShim is the canonical with its import paths rewritten to the kit module path goMod requires, since a block on a v1 release imports the kit without the /v2 suffix.
func expectedKitShim(goMod string) string {
	if strings.Contains(goMod, kitModulePath+"/v2 ") {
		return canonicalKitShim
	}
	return strings.ReplaceAll(canonicalKitShim, kitV2ImportPrefix, kitModulePath+"/")
}

func checkKitShimParity(target string) (detail string, ok bool) {
	goMod, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		return fmt.Sprintf("could not read %s/go.mod to choose the canonical kitshim.go: %v", target, err), false
	}
	shim, err := os.ReadFile(filepath.Join(target, kitShimFile))
	if err != nil {
		return fmt.Sprintf("could not read %s/%s: %v", target, kitShimFile, err), false
	}
	if string(shim) != expectedKitShim(string(goMod)) {
		return fmt.Sprintf("%s/%s differs from the kit's canonical, compared with the kit module path its go.mod requires; the shim is a binding, so block code belongs in other files",
			target, kitShimFile), false
	}
	return kitShimFile + " matches the kit's canonical", true
}
