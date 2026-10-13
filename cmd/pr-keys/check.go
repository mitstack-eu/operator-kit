package main

// A merge or rebase delivers the commits and a squash delivers the title, so a pull request must carry a key in both.

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
		if len(boardkeys.Message(c.Subject, c.Body)) > 0 {
			hasCommitKey = true
			break
		}
	}
	hasTitleKey := len(boardkeys.Subject(title)) > 0
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
