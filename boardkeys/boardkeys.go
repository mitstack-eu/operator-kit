// Package boardkeys, part of mitstack's own tooling, derives which board keys a commit delivers, counting only a leading key list in the subject terminated by ':' or a whole-line `Jira:` trailer.
package boardkeys

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var keyPattern = regexp.MustCompile(`MIT-[0-9]+`)

// Anchored to the line start so a key that merely appears early is not mistaken for a leading one.
var leadingKeyList = regexp.MustCompile(`^\s*MIT-[0-9]+(?:(?:\s*[,+/]\s*|\s+)MIT-[0-9]+)*:`)

// Anchored so a keyword buried in prose, or a look-alike such as `Jira-refs:`, never matches.
var jiraTrailer = regexp.MustCompile(`(?im)^[ \t]*jira:.*$`)

// Subject returns the board keys a commit subject's leading key list names, and nothing when there is none.
func Subject(line string) []string {
	prefix := leadingKeyList.FindString(line)
	if prefix == "" {
		return nil
	}
	return dedupeSorted(keyPattern.FindAllString(prefix, -1))
}

// Message returns the board keys a commit's subject and body together deliver, merged, deduped and sorted numerically.
func Message(subject, body string) []string {
	var trailerKeys []string
	for _, line := range jiraTrailer.FindAllString(body, -1) {
		trailerKeys = append(trailerKeys, keyPattern.FindAllString(line, -1)...)
	}
	return Merge(Subject(subject), trailerKeys)
}

// Merge combines several key lists into one, deduplicated and sorted numerically by the digits after the hyphen.
func Merge(sets ...[]string) []string {
	var all []string
	for _, s := range sets {
		all = append(all, s...)
	}
	return dedupeSorted(all)
}

// A lexical sort would put the tenth key before the ninth, so the sort is by the number after the hyphen.
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
