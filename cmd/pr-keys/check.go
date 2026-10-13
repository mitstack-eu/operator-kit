package main

// check.go is the pure core of the pr-keys gate: no git, no flags, no
// process exit. A pull request delivers a board key only when its commit
// range and its own title both carry one — a merge commit or a rebase
// delivers the commits, and a squash delivers the title — so both halves
// are checked here, through the boardkeys package rather than a second
// regex.

import (
	"fmt"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/boardkeys"
)

// commit is one commit's subject and body, the shape boardkeys.Message reads.
type commit struct {
	Subject string
	Body    string
}

// check reports whether title and commits together deliver a board key. It
// passes only when at least one commit in commits delivers a key through
// boardkeys.Message AND title delivers a key through boardkeys.Subject. An
// empty commits slice never passes, whatever title carries: a range with no
// commits carries no delivery intent to check.
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
