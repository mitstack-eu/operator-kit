// Command manifest-schema, part of mitstack's own tooling, prints the block manifest JSON Schema generated from the kit types: go run ./cmd/manifest-schema > contract/manifest.schema.json
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: manifest-schema > contract/manifest.schema.json (it takes no argument)")
		return 2
	}
	schema, err := contract.GenerateManifestSchema()
	if err != nil {
		fmt.Fprintf(stderr, "manifest-schema: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(schema); err != nil {
		fmt.Fprintf(stderr, "manifest-schema: %v\n", err)
		return 1
	}
	return 0
}
