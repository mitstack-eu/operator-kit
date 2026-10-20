package contract_test

// The word the retired ordered-list manifest shape was named for appears in no path or line of the contract, blockverify and cmd trees or the README, and this file spells it only in pieces so it never matches itself.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retiredWord matches anywhere in a path or line, so it also catches a plural, a past tense or a name built on the word.
var retiredWord = regexp.MustCompile("(?i)" + "s" + "tep")

var vocabularyRoots = []string{".", "../blockverify", "../cmd", "../README.md"}

func TestKit_NamesNoPartOfTheRetiredOrderedListShape(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	files := 0
	for _, root := range vocabularyRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(repoRoot, abs)
			if err != nil {
				return err
			}
			if retiredWord.MatchString(d.Name()) {
				hits = append(hits, rel+": the path names the retired word")
			}
			if d.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files++
			for i, line := range strings.Split(string(raw), "\n") {
				if retiredWord.MatchString(line) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
	}
	if files == 0 {
		t.Fatal("the guard read no file, so finding nothing proves nothing")
	}
	for _, h := range hits {
		t.Errorf("%s", h)
	}
}
