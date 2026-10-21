// Command pr-keys, part of mitstack's own tooling, gates a pull request on whether its commit range and its own title both name a board key.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Run checks the commits in --base..--head and --title, writing every message to stdout and returning 2 on a bad flag, 1 on a failed check and 0 on a pass.
func Run(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("pr-keys", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", ".", "the git repository to read commits from")
	base := fs.String("base", "", "the pull request's base sha")
	head := fs.String("head", "", "the pull request's head sha")
	title := fs.String("title", "", "the pull request's title (default: the PR_TITLE environment variable)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, err)
		return 2
	}
	if *base == "" {
		fmt.Fprintln(stdout, "pr-keys: --base is required")
		return 2
	}
	if *head == "" {
		fmt.Fprintln(stdout, "pr-keys: --head is required")
		return 2
	}
	if *title == "" {
		*title = os.Getenv("PR_TITLE")
	}

	rng := *base + ".." + *head
	commits, err := commitsInRange(*repo, rng)
	if err != nil {
		fmt.Fprintf(stdout, "pr-keys: reading commits in range %s: %v\n", rng, err)
		return 1
	}

	if err := check(*title, commits); err != nil {
		fmt.Fprintln(stdout, err)
		fmt.Fprintln(stdout, "pr-keys: name the key in a commit and in the pull request's title, as a leading \"MIT-###:\" list that delivers it or a closing \"(refs MIT-###)\" group that only references it; a commit may use a Jira: or Jira-refs: trailer line instead")
		return 1
	}

	fmt.Fprintf(stdout, "pr-keys: %s and the title both carry a board key\n", rng)
	return 0
}
