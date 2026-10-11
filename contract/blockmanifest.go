package contract

import (
	"fmt"
	"strings"
)

const ManifestAPIVersionV1Alpha = "mitstack/v1alpha"

const ManifestAPIVersion = "mitstack/v1beta"

// ParseManifest reads raw strictly by its apiVersion, refusing an unknown key or an alias or merge key, which bring in text from elsewhere, and naming the path in each error.
func ParseManifest(raw []byte) (Manifest, error) {
	root, err := parseYAML(string(raw))
	if err != nil {
		return Manifest{}, err
	}
	switch root.kind {
	case yamlNull:
		return Manifest{}, apiVersionError("")
	case yamlMap:
	default:
		if err := readable(root, ""); err != nil {
			return Manifest{}, err
		}
		return Manifest{}, mapEntries(root, "")
	}
	apiVersion, err := apiVersionOf(root)
	if err != nil {
		return Manifest{}, err
	}
	switch apiVersion {
	case ManifestAPIVersion:
		b, err := decodeBlockManifest(root)
		if err != nil {
			return Manifest{}, err
		}
		return Manifest{APIVersion: apiVersion, Block: b}, nil
	case ManifestAPIVersionV1Alpha:
		a, warnings, err := readV1Alpha(root)
		if err != nil {
			return Manifest{}, err
		}
		return Manifest{APIVersion: apiVersion, V1Alpha: a, Warnings: warnings}, nil
	}
	return Manifest{}, apiVersionError(apiVersion)
}

func apiVersionOf(root *yamlNode) (string, error) {
	n := root.get("apiVersion")
	if n == nil || isNull(n) {
		return "", nil
	}
	if err := readable(n, "apiVersion"); err != nil {
		return "", err
	}
	if n.kind != yamlScalar {
		return "", fmt.Errorf("apiVersion must be one value, not a mapping or a list")
	}
	return n.value, nil
}

func apiVersionError(got string) error {
	if got == "" {
		return fmt.Errorf("apiVersion is missing, so the manifest's shape is unknown; it must be %s or %s",
			ManifestAPIVersion, ManifestAPIVersionV1Alpha)
	}
	return fmt.Errorf("apiVersion %q is not a manifest version the kit knows; it must be %s or %s",
		got, ManifestAPIVersion, ManifestAPIVersionV1Alpha)
}

// Manifest is a parsed block manifest; exactly one of Block and V1Alpha is non-nil, chosen by APIVersion.
type Manifest struct {
	APIVersion string
	Block      *BlockManifest
	V1Alpha    *V1AlphaManifest
	// Warnings is in the platform's order: a spec.dependsOn given a list first, then each requirement whose optional is true. It is empty under ManifestAPIVersion.
	Warnings []ManifestWarning
}

type ManifestWarning struct {
	Path    string
	Message string
}

// ID returns metadata.id under either version, untrimmed, since every character of an id is judged.
func (m Manifest) ID() string {
	switch {
	case m.Block != nil:
		return m.Block.Metadata.ID
	case m.V1Alpha != nil:
		return m.V1Alpha.ID
	}
	return ""
}

func (m Manifest) Version() string {
	switch {
	case m.Block != nil:
		return strings.TrimSpace(m.Block.Spec.Version)
	case m.V1Alpha != nil:
		return strings.TrimSpace(m.V1Alpha.Version)
	}
	return ""
}

func (m Manifest) ReleaseClass() string {
	switch {
	case m.Block != nil:
		return strings.TrimSpace(m.Block.Spec.ReleaseClass)
	case m.V1Alpha != nil:
		return strings.TrimSpace(m.V1Alpha.ReleaseClass)
	}
	return ""
}

func (m Manifest) ReleaseNote() string {
	switch {
	case m.Block != nil:
		return strings.TrimSpace(m.Block.Spec.ReleaseNote)
	case m.V1Alpha != nil:
		return strings.TrimSpace(m.V1Alpha.ReleaseNote)
	}
	return ""
}

// BlockManifest is a block manifest under ManifestAPIVersion; a field without omitempty is required.
type BlockManifest struct {
	APIVersion string `json:"apiVersion"`
	// Type must be "block".
	Type     string           `json:"type"`
	Metadata ManifestMetadata `json:"metadata"`
	Spec     ManifestSpec     `json:"spec"`
}

type ManifestMetadata struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
}

type ManifestSpec struct {
	Version      string                `json:"version"`
	ReleaseClass string                `json:"releaseClass,omitempty"`
	ReleaseNote  string                `json:"releaseNote,omitempty"`
	Description  string                `json:"description,omitempty"`
	Params       []ManifestParam       `json:"params,omitempty"`
	Requires     []ManifestRequirement `json:"requires,omitempty"`
	Resources    []ManifestResource    `json:"resources"`
	// Provides names entries of Resources.
	Provides           []string               `json:"provides,omitempty"`
	Lifecycle          ManifestLifecycle      `json:"lifecycle"`
	Observability      *ManifestObservability `json:"observability,omitempty"`
	ReceivesAppSecrets bool                   `json:"receivesAppSecrets,omitempty"`
}

type ManifestParam struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
}

type ManifestRequirement struct {
	// Deprecated: ParseManifest leaves Kind empty; the next major release removes it.
	Kind string `json:"kind,omitempty"`
	// Deprecated: ParseManifest leaves In empty; the next major release removes it.
	In []string `json:"in,omitempty"`
	// Name is unique in the block and set when the requirement lists more than one type or the block needs two resources of one type.
	Name string `json:"name,omitempty"`
	// Many collects every offer of the types and accepts none.
	Many  bool     `json:"many,omitempty"`
	Types []string `json:"types,omitempty"`
}

func (q ManifestRequirement) Reference() string {
	if q.Name != "" {
		return q.Name
	}
	if len(q.Types) == 1 {
		return q.Types[0]
	}
	return q.Kind
}

type ManifestResource struct {
	// Name is unique in the block and stable across versions.
	Name string `json:"name"`
	// Type is empty for an entry listing more than one type.
	Type  string   `json:"type"`
	Types []string `json:"types,omitempty"`
	// Deprecated: ParseManifest refuses inputs under mitstack/v1beta; the next major release removes it.
	Inputs map[string]InputValue `json:"inputs,omitempty"`
	// Deprecated: ParseManifest refuses on under mitstack/v1beta; the next major release removes it.
	On string `json:"on,omitempty"`
}

// Deprecated: a mitstack/v1beta resource carries no inputs; the next major release removes it.
type InputValue struct {
	Scalar string
	Map    map[string]InputValue
}

type ManifestLifecycle struct {
	Endpoint               string   `json:"endpoint"`
	Operations             []string `json:"operations"`
	Contract               string   `json:"contract"`
	NeedsCredentialRefresh bool     `json:"needsCredentialRefresh,omitempty"`
}

type ManifestObservability struct {
	Metrics []ManifestMetricGroup `json:"metrics,omitempty"`
	Cost    *ManifestCost         `json:"cost,omitempty"`
}

type ManifestMetricGroup struct {
	Resource   string                 `json:"resource"`
	Namespace  string                 `json:"namespace"`
	Region     string                 `json:"region,omitempty"`
	Dimensions map[string]string      `json:"dimensions,omitempty"`
	Series     []ManifestMetricSeries `json:"series"`
}

type ManifestMetricSeries struct {
	Name   string `json:"name"`
	Metric string `json:"metric"`
	Stat   string `json:"stat"`
	Unit   string `json:"unit,omitempty"`
}

type ManifestCost struct {
	GroupBy []string `json:"groupBy,omitempty"`
}
