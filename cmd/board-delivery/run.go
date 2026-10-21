package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mitstack-eu/operator-kit/v3/boardkeys"
	"github.com/mitstack-eu/operator-kit/v3/safe"
)

// Run writes every message, errors included, to stdout so a caller can capture it.
func Run(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("board-delivery", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", ".", "the git repository to read commits from")
	before := fs.String("before", "", "the push event's before sha — used when no board-synced anchor tag exists")
	after := fs.String("after", "", "the delivered sha: the range's upper bound and the delivery id posted to the board")
	remote := fs.String("remote", "origin", "the remote the board-synced anchor tag is pushed to")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, err)
		return 2
	}
	if *after == "" {
		fmt.Fprintln(stdout, "board-delivery: --after is required")
		return 2
	}

	anchor := readAnchorTag(*repo)
	rng := resolveRange(anchor, *before, *after)

	keys, err := deliveredKeys(*repo, rng)
	if err != nil {
		fmt.Fprintf(stdout, "board-delivery: reading commits in range %s: %v\n", rng, err)
		return 1
	}

	if len(keys) == 0 {
		fmt.Fprintf(stdout, "board-delivery: recorded nothing — range %s delivers no board keys\n", rng)
		return 0
	}

	baseURL := os.Getenv("TRACKER_BASE_URL")
	if baseURL == "" {
		fmt.Fprintln(stdout, "board-delivery: TRACKER_BASE_URL is unset — no board to record on")
		return 1
	}
	token := os.Getenv("TRACKER_API_TOKEN")

	if err := postDelivered(baseURL, token, *after, keys); err != nil {
		fmt.Fprintf(stdout, "board-delivery: %s\n", safe.LogSafe(err.Error(), token))
		return 1
	}

	if err := advanceAnchorTag(*repo, *remote, *after); err != nil {
		fmt.Fprintf(stdout, "board-delivery: recorded %d key(s) but failed to advance the %s tag: %v\n", len(keys), anchorTag, err)
		return 1
	}

	fmt.Fprintf(stdout, "board-delivery: recorded %d key(s) for %s: %s\n", len(keys), *after, strings.Join(keys, ", "))
	return 0
}

func deliveredKeys(repo, rng string) ([]string, error) {
	commits, err := commitsInRange(repo, rng)
	if err != nil {
		return nil, err
	}
	perCommit := make([][]string, len(commits))
	for i, c := range commits {
		perCommit[i] = boardkeys.Message(c.Subject, c.Body)
	}
	return boardkeys.Merge(perCommit...), nil
}
