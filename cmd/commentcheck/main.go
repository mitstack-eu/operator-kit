// Command commentcheck, part of mitstack's own tooling, judges the comments a change adds, the comment share of each file it touches, and whether a change is comment-only.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("commentcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: commentcheck [-repo DIR] -base REV [-same-code] [path ...]\n       commentcheck [-repo DIR] -report [path ...]")
		flags.PrintDefaults()
	}
	repoDir := flags.String("repo", ".", "repository root")
	base := flags.String("base", "", "the commit the working tree is compared with")
	same := flags.Bool("same-code", false, "prove the change touches only comments")
	report := flags.Bool("report", false, "print the comment share of each path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	code, err := dispatch(stdout, *repoDir, *base, *same, *report, paths)
	if err != nil {
		fmt.Fprintln(stderr, "commentcheck:", err)
		return 2
	}
	return code
}

func dispatch(stdout io.Writer, dir, base string, same, report bool, paths []string) (int, error) {
	switch {
	case same && report:
		return 2, errors.New("-same-code and -report cannot be combined")
	case report:
		return runReport(stdout, dir, paths)
	case base == "":
		return 2, errors.New("-base is required unless -report is given")
	}
	g := gitRepo{dir}
	sha, short, err := g.resolve(base)
	if err != nil {
		return 2, err
	}
	if same {
		return runSameCode(stdout, g, sha, short, paths)
	}
	return runCheck(stdout, g, sha, short, paths)
}
