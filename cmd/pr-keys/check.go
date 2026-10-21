package main

// A merge or rebase delivers the commits and a squash delivers the title, so a pull request must name a key in both.

import (
	"fmt"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/boardkeys"
)

type commit struct {
	Subject string
	Body    string
}

func check(title string, commits []commit) error {
	hasCommitKey := false
	for _, c := range commits {
		if namesKey(c.Subject, c.Body) {
			hasCommitKey = true
			break
		}
	}
	hasTitleKey := namesKey(title, "")
	if hasCommitKey && hasTitleKey {
		return nil
	}

	var missing []string
	if !hasCommitKey {
		missing = append(missing, "commits")
	}
	if !hasTitleKey {
		missing = append(missing, "title")
	}
	return fmt.Errorf("pr-keys: no board key in the pull request's %s", strings.Join(missing, " and "))
}

// A reference passes too, since a change whose ticket also needs a release elsewhere must leave the key open.
func namesKey(subject, body string) bool {
	return len(boardkeys.Message(subject, body)) > 0 || len(boardkeys.References(subject, body)) > 0
}
