package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mitstack-eu/operator-kit/v3/provenance"
)

func runVerify(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	recordPath := fs.String("record", "", "path to record.json")
	jwks := fs.String("jwks", "", "path or URL to the signing JWKS")
	repository := fs.String("repository", "", "the expected publishing repository")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stdout, err)
		return 2
	}
	if *recordPath == "" || *jwks == "" || *repository == "" {
		fmt.Fprintln(stdout, "blockprov verify: --record, --jwks and --repository are all required")
		return 2
	}

	recordRaw, err := os.ReadFile(*recordPath)
	if err != nil {
		fmt.Fprintf(stdout, "blockprov verify: reading record: %v\n", err)
		return 1
	}
	// Decode, not json.Unmarshal, which drops an undeclared field and would verify a record carrying an attacker-added one.
	rec, err := provenance.Decode(recordRaw)
	if err != nil {
		fmt.Fprintf(stdout, "blockprov verify: record is not valid: %v\n", err)
		return 1
	}

	jwksRaw, err := readPathOrURL(*jwks)
	if err != nil {
		fmt.Fprintf(stdout, "blockprov verify: reading JWKS: %v\n", err)
		return 1
	}
	resolve, err := provenance.ParseJWKS(jwksRaw)
	if err != nil {
		fmt.Fprintf(stdout, "blockprov verify: %v\n", err)
		return 1
	}

	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: *repository}); err != nil {
		fmt.Fprintf(stdout, "blockprov verify: record rejected: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "blockprov verify: record accepted")
	return 0
}
