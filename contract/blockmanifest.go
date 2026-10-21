package contract

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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
	if apiVersion != ManifestAPIVersion {
		return Manifest{}, apiVersionError(apiVersion)
	}
	b, err := decodeBlockManifest(root)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{APIVersion: apiVersion, Block: b}, nil
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
		return fmt.Errorf("apiVersion is missing, so the manifest's shape is unknown; it must be %s", ManifestAPIVersion)
	}
	return fmt.Errorf("apiVersion %q is not a manifest version the kit knows; it must be %s", got, ManifestAPIVersion)
}

// Manifest is a parsed block manifest, whose Block ParseManifest always sets.
type Manifest struct {
	APIVersion string
	Block      *BlockManifest
}

// ID returns metadata.id untrimmed, since every character of an id is judged.
func (m Manifest) ID() string {
	if m.Block == nil {
		return ""
	}
	return m.Block.Metadata.ID
}

func (m Manifest) Version() string {
	if m.Block == nil {
		return ""
	}
	return strings.TrimSpace(m.Block.Spec.Version)
}

func (m Manifest) ReleaseClass() string {
	if m.Block == nil {
		return ""
	}
	return strings.TrimSpace(m.Block.Spec.ReleaseClass)
}

func (m Manifest) ReleaseNote() string {
	if m.Block == nil {
		return ""
	}
	return strings.TrimSpace(m.Block.Spec.ReleaseNote)
}

// The two types a ManifestAPIVersion manifest may declare: a capability serves more than one app and may issue each a credential, and a block serves one.
const (
	ManifestTypeBlock      = "block"
	ManifestTypeCapability = "capability"
)

// BlockManifest is a block manifest under ManifestAPIVersion; a field without omitempty is required.
type BlockManifest struct {
	APIVersion string `json:"apiVersion"`
	// Type is ManifestTypeBlock or ManifestTypeCapability.
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
	return ""
}

type ManifestResource struct {
	// Name is unique in the block and stable across versions.
	Name string `json:"name"`
	// Type is empty for an entry listing more than one type.
	Type  string   `json:"type"`
	Types []string `json:"types,omitempty"`
	// Deprecated: ParseManifest refuses inputs under mitstack/v1beta; a later major release removes it.
	Inputs map[string]InputValue `json:"inputs,omitempty"`
	// Deprecated: ParseManifest refuses on under mitstack/v1beta; a later major release removes it.
	On string `json:"on,omitempty"`
}

// Deprecated: a mitstack/v1beta resource carries no inputs; a later major release removes it.
type InputValue struct {
	Scalar string
	Map    map[string]InputValue
}

type ManifestLifecycle struct {
	Endpoint               string        `json:"endpoint"`
	Operations             []string      `json:"operations"`
	Contract               string        `json:"contract"`
	NeedsCredentialRefresh bool          `json:"needsCredentialRefresh,omitempty"`
	SettleBudget           time.Duration `json:"settleBudget,omitempty"`
	// VerbConnections is nil when the manifest declares none.
	VerbConnections *ManifestVerbConnections `json:"verbConnections,omitempty"`
}

// ManifestVerbConnections names, per verb, the connections whose secrets a request of that verb carries, by the names Secrets groups them under.
type ManifestVerbConnections struct {
	Validate    []string `json:"validate,omitempty"`
	Unsubscribe []string `json:"unsubscribe,omitempty"`
}

type plainLifecycle ManifestLifecycle

// lifecycleJSON writes SettleBudget in the form ParseManifest reads, since encoding/json writes a time.Duration as nanoseconds.
type lifecycleJSON struct {
	plainLifecycle
	SettleBudget string `json:"settleBudget,omitempty"`
}

func (l ManifestLifecycle) MarshalJSON() ([]byte, error) {
	out := lifecycleJSON{plainLifecycle: plainLifecycle(l)}
	if l.SettleBudget != 0 {
		out.SettleBudget = l.SettleBudget.String()
	}
	return json.Marshal(out)
}

func (l *ManifestLifecycle) UnmarshalJSON(raw []byte) error {
	var in lifecycleJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	*l = ManifestLifecycle(in.plainLifecycle)
	if in.SettleBudget == "" {
		return nil
	}
	budget, err := time.ParseDuration(in.SettleBudget)
	if err != nil {
		return fmt.Errorf("lifecycle settleBudget %q: %w", in.SettleBudget, err)
	}
	l.SettleBudget = budget
	return nil
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
