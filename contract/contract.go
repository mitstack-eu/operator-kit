// Package contract, part of the core every block needs, holds the wire contract between the engine and a block, which the engine and every block consume from here rather than keeping a copy.
package contract

import (
	"embed"
	"fmt"
	"strings"
)

// EnvelopeSchema is the JSON Schema for the lifecycle webhook envelope.
//
//go:embed envelope.schema.json
var EnvelopeSchema []byte

// ResponseSchema is the JSON Schema for a lifecycle webhook response.
//
//go:embed response.schema.json
var ResponseSchema []byte

// GoldensDir is the directory inside Goldens holding the envelope fixtures.
const GoldensDir = "goldens"

// Goldens embeds the envelope fixtures, those prefixed InvalidPrefix being ones a block must reject.
//
//go:embed goldens/*.json
var Goldens embed.FS

// ResponseGoldensDir is the directory inside ResponseGoldens holding the response fixtures.
const ResponseGoldensDir = "response-goldens"

// ResponseGoldens embeds the response fixtures, those prefixed InvalidPrefix being ones the engine's decoder must reject.
//
//go:embed response-goldens/*.json
var ResponseGoldens embed.FS

// InvalidPrefix marks a golden file name as belonging to the malformed half of a fixture set.
const InvalidPrefix = "invalid-"

func splitGoldens(fsys embed.FS, dir string) (valid, invalid map[string][]byte, err error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("contract: reading %s: %w", dir, err)
	}

	valid = map[string][]byte{}
	invalid = map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := fsys.ReadFile(dir + "/" + name)
		if err != nil {
			return nil, nil, fmt.Errorf("contract: reading %s/%s: %w", dir, name, err)
		}
		key := strings.TrimSuffix(name, ".json")
		if strings.HasPrefix(key, InvalidPrefix) {
			invalid[key] = raw
		} else {
			valid[key] = raw
		}
	}
	return valid, invalid, nil
}

// ValidGoldens returns the envelope goldens a block must accept, erroring on an empty set since a vacuous acceptance set proves nothing.
func ValidGoldens() (map[string][]byte, error) {
	valid, _, err := splitGoldens(Goldens, GoldensDir)
	if err != nil {
		return nil, err
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("contract: no valid goldens found under %s", GoldensDir)
	}
	return valid, nil
}

// InvalidGoldens returns the envelope goldens a block must reject, erroring on an empty set since a vacuous rejection set proves nothing.
func InvalidGoldens() (map[string][]byte, error) {
	_, invalid, err := splitGoldens(Goldens, GoldensDir)
	if err != nil {
		return nil, err
	}
	if len(invalid) == 0 {
		return nil, fmt.Errorf("contract: no invalid goldens found under %s", GoldensDir)
	}
	return invalid, nil
}

// ValidResponseGoldens returns the response goldens the engine must decode intact, erroring on an empty set since a vacuous acceptance set proves nothing.
func ValidResponseGoldens() (map[string][]byte, error) {
	valid, _, err := splitGoldens(ResponseGoldens, ResponseGoldensDir)
	if err != nil {
		return nil, err
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("contract: no valid response goldens found under %s", ResponseGoldensDir)
	}
	return valid, nil
}

// InvalidResponseGoldens returns the response goldens the engine's decoder must reject, erroring on an empty set since a vacuous rejection set proves nothing.
func InvalidResponseGoldens() (map[string][]byte, error) {
	_, invalid, err := splitGoldens(ResponseGoldens, ResponseGoldensDir)
	if err != nil {
		return nil, err
	}
	if len(invalid) == 0 {
		return nil, fmt.Errorf("contract: no invalid response goldens found under %s", ResponseGoldensDir)
	}
	return invalid, nil
}
