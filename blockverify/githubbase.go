package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const urlguardFile = "urlguard.go"

// githubOwnerRead names an account, not a credential or an endpoint, so a module reading only it makes no GitHub API call.
const githubOwnerRead = `Secrets["github"]["owner"]`

var (
	githubConsumingRe   = regexp.MustCompile(`Secrets\["github"\](\["[^"]*"\])?|Instances\["github"\]|api\.github\.com`)
	requestHostReturnRe = regexp.MustCompile(`return\s+"https?://"\s*\+\s*u\.(Host|Hostname\(\))`)
	allowlistFuncRe     = regexp.MustCompile(`func allowedGitHubHosts\(\)`)
	ghapiImportRe       = regexp.MustCompile(`operator-kit/(v[0-9]+/)?ghapi`)
	ghapiBaseCallRe     = regexp.MustCompile(`ghapi\.(Base|BaseFromEnv)\(`)
	rawAPIBaseConcatRe  = regexp.MustCompile(`\bapiBase\s*\+`)
	wholeLineCommentRe  = regexp.MustCompile(`^\s*//`)
)

// goSource is one non-test .go file, by its path relative to the target.
type goSource struct {
	rel     string
	content string
}

// nonTestGoSources returns an error for a file it could not read, because an unread file is never evidence that a rule holds.
func nonTestGoSources(target string) ([]goSource, error) {
	var sources []goSource
	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		sources = append(sources, goSource{rel: rel, content: string(raw)})
		return nil
	})
	sort.Slice(sources, func(i, j int) bool { return sources[i].rel < sources[j].rel })
	return sources, err
}

func isGitHubConsuming(sources []goSource) bool {
	for _, s := range sources {
		for _, m := range githubConsumingRe.FindAllString(s.content, -1) {
			if m != githubOwnerRead {
				return true
			}
		}
	}
	return false
}

// checkGitHubBaseAllowlist answers ok with an empty detail for a module that makes no GitHub API call, since the rule does not apply to it.
func checkGitHubBaseAllowlist(target string) (detail string, ok bool) {
	sources, err := nonTestGoSources(target)
	if err != nil {
		return fmt.Sprintf("could not scan %s for GitHub API use: %v", target, err), false
	}
	if !isGitHubConsuming(sources) {
		return "", true
	}

	binding, ok := githubBaseBinding(target, sources)
	if !ok {
		return binding, false
	}
	if hits := rawAPIBaseConcatenations(sources); len(hits) > 0 {
		return fmt.Sprintf("%s concatenates the raw apiBase into a URL at %s; route it through ghapi.Base or trustedGitHubBase first",
			target, strings.Join(hits, ", ")), false
	}
	return binding, true
}

// githubBaseBinding judges how the module resolves a request-supplied base: through urlguard.go when it carries one, else through the kit's ghapi.
func githubBaseBinding(target string, sources []goSource) (detail string, ok bool) {
	urlguard, hasURLGuard := sourceNamed(sources, urlguardFile)
	if !hasURLGuard {
		if file, adopted := ghapiAdoption(sources); adopted {
			return fmt.Sprintf("GitHub base resolved through ghapi in %s", file), true
		}
		return fmt.Sprintf("%s reads the GitHub API but has neither %s nor a ghapi.Base or ghapi.BaseFromEnv call, so the request could choose the host",
			target, urlguardFile), false
	}

	if hits := requestHostReturns(sources); len(hits) > 0 {
		return fmt.Sprintf("%s returns a base built from the request host at %s; return the allowlist entry instead",
			target, strings.Join(hits, ", ")), false
	}
	if !allowlistFuncRe.MatchString(urlguard.content) {
		return fmt.Sprintf("%s has no allowedGitHubHosts(), so trustedGitHubBase has no operator-owned allowlist to resolve against",
			filepath.Join(target, urlguardFile)), false
	}
	return fmt.Sprintf("GitHub base resolved through the allowlist in %s", urlguardFile), true
}

func sourceNamed(sources []goSource, rel string) (goSource, bool) {
	for _, s := range sources {
		if s.rel == rel {
			return s, true
		}
	}
	return goSource{}, false
}

func ghapiAdoption(sources []goSource) (file string, adopted bool) {
	for _, s := range sources {
		if ghapiImportRe.MatchString(s.content) && ghapiBaseCallRe.MatchString(s.content) {
			return s.rel, true
		}
	}
	return "", false
}

func requestHostReturns(sources []goSource) []string {
	return matchingLines(sources, func(line string) bool { return requestHostReturnRe.MatchString(line) })
}

// rawAPIBaseConcatenations skips whole-line comments, because prose describing the defect documents the rule rather than breaking it.
func rawAPIBaseConcatenations(sources []goSource) []string {
	return matchingLines(sources, func(line string) bool {
		return rawAPIBaseConcatRe.MatchString(line) &&
			!strings.Contains(line, "trustedGitHubBase") &&
			!wholeLineCommentRe.MatchString(line)
	})
}

func matchingLines(sources []goSource, match func(line string) bool) []string {
	var hits []string
	for _, s := range sources {
		for i, line := range strings.Split(s.content, "\n") {
			if match(line) {
				hits = append(hits, fmt.Sprintf("%s:%d", s.rel, i+1))
			}
		}
	}
	return hits
}
