package hostexec_test

import (
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// RunOutput sends the script exactly as Run does, so a reader who finds it without Run's doc must not miss the CloudTrail constraint.
func TestRunOutputDocCarriesTheSameCloudTrailConstraintAsRun(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments)
	if err != nil || pkgs["hostexec"] == nil {
		t.Fatalf("parsing the hostexec sources: %v", err)
	}

	var runOutput *doc.Func
	for _, fn := range doc.New(pkgs["hostexec"], "", doc.AllDecls).Funcs {
		if fn.Name == "RunOutput" {
			runOutput = fn
		}
	}

	if runOutput == nil || !strings.Contains(runOutput.Doc, "CloudTrail") {
		t.Errorf("RunOutput doc = %+v, want an exported RunOutput whose doc names CloudTrail, since it shares sendSSMCommand with Run", runOutput)
	}
}
