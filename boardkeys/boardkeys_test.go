// Package boardkeys_test pins the delivered-key contract.
package boardkeys_test

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/boardkeys"
)

func keysEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertKeys(t *testing.T, label string, got, want []string) {
	t.Helper()
	if !keysEqual(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestSubject_SingleLeadingKeyDelivers(t *testing.T) {
	got := boardkeys.Subject("MIT-3118: text")
	assertKeys(t, `Subject("MIT-3118: text")`, got, []string{"MIT-3118"})
}

func TestSubject_TwoSpaceSeparatedLeadingKeysBothDeliver(t *testing.T) {
	got := boardkeys.Subject("MIT-1263 MIT-1264: text")
	assertKeys(t, `Subject("MIT-1263 MIT-1264: text")`, got, []string{"MIT-1263", "MIT-1264"})
}

func TestSubject_CommaSeparatedLeadingKeysBothDeliver(t *testing.T) {
	got := boardkeys.Subject("MIT-1, MIT-2: t")
	assertKeys(t, `Subject("MIT-1, MIT-2: t")`, got, []string{"MIT-1", "MIT-2"})
}

func TestSubject_SpacedPlusSeparatedLeadingKeysBothDeliver(t *testing.T) {
	got := boardkeys.Subject("MIT-1 + MIT-2: t")
	assertKeys(t, `Subject("MIT-1 + MIT-2: t")`, got, []string{"MIT-1", "MIT-2"})
}

func TestSubject_SlashSeparatedLeadingKeysBothDeliver(t *testing.T) {
	got := boardkeys.Subject("MIT-1/MIT-2: t")
	assertKeys(t, `Subject("MIT-1/MIT-2: t")`, got, []string{"MIT-1", "MIT-2"})
}

func TestSubject_LeadingWhitespaceBeforeFirstKeyIsAllowed(t *testing.T) {
	got := boardkeys.Subject("   MIT-1: t")
	assertKeys(t, `Subject("   MIT-1: t")`, got, []string{"MIT-1"})
}

func TestSubject_KeyNotLeadingDeliversNothing(t *testing.T) {
	got := boardkeys.Subject("chore: bump platform submodule to abc (MIT-1092 automation)")
	if len(got) != 0 {
		t.Errorf("Subject(chore bump) = %v, want no keys — the key is not leading", got)
	}
}

func TestSubject_KeyInsideParentheticalAfterAnEarlierColonDeliversNothing(t *testing.T) {
	got := boardkeys.Subject("PM audit trail: something (MIT-1055): the spine MIT-684 blocks it")
	if len(got) != 0 {
		t.Errorf("Subject(PM audit trail) = %v, want no keys — this subject never begins with a key", got)
	}
}

func TestSubject_KeyAfterTerminatingColonIsProseEvenWhenSubjectAlsoDelivers(t *testing.T) {
	got := boardkeys.Subject("MIT-1: fix, supersedes MIT-2")
	assertKeys(t, `Subject("MIT-1: fix, supersedes MIT-2")`, got, []string{"MIT-1"})
}

func TestSubject_RangeExpressionAfterAKeyDeliversNothing(t *testing.T) {
	got := boardkeys.Subject("MIT-1475-1477: fix")
	if len(got) != 0 {
		t.Errorf(`Subject("MIT-1475-1477: fix") = %v, want no keys — the hyphen breaks the leading-list match before the colon`, got)
	}
}

func TestSubject_RangeExpressionInProseDeliversNothing(t *testing.T) {
	got := boardkeys.Subject("blocking MIT-1475-1477")
	if len(got) != 0 {
		t.Errorf(`Subject("blocking MIT-1475-1477") = %v, want no keys`, got)
	}
}

func TestSubject_LowercaseKeyDeliversNothing(t *testing.T) {
	got := boardkeys.Subject("mit-1: t")
	if len(got) != 0 {
		t.Errorf(`Subject("mit-1: t") = %v, want no keys — the key prefix is case-sensitive`, got)
	}
}

func TestSubject_EmptySubjectDeliversNothingWithoutError(t *testing.T) {
	got := boardkeys.Subject("")
	if len(got) != 0 {
		t.Errorf(`Subject("") = %v, want no keys`, got)
	}
}

func TestSubject_SortsNumericallyRegardlessOfAppearanceOrder(t *testing.T) {
	got := boardkeys.Subject("MIT-1264 MIT-1263: text")
	assertKeys(t, `Subject("MIT-1264 MIT-1263: text")`, got, []string{"MIT-1263", "MIT-1264"})
}

func TestSubject_SortsNumericallyNotLexically(t *testing.T) {
	// A lexical sort would put the tenth key before the ninth.
	got := boardkeys.Subject("MIT-10 MIT-9: text")
	assertKeys(t, `Subject("MIT-10 MIT-9: text")`, got, []string{"MIT-9", "MIT-10"})
}

func TestMessage_JiraTrailerDeliversAllListedKeys(t *testing.T) {
	got := boardkeys.Message("", "Jira: MIT-1301 MIT-1302 MIT-1303")
	assertKeys(t, "Message with Jira trailer", got, []string{"MIT-1301", "MIT-1302", "MIT-1303"})
}

func TestMessage_LowercaseJiraKeywordDelivers(t *testing.T) {
	got := boardkeys.Message("", "jira: MIT-5")
	assertKeys(t, "Message with lowercase jira: trailer", got, []string{"MIT-5"})
}

func TestMessage_LeadingWhitespaceBeforeJiraTrailerIsAllowed(t *testing.T) {
	got := boardkeys.Message("", "   Jira: MIT-9")
	assertKeys(t, "Message with indented Jira: trailer", got, []string{"MIT-9"})
}

func TestMessage_JiraRefsTrailerDeliversNothing(t *testing.T) {
	got := boardkeys.Message("", "Jira-refs: MIT-7")
	if len(got) != 0 {
		t.Errorf(`Message("", "Jira-refs: MIT-7") = %v, want no keys — the trailer keyword must be exactly "Jira:"`, got)
	}
}

func TestMessage_KeyInOrdinaryBodyLineDeliversNothing(t *testing.T) {
	got := boardkeys.Message("", "See MIT-8 for context on this change")
	if len(got) != 0 {
		t.Errorf("Message with a key in prose body = %v, want no keys", got)
	}
}

func TestMessage_MultipleJiraTrailerLinesAllCombine(t *testing.T) {
	body := strings.Join([]string{
		"context line explaining the change",
		"Jira: MIT-1",
		"more prose that mentions MIT-99 in passing",
		"Jira: MIT-2",
	}, "\n")
	got := boardkeys.Message("", body)
	assertKeys(t, "Message with two Jira trailer lines", got, []string{"MIT-1", "MIT-2"})
}

func TestMessage_SubjectAndTrailerKeysCombineSortedAndDeduped(t *testing.T) {
	got := boardkeys.Message("MIT-5: fix", "Jira: MIT-5 MIT-6")
	assertKeys(t, "Message combining subject and trailer", got, []string{"MIT-5", "MIT-6"})
}

func TestMessage_SubjectOnlyMatchesSubjectAlone(t *testing.T) {
	got := boardkeys.Message("MIT-3118: text", "just a body line, no trailer")
	assertKeys(t, "Message subject-only", got, []string{"MIT-3118"})
}

func TestMessage_NonLeadingSubjectKeyContributesNothingEvenWithATrailer(t *testing.T) {
	got := boardkeys.Message("chore: bump platform submodule to abc (MIT-1092 automation)", "Jira: MIT-5")
	assertKeys(t, "Message non-leading subject + trailer", got, []string{"MIT-5"})
}

func TestMessage_EmptySubjectAndBodyDeliverNothingWithoutError(t *testing.T) {
	got := boardkeys.Message("", "")
	if len(got) != 0 {
		t.Errorf(`Message("", "") = %v, want no keys`, got)
	}
}
