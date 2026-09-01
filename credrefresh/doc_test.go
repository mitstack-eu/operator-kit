package credrefresh_test

import (
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// --- Provider neutrality -----------------------------------------------------

// providerNames are the cloud provider names credrefresh's exported surface
// and doc comment must never mention.
var providerNames = []string{"aws", "azure", "gcp", "stackit"}

// providerNamePattern matches any of providerNames as a whole word, case
// insensitively — a plain substring match would false-positive on "flaws",
// "draws" or "jaws" for the "aws" entry, which is not a provider mention at
// all.
var providerNamePattern = regexp.MustCompile(`(?i)\b(` + strings.Join(providerNames, "|") + `)\b`)

// mentionsProviderName reports whether text names a cloud provider as a
// whole word, and if so, which one (lowercased).
func mentionsProviderName(text string) (name string, found bool) {
	m := providerNamePattern.FindString(text)
	if m == "" {
		return "", false
	}
	return strings.ToLower(m), true
}

func TestMentionsProviderName_MatchesWholeWordsOnlyNotSubstrings(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"credrefresh is provider-agnostic", false},
		{"this generalises cloudid's aws-specific renewal", true},
		{"AWS S3 credentials", true},
		{"a design that flaws under load", false},
		{"draws a diagram of the reconcile loop", false},
		{"the jaws of a dead credential", false},
	}
	for _, c := range cases {
		if _, got := mentionsProviderName(c.text); got != c.want {
			t.Errorf("mentionsProviderName(%q) matched = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestPackageMakesNoProviderSpecificClaim pins the reason credrefresh exists
// apart from cloudid: cloudid is AWS-only by design and says so in its own
// package doc, and credrefresh is the part of it that generalises. A provider
// name anywhere in the package's exported surface or its doc comment would be
// exactly the kind of claim the split is meant to avoid.
func TestPackageMakesNoProviderSpecificClaim(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing credrefresh sources: %v", err)
	}
	pkg, ok := pkgs["credrefresh"]
	if !ok {
		t.Fatal(`no "credrefresh" package found among the non-test .go files in this directory`)
	}
	docPkg := doc.New(pkg, "", doc.AllDecls)

	check := func(where, text string) {
		if name, found := mentionsProviderName(text); found {
			t.Errorf("%s names a cloud provider (%q) — credrefresh is the provider-agnostic half split out of cloudid and must make no provider-specific claim", where, name)
		}
	}

	check("the package doc comment", docPkg.Doc)

	for _, fn := range docPkg.Funcs {
		check("exported func "+fn.Name, fn.Name)
	}
	for _, ty := range docPkg.Types {
		check("exported type "+ty.Name, ty.Name)
		for _, fn := range ty.Funcs {
			check("exported func "+fn.Name, fn.Name)
		}
		for _, m := range ty.Methods {
			check("exported method "+m.Name, m.Name)
		}
	}
	for _, c := range docPkg.Consts {
		for _, name := range c.Names {
			check("exported const "+name, name)
		}
	}
	for _, v := range docPkg.Vars {
		for _, name := range v.Names {
			check("exported var "+name, name)
		}
	}
}
