package main

import (
	"fmt"
	"testing"
)

// key returns board key n as a subject or title writes it.
func key(n int) string { return fmt.Sprintf("MIT-%d", n) }

func TestCheck(t *testing.T) {
	keyed := key(12) + ": fix the thing"
	referenced := "operator-kit exports the host-cannot-run refusal class (refs " + key(5141) + ")"
	cases := []struct {
		name    string
		title   string
		commits []commit
		wantErr string
	}{
		{"a keyed leading subject and a keyed title pass", keyed, []commit{{Subject: keyed}}, ""},
		{"a Jira trailer and a keyed title pass", keyed, []commit{{Subject: "fix the thing", Body: "Jira: " + key(12)}}, ""},
		{"a reference title and a commit ending in the same refs group pass", referenced, []commit{{Subject: referenced}}, ""},
		{"a reference title and a Jira-refs trailer pass", referenced, []commit{{Subject: "export the refusal class", Body: "Jira-refs: " + key(5141)}}, ""},
		{"a leading list of several keys passes", key(12) + ", " + key(13) + ": fix two things", []commit{{Subject: key(12) + ", " + key(13) + ": fix two things"}}, ""},
		{"a key only inside a merge subject's branch name is no delivery intent", key(3149) + ": delete gets instance config", []commit{
			{Subject: "delete gets instance config"},
			{Subject: "fix review comments"},
			{Subject: "Merge pull request #6 from mitstack-eu/fix/" + key(3149) + "-delete-gets-instance-config"},
		}, "pr-keys: no board key in the pull request's commits"},
		{"keyed commits under a keyless title name the title", "fix the thing", []commit{{Subject: keyed}}, "pr-keys: no board key in the pull request's title"},
		{"a keyless range and title name both", "fix the thing", []commit{{Subject: "fix the thing"}}, "pr-keys: no board key in the pull request's commits and title"},
		{"a key later in a subject is prose, not delivery", keyed, []commit{{Subject: "fix " + key(12) + " thing"}}, "pr-keys: no board key in the pull request's commits"},
		{"a key in an ordinary body line is not a Jira trailer", keyed, []commit{{Subject: "fix the thing", Body: "See " + key(12) + " for context"}}, "pr-keys: no board key in the pull request's commits"},
		{"an empty range never passes on the title alone", keyed, nil, "pr-keys: no board key in the pull request's commits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := check(tc.title, tc.commits)

			if got := fmt.Sprint(err); (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && got != tc.wantErr) {
				t.Errorf("check(%q, %+v) = %v, want %q", tc.title, tc.commits, err, tc.wantErr)
			}
		})
	}
}
