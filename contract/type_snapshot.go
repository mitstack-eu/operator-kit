package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// TypeSnapshot is one versioned snapshot of the resource types the platform serves, which manifests are judged against rather than the kit's own catalogue.
type TypeSnapshot struct {
	Version int            `json:"version"`
	Types   []SnapshotType `json:"types"`
}

// SnapshotType is one resource type of a snapshot.
type SnapshotType struct {
	ID           string            `json:"id"`
	Capability   string            `json:"capability"`
	Kind         string            `json:"kind"`
	Scope        string            `json:"scope"`
	Stateful     bool              `json:"stateful"`
	Pricing      SnapshotPricing   `json:"pricing"`
	Category     string            `json:"category"`
	Languages    []string          `json:"languages,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
	ReliesOn     []string          `json:"reliesOn,omitempty"`
	Hosts        string            `json:"hosts,omitempty"`
	Outputs      []SnapshotOutput  `json:"outputs"`
	Observe      string            `json:"observe,omitempty"`
	Absence      string            `json:"absence,omitempty"`
	TimeBudgets  map[string]string `json:"timeBudgets,omitempty"`
	Failures     []SnapshotFailure `json:"failures,omitempty"`
	Version      int               `json:"version"`

	// EndsWithSubscription marks a type whose resource ends with the block's unsubscribe, since its absence cannot be observed.
	EndsWithSubscription bool `json:"endsWithSubscription,omitempty"`

	// Parts are the objects that exist only to serve one resource of the type, which the tag guard accounts to that resource.
	Parts []SnapshotPart `json:"parts,omitempty"`
}

// SnapshotPart names a type the snapshot holds whose objects serve one resource of the owning type, and the most of them one resource holds.
type SnapshotPart struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type SnapshotOutput struct {
	Name     string `json:"name"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

// SnapshotPricing says whether a resource of the type costs money and the price list it is costed against.
type SnapshotPricing struct {
	Priced bool   `json:"priced"`
	Key    string `json:"key,omitempty"`
}

type SnapshotFailure struct {
	Class string `json:"class"`
	Cause string `json:"cause"`
}

// ParseTypeSnapshot reads raw as a type snapshot, refusing one holding no type, a type id twice, or a part checkParts refuses.
func ParseTypeSnapshot(raw []byte) (TypeSnapshot, error) {
	var snap TypeSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return TypeSnapshot{}, fmt.Errorf("contract: the type snapshot is not valid JSON: %w", err)
	}
	if len(snap.Types) == 0 {
		return TypeSnapshot{}, errors.New("contract: the type snapshot holds no type")
	}
	held := make(map[string]bool, len(snap.Types))
	for _, t := range snap.Types {
		if held[t.ID] {
			return TypeSnapshot{}, fmt.Errorf("contract: the type snapshot holds type %q twice", t.ID)
		}
		held[t.ID] = true
	}
	for _, t := range snap.Types {
		if err := checkParts(t, held); err != nil {
			return TypeSnapshot{}, err
		}
	}
	return snap, nil
}

// checkParts refuses a part with no type, a type the snapshot does not hold, or a count below one, since the tag guard gives room by part type.
func checkParts(t SnapshotType, held map[string]bool) error {
	for _, p := range t.Parts {
		if p.Type == "" {
			return fmt.Errorf("contract: type %q lists a part with no type", t.ID)
		}
		if !held[p.Type] {
			return fmt.Errorf("contract: type %q lists part type %q, which the snapshot does not hold", t.ID, p.Type)
		}
		if p.Count < 1 {
			return fmt.Errorf("contract: type %q lists part type %q with count %d, but a part's count is at least one", t.ID, p.Type, p.Count)
		}
	}
	return nil
}

func (s TypeSnapshot) Lookup(id string) (SnapshotType, bool) {
	i := slices.IndexFunc(s.Types, func(t SnapshotType) bool { return t.ID == id })
	if i < 0 {
		return SnapshotType{}, false
	}
	return s.Types[i], true
}

func (t SnapshotType) Output(name string) (SnapshotOutput, bool) {
	i := slices.IndexFunc(t.Outputs, func(o SnapshotOutput) bool { return o.Name == name })
	if i < 0 {
		return SnapshotOutput{}, false
	}
	return t.Outputs[i], true
}
