// Package boardkeys_test pins the delivered-key contract.
package boardkeys_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/boardkeys"
)

// k writes each "#" in s as the board's key prefix, so "#12: fix" is a subject delivering key 12.
func k(s string) string { return strings.ReplaceAll(s, "#", "MIT-") }

// keys turns numbers into board keys.
func keys(ns ...string) []string {
	var out []string
	for _, n := range ns {
		out = append(out, k("#"+n))
	}
	return out
}

func TestSubject(t *testing.T) {
	cases := []struct {
		name, subject string
		want          []string
	}{
		{"a single leading key delivers", "#3118: text", keys("3118")},
		{"two space-separated leading keys both deliver", "#1263 #1264: text", keys("1263", "1264")},
		{"comma-separated leading keys both deliver", "#1, #2: t", keys("1", "2")},
		{"spaced plus-separated leading keys both deliver", "#1 + #2: t", keys("1", "2")},
		{"slash-separated leading keys both deliver", "#1/#2: t", keys("1", "2")},
		{"leading whitespace before the first key is allowed", "   #1: t", keys("1")},
		{"a key that is not leading delivers nothing", "chore: bump platform submodule to abc (#1092 automation)", nil},
		{"a key in a parenthetical after an earlier colon delivers nothing", "PM audit trail: something (#1055): the spine #684 blocks it", nil},
		{"a key after the terminating colon is prose even when the subject delivers", "#1: fix, supersedes #2", keys("1")},
		{"a range after a key breaks the leading list before the colon", "#1475-1477: fix", nil},
		{"a range in prose delivers nothing", "blocking #1475-1477", nil},
		{"a lower-case key delivers nothing, since the prefix is case-sensitive", "mit-1: t", nil},
		{"an empty subject delivers nothing", "", nil},
		{"keys sort numerically whatever their order of appearance", "#1264 #1263: text", keys("1263", "1264")},
		{"keys sort numerically, not lexically", "#10 #9: text", keys("9", "10")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := k(tc.subject)

			got := boardkeys.Subject(subject)

			if !slices.Equal(got, tc.want) {
				t.Errorf("Subject(%q) = %v, want %v", subject, got, tc.want)
			}
		})
	}
}

func TestMessageAndReferences(t *testing.T) {
	cases := []struct {
		name, subject, body string
		want, wantRefs      []string
	}{
		{"a Jira trailer delivers every key it lists", "", "Jira: #1301 #1302 #1303", keys("1301", "1302", "1303"), nil},
		{"a lower-case jira keyword delivers", "", "jira: #5", keys("5"), nil},
		{"leading whitespace before a Jira trailer is allowed", "", "   Jira: #9", keys("9"), nil},
		{"a Jira-refs trailer only references its keys, since the delivering keyword is exactly Jira:", "", "Jira-refs: #7", nil, keys("7")},
		{"a trailing refs group only references its keys", "export the class (refs #5141, #5142)", "", nil, keys("5141", "5142")},
		{"a refs group before more text is prose", "export the class (refs #5141) for blocks", "", nil, nil},
		{"a delivering subject and a refs trailer each keep their own keys", "#5: fix", "Jira-refs: #6", keys("5"), keys("6")},
		{"a key in an ordinary body line delivers nothing", "", "See #8 for context on this change", nil, nil},
		{"several Jira trailer lines combine", "", "context line explaining the change\nJira: #1\nmore prose that mentions #99 in passing\nJira: #2", keys("1", "2"), nil},
		{"subject and trailer keys combine sorted and deduplicated", "#5: fix", "Jira: #5 #6", keys("5", "6"), nil},
		{"a subject alone delivers its key", "#3118: text", "just a body line, no trailer", keys("3118"), nil},
		{"a non-leading subject key adds nothing to a trailer", "chore: bump platform submodule to abc (#1092 automation)", "Jira: #5", keys("5"), nil},
		{"an empty subject and body deliver nothing", "", "", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, body := k(tc.subject), k(tc.body)

			got, refs := boardkeys.Message(subject, body), boardkeys.References(subject, body)

			if !slices.Equal(got, tc.want) || !slices.Equal(refs, tc.wantRefs) {
				t.Errorf("Message, References(%q, %q) = %v, %v; want %v, %v", subject, body, got, refs, tc.want, tc.wantRefs)
			}
		})
	}
}
