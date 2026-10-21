package main

// The manifest-schema command prints the JSON Schema contract/manifest.schema.json must hold.

import (
	"bytes"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

func TestRun_PrintsTheGeneratedManifestSchema(t *testing.T) {
	want, err := contract.GenerateManifestSchema()
	if err != nil {
		t.Fatalf("contract.GenerateManifestSchema(): %v", err)
	}
	if len(want) == 0 {
		t.Fatal("contract.GenerateManifestSchema() is empty, so matching it proves nothing")
	}
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("Run(nil) = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Errorf("Run(nil) printed\n%s\nwant\n%s", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("Run(nil) wrote %q to stderr, want nothing", stderr.String())
	}
}

func TestRun_RefusesAnyArgumentWithAUsageError(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"-o", "x.json"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 2 {
			t.Errorf("Run(%q) = %d, want 2", args, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("Run(%q) printed %q to stdout, want nothing", args, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Errorf("Run(%q) wrote nothing to stderr, want a usage message", args)
		}
	}
}
