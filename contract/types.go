package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type State string

// The three states an Observation can carry; Unknown means the question was not settled this call, for example an async delete still converging.
const (
	Present State = "present"
	Absent  State = "absent"
	Unknown State = "unknown"
)

type InstanceConfig struct {
	Name     string            `json:"name"`
	Endpoint string            `json:"endpoint"`
	Config   map[string]string `json:"config"`
}

// CloudIdentity is the federated cloud-account identity, nil when the org has no connected cloud_connection.
type CloudIdentity struct {
	Provider         string `json:"provider"`
	RoleARN          string `json:"roleArn"`
	WebIdentityToken string `json:"webIdentityToken"`
	AccountRef       string `json:"accountRef,omitempty"`
}

type EventsCallback struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// BuildRequest is the app's newest done build request, present only on
// subscribe/upgrade for a block that provides generated-source.
type BuildRequest struct {
	ID           string          `json:"id"`
	SourceBranch string          `json:"sourceBranch"`
	SourceSha    string          `json:"sourceSha"`
	Summary      json.RawMessage `json:"summary,omitempty"`
}

// Request's JSON tags mirror the engine's request struct field for field, and a request naming no resource leaves Resource, ResourceIdentity and ResourceType empty and runs the verb on every declared resource.
type Request struct {
	Operation        string                         `json:"operation"`
	SubscriptionID   string                         `json:"subscriptionId"`
	IdempotencyKey   string                         `json:"idempotencyKey"`
	BlockID          string                         `json:"blockId"`
	BlockVersion     string                         `json:"blockVersion"`
	OrgID            string                         `json:"orgId"`
	AppID            string                         `json:"appId"`
	Params           map[string]string              `json:"params"`
	Instances        map[string]InstanceConfig      `json:"instances,omitempty"`
	Secrets          map[string]map[string]string   `json:"secrets,omitempty"`
	OBO              string                         `json:"obo,omitempty"`
	EventsCallback   *EventsCallback                `json:"eventsCallback,omitempty"`
	SecretInputs     map[string]string              `json:"secretInputs,omitempty"`
	AppSecrets       map[string]string              `json:"appSecrets,omitempty"`
	Bindings         map[string]map[string]string   `json:"bindings,omitempty"`
	BindingsAll      map[string][]map[string]string `json:"bindingsAll,omitempty"`
	CloudIdentity    *CloudIdentity                 `json:"cloudIdentity,omitempty"`
	CreatedAt        time.Time                      `json:"createdAt"`
	Nonce            string                         `json:"nonce"`
	PriorOutputs     map[string]string              `json:"priorOutputs,omitempty"`
	BuildRequest     *BuildRequest                  `json:"buildRequest,omitempty"`
	Resource         string                         `json:"resource,omitempty"`
	ResourceIdentity map[string]string              `json:"resourceIdentity,omitempty"`

	ResourceType      string                      `json:"resourceType,omitempty"`
	RecordedResources map[string]RecordedResource `json:"recordedResources,omitempty"`
	RequirementTypes  map[string]string           `json:"requirementTypes,omitempty"`
	// SettlesAt is the time of the state-changing verb a validate settles.
	SettlesAt   *time.Time `json:"settlesAt,omitempty"`
	NeverActive bool       `json:"neverActive,omitempty"`

	IssueAppID string `json:"issueAppId,omitempty"`
}

// OperationIssue asks a capability for one app's credential, beside the four lifecycle verbs.
const OperationIssue = "issue"

// Credential is what a capability issues one app: values that are secret, and when they stop working.
type Credential struct {
	Values    map[string]string `json:"values"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type RecordedResource struct {
	Type     string            `json:"type"`
	Identity map[string]string `json:"identity,omitempty"`
	State    State             `json:"state,omitempty"`
}

type ResourceRef struct {
	Name     string
	Type     string
	Identity map[string]string
	Desired  Desired
}

type CreatedResource struct {
	Name     string
	Type     string
	Identity map[string]string
}

// Response's Outputs has no omitempty because a nil map must still encode as JSON null, the shape the engine tolerates from a verb with nothing to return.
type Response struct {
	Outputs       map[string]string `json:"outputs"`
	SecretOutputs map[string]string `json:"secretOutputs,omitempty"`
	Error         string            `json:"error,omitempty"`
	Pending       bool              `json:"pending,omitempty"`
	StatusDetail  string            `json:"statusDetail,omitempty"`
	LastError     string            `json:"lastError,omitempty"`
	Absent        *bool             `json:"absent,omitempty"`
	Redrive       bool              `json:"redrive,omitempty"`
	// Resources has no entry for a resource the verb never reached.
	Resources map[string]ResourceOutcome `json:"resources,omitempty"`
	// Replaces names, on a validate, the resources an upgrade would replace rather than change in place.
	Replaces []string `json:"replaces,omitempty"`
	// ResourceSecretOutputs keeps each provided resource's secret outputs apart, since the merged SecretOutputs keeps one value of a key two resources share.
	ResourceSecretOutputs map[string]map[string]string `json:"resourceSecretOutputs,omitempty"`
	// Credential answers an issue alone.
	Credential *Credential `json:"credential,omitempty"`
}

// ResourceOutcome is what a verb's calls did to one declared resource; a nil call did not run.
type ResourceOutcome struct {
	Ensure  *CallOutcome `json:"ensure,omitempty"`
	Observe *CallOutcome `json:"observe,omitempty"`
	Delete  *CallOutcome `json:"delete,omitempty"`
}

// CallOutcome is the result of one ensure, observe or delete call; the zero value is a call that ran and succeeded with nothing to report.
type CallOutcome struct {
	Error        string            `json:"error,omitempty"`
	State        State             `json:"state,omitempty"`
	Outputs      map[string]string `json:"outputs,omitempty"`
	StatusDetail string            `json:"statusDetail,omitempty"`
	Identity     map[string]string `json:"identity,omitempty"`
	Type         string            `json:"type,omitempty"`
}

// Identity names which subscription an operation targets and where its resource lives, and carries Instances because a region-scoped block reads its region here.
type Identity struct {
	SubscriptionID string
	BlockID        string
	BlockVersion   string
	OrgID          string
	AppID          string
	Instances      map[string]InstanceConfig
	PriorOutputs   map[string]string
	CloudIdentity  *CloudIdentity
}

// Desired's Instances reach a block through the embedded Identity, so d.Instances in Ensure is the same map Delete and Observe are handed. VerbAt is the request's settlesAt when set, else its createdAt.
type Desired struct {
	Identity

	Params       map[string]string
	SecretInputs map[string]string
	AppSecrets   map[string]string
	Secrets      map[string]map[string]string
	Bindings     map[string]map[string]string
	BindingsAll  map[string][]map[string]string
	BuildRequest *BuildRequest

	Resource         string
	ResourceIdentity map[string]string
	ResourceType     string

	RecordedResources map[string]RecordedResource
	RequirementTypes  map[string]string
	VerbAt            time.Time
	NeverActive       bool
}

type Observation struct {
	State         State
	Outputs       map[string]string
	SecretOutputs map[string]string
	StatusDetail  string
	// Redrive, read beside State Unknown alone, reports from what the provider holds, never a clock, that the work the last Ensure left in flight has finished, so a validate asks the engine to re-drive subscribe.
	Redrive bool
}

type requiredField struct {
	name    string
	present bool
}

// requiredRequestFields reports a missing property by the JSON name a schema validator would use.
func requiredRequestFields(r Request) []requiredField {
	fields := []requiredField{
		{"operation", r.Operation != ""},
		{"nonce", r.Nonce != ""},
		{"createdAt", !r.CreatedAt.IsZero()},
		{"subscriptionId", r.SubscriptionID != ""},
		{"blockId", r.BlockID != ""},
		{"orgId", r.OrgID != ""},
	}

	if ci := r.CloudIdentity; ci != nil {
		fields = append(fields,
			requiredField{"cloudIdentity.provider", ci.Provider != ""},
			requiredField{"cloudIdentity.roleArn", ci.RoleARN != ""},
			requiredField{"cloudIdentity.webIdentityToken", ci.WebIdentityToken != ""},
		)
	}

	if r.Operation == OperationIssue {
		fields = append(fields, requiredField{"issueAppId", r.IssueAppID != ""})
	}

	if br := r.BuildRequest; br != nil {
		fields = append(fields,
			requiredField{"buildRequest.id", br.ID != ""},
			requiredField{"buildRequest.sourceBranch", br.SourceBranch != ""},
			requiredField{"buildRequest.sourceSha", br.SourceSha != ""},
		)
	}

	return fields
}

// envelopeTimes lets a value that is not an RFC 3339 string be reported by field name, which time.Time.UnmarshalJSON's error lacks.
type envelopeTimes struct {
	CreatedAt json.RawMessage `json:"createdAt"`
	SettlesAt json.RawMessage `json:"settlesAt"`
}

var validOperations = map[string]bool{
	"subscribe":    true,
	"unsubscribe":  true,
	"upgrade":      true,
	"validate":     true,
	OperationIssue: true,
}

// DecodeRequest decodes raw into a Request validated against envelope.schema.json, naming the JSON field in every error.
func DecodeRequest(raw []byte) (Request, error) {
	var probe envelopeTimes
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Request{}, wrapDecodeError("envelope", err)
	}
	if len(probe.CreatedAt) > 0 && !bytes.Equal(probe.CreatedAt, []byte("null")) && probe.CreatedAt[0] != '"' {
		return Request{}, fmt.Errorf("contract: envelope field %q must be an RFC 3339 string", "createdAt")
	}
	if len(probe.SettlesAt) > 0 && !bytes.Equal(probe.SettlesAt, []byte("null")) {
		var t time.Time
		if err := json.Unmarshal(probe.SettlesAt, &t); err != nil {
			return Request{}, fmt.Errorf("contract: envelope field %q must be an RFC 3339 string: %w", "settlesAt", err)
		}
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Request{}, wrapDecodeError("envelope", err)
	}

	for _, f := range requiredRequestFields(req) {
		if !f.present {
			return Request{}, fmt.Errorf("contract: envelope field %q is required", f.name)
		}
	}

	if !validOperations[req.Operation] {
		return Request{}, fmt.Errorf(
			"contract: envelope field %q must be one of subscribe, unsubscribe, upgrade, validate, issue, got %q",
			"operation", req.Operation)
	}

	if len(req.ResourceIdentity) > 0 && req.Resource == "" {
		return Request{}, fmt.Errorf("contract: envelope field %q needs a non-empty %q naming the resource the identity is for",
			"resourceIdentity", "resource")
	}
	if req.ResourceType != "" && req.Resource == "" {
		return Request{}, fmt.Errorf("contract: envelope field %q needs a non-empty %q naming the resource the type is for",
			"resourceType", "resource")
	}

	return req, nil
}

// DecodeResponse decodes raw into a Response, naming the JSON field of any type mismatch.
func DecodeResponse(raw []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, wrapDecodeError("response", err)
	}
	return resp, nil
}

// wrapDecodeError names createdAt for a *time.ParseError because DecodeRequest checks settlesAt before the full decode.
func wrapDecodeError(kind string, err error) error {
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		return fmt.Errorf("contract: %s field %q has the wrong type: %w", kind, ute.Field, err)
	}

	var perr *time.ParseError
	if kind == "envelope" && errors.As(err, &perr) {
		return fmt.Errorf("contract: %s field %q is not RFC 3339: %w", kind, "createdAt", err)
	}

	return fmt.Errorf("contract: invalid %s JSON: %w", kind, err)
}

// Identity returns the subset of r that names its subscription and where its resource lives.
func (r Request) Identity() Identity {
	return Identity{
		SubscriptionID: r.SubscriptionID,
		BlockID:        r.BlockID,
		BlockVersion:   r.BlockVersion,
		OrgID:          r.OrgID,
		AppID:          r.AppID,
		Instances:      r.Instances,
		PriorOutputs:   r.PriorOutputs,
		CloudIdentity:  r.CloudIdentity,
	}
}

// Desired returns the subset of r describing what an operator should converge its resource to.
func (r Request) Desired() Desired {
	return Desired{
		Identity:          r.Identity(),
		Params:            r.Params,
		SecretInputs:      r.SecretInputs,
		AppSecrets:        r.AppSecrets,
		Secrets:           r.Secrets,
		Bindings:          r.Bindings,
		BindingsAll:       r.BindingsAll,
		BuildRequest:      r.BuildRequest,
		Resource:          r.Resource,
		ResourceIdentity:  r.ResourceIdentity,
		ResourceType:      r.ResourceType,
		RecordedResources: r.RecordedResources,
		RequirementTypes:  r.RequirementTypes,
		VerbAt:            r.verbAt(),
		NeverActive:       r.NeverActive,
	}
}

func (r Request) verbAt() time.Time {
	if r.SettlesAt != nil {
		return *r.SettlesAt
	}
	return r.CreatedAt
}

// Response turns an Observation into a Response whose Absent stays nil for Unknown, so the engine fails open as for an operator without the marker.
func (o Observation) Response() Response {
	resp := Response{
		Outputs:       o.Outputs,
		SecretOutputs: o.SecretOutputs,
		StatusDetail:  o.StatusDetail,
	}

	switch o.State {
	case Present:
		f := false
		resp.Absent = &f
	case Absent:
		t := true
		resp.Absent = &t
	case Unknown:
	}

	return resp
}
