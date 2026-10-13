package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const kitModulePath = "github.com/mitstack-eu/operator-kit"

// kitVersionFloor is held textually equal to scaffold.KitVersionFloor by a test, because this module cannot import that package; bump it only on a deliberate floor raise.
const kitVersionFloor = "v0.1.0"

const kitShimFile = "kitshim.go"

// kitOwnedFuncs are the trust-boundary functions only the kit may declare, so a block's local copy cannot diverge from the contract.
var kitOwnedFuncs = []string{
	"verifyReplay",
	"verifyTimestampSkew",
	"verifyEd25519Sig",
	"publicKeyFromJWKS",
	"fetchJWKSKey",
	"MintOBO",
	"VerifyOBO",
	"appendRecentLog",
	"clearRecentLogs",
	"recentLogSnapshot",
	"EmitProgress",
	"EmitCompleted",
	"EmitFailed",
	"scrubEventDetail",
}

// trustPlumbingSuite is textual and static so it still fails meaningfully on a module that does not compile, and it alone fails a missing manifest.yaml.
func trustPlumbingSuite(target string) SuiteResult {
	const name = "trust-plumbing"

	contract, detail, ok := contractRule(target)
	if !ok {
		return SuiteResult{name, "fail", detail}
	}
	if detail, ok := checkKitDependency(target); !ok {
		return SuiteResult{name, "fail", detail}
	}
	var binding string
	if contract == kitV1Contract {
		file, ok := checkKitHandlerImport(target)
		if !ok {
			return SuiteResult{name, "fail", file}
		}
		binding = fmt.Sprintf("%s imports %s (%s waived)", file, kitHandlerPackage, kitShimFile)
	} else if _, err := os.Stat(filepath.Join(target, kitShimFile)); err != nil {
		return SuiteResult{name, "fail", fmt.Sprintf("%s has no %s binding it to the kit", target, kitShimFile)}
	} else if binding, ok = checkKitShimParity(target); !ok {
		return SuiteResult{name, "fail", binding}
	}
	if detail, ok := checkNoOwnedSymbolRedeclared(target); !ok {
		return SuiteResult{name, "fail", detail}
	}
	if _, err := os.Stat(filepath.Join(target, manifestFile)); err != nil {
		return SuiteResult{name, "fail", fmt.Sprintf("%s is missing manifest.yaml", target)}
	}
	githubBase, ok := checkGitHubBaseAllowlist(target)
	if !ok {
		return SuiteResult{name, "fail", githubBase}
	}

	pass := fmt.Sprintf(
		"depends on %s at or above the floor %s, %s, no kit-owned symbol redeclared, manifest.yaml present",
		kitModulePath, kitVersionFloor, binding)
	if githubBase != "" {
		pass += ", " + githubBase
	}
	if contract == kitV1Contract {
		pass = kitV1Contract + " rule: " + pass
	}
	return SuiteResult{name, "pass", pass}
}

var kitVersionRe = regexp.MustCompile(regexp.QuoteMeta(kitModulePath) + ` v(\d+\.\d+\.\d+)`)

func checkKitDependency(target string) (detail string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		return fmt.Sprintf("%s has no go.mod to depend on the shared operator-kit module", target), false
	}
	content := string(raw)
	if !strings.Contains(content, kitModulePath) {
		return fmt.Sprintf("%s/go.mod does not depend on the shared operator-kit module (want require %s)", target, kitModulePath), false
	}

	m := kitVersionRe.FindStringSubmatch(content)
	if m != nil {
		have := "v" + m[1]
		if have != kitVersionFloor && versionLess(have, kitVersionFloor) {
			return fmt.Sprintf("%s pins kit %s, below the floor %s", target, have, kitVersionFloor), false
		}
	}
	return "", true
}

func versionLess(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

func checkNoOwnedSymbolRedeclared(target string) (detail string, ok bool) {
	type hit struct{ fn, file string }
	var hits []hit

	_ = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == kitShimFile {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		content := string(raw)
		rel, err := filepath.Rel(target, path)
		if err != nil {
			rel = path
		}
		for _, fn := range kitOwnedFuncs {
			if funcDeclRe(fn).MatchString(content) {
				hits = append(hits, hit{fn: fn, file: rel})
			}
		}
		return nil
	})

	if len(hits) == 0 {
		return "", true
	}

	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s() in %s", h.fn, h.file))
	}
	return fmt.Sprintf(
		"%s redeclares kit-owned symbol(s): %s — delete it and use the kit; a local copy silently diverges from the contract",
		target, strings.Join(parts, ", "),
	), false
}

func funcDeclRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\(`)
}
