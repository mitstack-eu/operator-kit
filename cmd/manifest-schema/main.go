// Command manifest-schema prints the JSON Schema for a block manifest under
// contract.ManifestAPIVersion, as contract.GenerateManifestSchema builds it
// from the kit's manifest types and their json tags.
//
// Usage:
//
//	go run ./cmd/manifest-schema > contract/manifest.schema.json
//
// It exists so the schema the kit ships, which a contract test compares byte
// for byte with what the types generate, is generated from the types rather
// than written by hand.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run prints the manifest schema to stdout and returns 0. It takes no
// argument: given any, it writes a usage line to stderr and returns 2. It
// returns 1 when the schema cannot be built or stdout cannot be written.
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
