package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// State names what an operator observed about a resource on a validate or
// unsubscribe call: whether it is still there, confirmed gone, or the
// operator cannot yet tell.
type State string

// The three states an Observation can carry. Present and Absent are
// definite; Unknown means the operator could not settle the question this
// call — for example an async delete still converging.
const (
	Present State = "present"
	Absent  State = "absent"
	Unknown State = "unknown"
)

// InstanceConfig is a resolved tool instance the engine hands to a block,
// keyed by capability in Request.Instances.
type InstanceConfig struct {
	Name     string            `json:"name"`
	Endpoint string            `json:"endpoint"`
	Config   map[string]string `json:"config"`
}

// CloudIdentity is the federated cloud-account identity the engine attaches
// to a request when the org has a connected cloud_connection. It is nil
// when no connected connection exists.
type CloudIdentity struct {
	Provider         string `json:"provider"`
	RoleARN          string `json:"roleArn"`
	WebIdentityToken string `json:"webIdentityToken"`
	AccountRef       string `json:"accountRef,omitempty"`
}

// EventsCallback tells a block where and how to report progress events back
// to the engine.
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

// Request is the typed form of a lifecycle webhook envelope, decoded from
// the wire shape envelope.schema.json pins. Its JSON tags mirror the
// engine's own request struct field for field. Resource names the one
// declared resource the verb runs on, ResourceInputs carries that
// resource's inputs, each a scalar or a mapping kept as raw JSON, and
// ResourceIdentity carries the identity that resource's ensure stored; a
// request naming no resource leaves all three empty and runs the verb on
// every declared resource.
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
	ResourceInputs   map[string]json.RawMessage     `json:"resourceInputs,omitempty"`
	ResourceIdentity map[string]string              `json:"resourceIdentity,omitempty"`
}

// Response is the typed form of a lifecycle webhook response, mirroring the
// wire shape response.schema.json pins. Outputs has no omitempty: a Go nil
// map must still encode as JSON null, the shape the engine tolerates from a
// verb with nothing to return. Resources is omitted when nil, so a response
// that reports no resource encodes with no resources key.
type Response struct {
	Outputs       map[string]string `json:"outputs"`
	SecretOutputs map[string]string `json:"secretOutputs,omitempty"`
	Error         string            `json:"error,omitempty"`
	Pending       bool              `json:"pending,omitempty"`
	StatusDetail  string            `json:"statusDetail,omitempty"`
	LastError     string            `json:"lastError,omitempty"`
	Absent        *bool             `json:"absent,omitempty"`
	// Resources reports, under each manifest resource name, the calls the
	// verb ran for that resource. A resource the verb never reached has no
	// entry.
	Resources map[string]ResourceOutcome `json:"resources,omitempty"`
}

// ResourceOutcome is what a verb's calls did to one declared resource. A
// non-nil call means that call ran during the verb; a nil one means it did
// not.
type ResourceOutcome struct {
	Ensure  *CallOutcome `json:"ensure,omitempty"`
	Observe *CallOutcome `json:"observe,omitempty"`
	Delete  *CallOutcome `json:"delete,omitempty"`
}

// CallOutcome is the result of one ensure, observe or delete call on a
// resource. Error is set when the call failed. State and Outputs are what
// an observe found, and on an ensure Outputs are the outputs it published.
// Identity is what an ensure stored as its resource's identity.
// StatusDetail is the human-readable detail the call left. The zero value
// is a call that ran and succeeded with nothing to report.
type CallOutcome struct {
	Error        string            `json:"error,omitempty"`
	State        State             `json:"state,omitempty"`
	Outputs      map[string]string `json:"outputs,omitempty"`
	StatusDetail string            `json:"statusDetail,omitempty"`
	Identity     map[string]string `json:"identity,omitempty"`
}

// Ensured is what an ensure reports about the resource it converged:
// Identity is what it stored as the resource's identity, the values that
// address the resource where it lives, and Outputs are the outputs it
// published.
type Ensured struct {
	Identity map[string]string
	Outputs  map[string]string
}

// Identity is the subset of a Request that names which subscription an
// operation targets and where its resource lives, independent of what it
// should converge to. Instances carries the resolved tool instances,
// because a teardown or an observation has to address the resource where it
// was provisioned — a region-scoped block reads that region here, on the
// only value Delete and Observe are handed. It is nil when the request
// carries no instances, which reads as an unset config rather than
// panicking.
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

// Desired is the subset of a Request describing what an operator should
// converge a subscription's resource to. It embeds Identity, since knowing
// what is desired is meaningless without knowing which subscription it is
// desired for and where its resource lives, and adds the params, secrets
// and bindings that describe the target state. The instances reach a block
// through the embedded Identity, so d.Instances inside Ensure resolves to
// the same map Delete and Observe are handed. Resource, ResourceInputs and
// ResourceIdentity are the request's own: the declared resource it names,
// that resource's inputs and the identity that resource's ensure stored,
// all empty when the request names no resource.
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
	ResourceInputs   map[string]json.RawMessage
	ResourceIdentity map[string]string
	ResourceType     string
}

// Observation is what an operator's validate/unsubscribe check found about
// a resource, in the shape Response() turns into the typed wire Response.
type Observation struct {
	State         State
	Outputs       map[string]string
	SecretOutputs map[string]string
	StatusDetail  string
}

// requiredField pairs one required JSON property name with whether the
// decoded Request carries it.
type requiredField struct {
	name    string
	present bool
}

// requiredRequestFields lists the envelope.schema.json required properties
// in schema order, each paired with the Request field that carries it.
// DecodeRequest walks this list so a missing property is reported by the
// same JSON name a schema validator would use. When CloudIdentity or
// BuildRequest is present, their own required properties join the walk
// under the nested "cloudIdentity."/"buildRequest." names the schema uses.
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

	if br := r.BuildRequest; br != nil {
		fields = append(fields,
			requiredField{"buildRequest.id", br.ID != ""},
			requiredField{"buildRequest.sourceBranch", br.SourceBranch != ""},
			requiredField{"buildRequest.sourceSha", br.SourceSha != ""},
		)
	}

	return fields
}

// envelopeCreatedAt probes the raw envelope for its createdAt value ahead of
// the full Request unmarshal, so a non-string JSON value (number, bool,
// object, array) is reported by name instead of surfacing as
// time.Time.UnmarshalJSON's bare "input is not a JSON string" error, which
// carries no field name.
type envelopeCreatedAt struct {
	CreatedAt json.RawMessage `json:"createdAt"`
}

// validOperations is the envelope.schema.json operation enum.
var validOperations = map[string]bool{
	"subscribe":   true,
	"unsubscribe": true,
	"upgrade":     true,
	"validate":    true,
}

// DecodeRequest decodes raw into a Request and validates it against
// envelope.schema.json's required set and operation enum, including the
// nested required sets cloudIdentity and buildRequest carry when present,
// and refuses resource inputs or a resource identity that name no resource
// to bind them to. Every error names the JSON field it concerns, so an
// operator author can act on it without reading the schema.
func DecodeRequest(raw []byte) (Request, error) {
	var probe envelopeCreatedAt
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Request{}, wrapDecodeError("envelope", err)
	}
	if len(probe.CreatedAt) > 0 && !bytes.Equal(probe.CreatedAt, []byte("null")) && probe.CreatedAt[0] != '"' {
		return Request{}, fmt.Errorf("contract: envelope field %q must be an RFC 3339 string", "createdAt")
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
			"contract: envelope field %q must be one of subscribe, unsubscribe, upgrade, validate, got %q",
			"operation", req.Operation)
	}

	if len(req.ResourceInputs) > 0 && req.Resource == "" {
		return Request{}, fmt.Errorf("contract: envelope field %q needs a non-empty %q naming the resource the inputs are for",
			"resourceInputs", "resource")
	}
	if len(req.ResourceIdentity) > 0 && req.Resource == "" {
		return Request{}, fmt.Errorf("contract: envelope field %q needs a non-empty %q naming the resource the identity is for",
			"resourceIdentity", "resource")
	}

	return req, nil
}

// DecodeResponse decodes raw into a Response. A type mismatch on any field
// is reported naming the JSON field it concerns, exactly as DecodeRequest
// does for the envelope side.
func DecodeResponse(raw []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, wrapDecodeError("response", err)
	}
	return resp, nil
}

// wrapDecodeError turns a json.Unmarshal error into one naming the JSON
// field it concerns. A *json.UnmarshalTypeError already carries the field
// as a JSON path (e.g. "outputs" or "instances.config"), so its Field is
// used directly. A *time.ParseError carries no field of its own; Request is
// the only decode target with a time.Time field (CreatedAt), so a
// *time.ParseError surfacing from an "envelope" decode can only concern
// "createdAt" — a second time.Time field added to Request would need this
// function changed too.
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

// Identity returns the subset of r that names which subscription it
// targets and where that subscription's resource lives.
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

// Desired returns the subset of r describing what an operator should
// converge its resource to, with the resource it names, that resource's
// inputs and the identity its ensure stored. The instances arrive through
// the embedded Identity, which is why no field here repeats them.
func (r Request) Desired() Desired {
	return Desired{
		Identity:         r.Identity(),
		Params:           r.Params,
		SecretInputs:     r.SecretInputs,
		AppSecrets:       r.AppSecrets,
		Secrets:          r.Secrets,
		Bindings:         r.Bindings,
		BindingsAll:      r.BindingsAll,
		BuildRequest:     r.BuildRequest,
		Resource:         r.Resource,
		ResourceInputs:   r.ResourceInputs,
		ResourceIdentity: r.ResourceIdentity,
	}
}

// Response turns an Observation into the typed wire Response an operator's
// webhook handler returns. State controls the Absent pointer: Present sets
// it to false, Absent sets it to true, and Unknown leaves it nil so the
// engine fails open exactly as an operator that never implements the
// marker does. Pending is always false — Response() reports what was
// observed, not that a resource is still converging.
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
		// resp.Absent stays nil.
	}

	return resp
}
