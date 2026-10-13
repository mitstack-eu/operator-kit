package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/handler"
)

const appNamePlaceholder = "{appName}"

// namingSuite requires maxLength to equal the smallest room any derived name leaves once its fixed text is removed from its limit.
func namingSuite(target string) SuiteResult {
	const name = "naming"

	path := filepath.Join(target, manifestFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return SuiteResult{name, "skipped", fmt.Sprintf(
			"%s has no %s (trust-plumbing reports that), so there is no app-name limit to check", target, manifestFile)}
	}
	if err != nil {
		return SuiteResult{name, "fail", fmt.Sprintf("could not read %s: %v", path, err)}
	}

	naming, err := handler.ManifestAppNameNaming(raw)
	if err != nil {
		return SuiteResult{name, "fail", path + ": " + err.Error()}
	}
	if naming == nil {
		return SuiteResult{name, "skipped",
			"manifest.yaml declares no spec.naming.appName, so there is no app-name limit to check"}
	}
	if len(naming.DerivedNames) == 0 {
		return SuiteResult{name, "fail", fmt.Sprintf(
			"%s: spec.naming.appName declares maxLength %d but no derivedNames, so no derived name backs it", path, naming.MaxLength)}
	}

	var problems []string
	computed, setBy := 0, ""
	for _, d := range naming.DerivedNames {
		switch n := strings.Count(d.Template, appNamePlaceholder); {
		case n == 0:
			problems = append(problems, fmt.Sprintf("derived name %q has no %s placeholder", d.Template, appNamePlaceholder))
			continue
		case n > 1:
			problems = append(problems, fmt.Sprintf(
				"derived name %q uses the %s placeholder more than once", d.Template, appNamePlaceholder))
			continue
		}
		fixed := len(d.Template) - len(appNamePlaceholder)
		if fixed >= d.Limit {
			problems = append(problems, fmt.Sprintf(
				"derived name %q has %d characters of fixed text, which reaches its limit of %d and leaves no room for the app name",
				d.Template, fixed, d.Limit))
			continue
		}
		if room := d.Limit - fixed; setBy == "" || room < computed {
			computed, setBy = room, d.Template
		}
	}
	if len(problems) > 0 {
		return SuiteResult{name, "fail", path + ": spec.naming.appName: " + strings.Join(problems, "; ")}
	}

	if naming.MaxLength != computed {
		return SuiteResult{name, "fail", fmt.Sprintf(
			"%s: spec.naming.appName declares maxLength %d, but the derived names allow an app name of at most %d, set by %q",
			path, naming.MaxLength, computed, setBy)}
	}
	return SuiteResult{name, "pass", fmt.Sprintf(
		"spec.naming.appName maxLength %d matches the derived names, set by %q", computed, setBy)}
}
