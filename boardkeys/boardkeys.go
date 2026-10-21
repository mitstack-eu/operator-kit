// Package boardkeys, part of mitstack's own tooling, derives which board keys a commit delivers and which it names only as a reference.
package boardkeys

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var keyPattern = regexp.MustCompile(`MIT-[0-9]+`)

var leadingKeyList = regexp.MustCompile(`^\s*MIT-[0-9]+(?:(?:\s*[,+/]\s*|\s+)MIT-[0-9]+)*:`)

var jiraTrailer = regexp.MustCompile(`(?im)^[ \t]*jira:.*$`)

var refsGroup = regexp.MustCompile(`\(refs\s+MIT-[0-9]+(?:(?:\s*[,+/]\s*|\s+)MIT-[0-9]+)*\)\s*$`)

var refsTrailer = regexp.MustCompile(`(?im)^[ \t]*jira-refs:.*$`)

// Subject returns the keys a list anchored at the subject's start and closed by ':' names, so a key that merely appears early delivers nothing.
func Subject(line string) []string {
	return dedupeSorted(keyPattern.FindAllString(leadingKeyList.FindString(line), -1))
}

// Message returns the keys a commit delivers through its subject or a whole-line `Jira:` trailer, never a keyword in prose or a `Jira-refs:` look-alike.
func Message(subject, body string) []string {
	return Merge(Subject(subject), trailerKeys(jiraTrailer, body))
}

// References returns the keys a commit names without delivering them, in a parenthesised `refs` group ending its subject or a `Jira-refs:` trailer.
func References(subject, body string) []string {
	return Merge(keyPattern.FindAllString(refsGroup.FindString(subject), -1), trailerKeys(refsTrailer, body))
}

// Merge combines key lists, deduplicated and sorted by the number after the hyphen, so the tenth key follows the ninth.
func Merge(sets ...[]string) []string {
	var all []string
	for _, s := range sets {
		all = append(all, s...)
	}
	return dedupeSorted(all)
}

func trailerKeys(trailer *regexp.Regexp, body string) []string {
	var keys []string
	for _, line := range trailer.FindAllString(body, -1) {
		keys = append(keys, keyPattern.FindAllString(line, -1)...)
	}
	return keys
}

func dedupeSorted(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(keys))
	var out []string
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return keyNumber(out[i]) < keyNumber(out[j])
	})
	return out
}

func keyNumber(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, "MIT-"))
	if err != nil {
		return 0
	}
	return n
}
