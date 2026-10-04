package handler_test

// dropped_resource_test.go pins how the Handler serves a request naming a
// resource the manifest no longer declares: unsubscribe and validate run on
// the handler Config.Types registers for the resource's recorded type, sent
// under "resourceType", and every other request naming it is refused with a
// 400 undeclared-resource naming the resource and the type.

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// droppedType is the recorded type of firewall, a resource
// twoResourceManifest does not declare.
const droppedType = "aws.security-group"

// droppedIdentity is the identity firewall's ensure stored while the
// manifest still declared it.
var droppedIdentity = map[string]string{"groupId": "sg-0abc"}

// droppedBlock is the two-resource block, plus a handler for droppedType
// that reaches what firewall's ensure created through the request alone.
type droppedBlock struct {
	network, instance *recordedResource
	firewall          *requestResource
	log               *callLog
}

// newDroppedBlock returns the block with firewall standing when standing is
// set.
func newDroppedBlock(standing bool) *droppedBlock {
	network, instance, log := twoResources()
	fw := &recordedResource{
		name: "firewall", log: log,
		FakeResource: &kittest.FakeResource{Outputs: map[string]string{"groupId": "sg-0abc"}},
	}
	if standing {
		fw.Count = 1
	}
	return &droppedBlock{network: network, instance: instance, firewall: &requestResource{recordedResource: fw}, log: log}
}

// serveDropped serves one verb over the block, with types as its
// Config.Types, and returns the status, the raw body and the decoded
// response.
func serveDropped(t *testing.T, b *droppedBlock, types map[string]handler.DeclaredResource, operation string, extra map[string]any) (int, []byte, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-dropped", 29)
	h := newHandler(t, nil, s, func(c *handler.Config) {
		c.Manifest = []byte(twoResourceManifest)
		c.Resources = declared(b.network, b.instance)
		c.Types = types
	})
	fields := map[string]any{"operation": operation}
	for k, v := range extra {
		fields[k] = v
	}
	status, raw := postHook(t, h, s, envelope(t, s, "nonce-dropped-"+operation, fields))
	assertConforms(t, raw)
	return status, raw, decodeResponse(t, raw)
}

func (b *droppedBlock) types() map[string]handler.DeclaredResource {
	return map[string]handler.DeclaredResource{droppedType: b.firewall}
}

// namingDropped is the request body naming firewall with the given recorded
// type, or with none when resourceType is empty.
func namingDropped(resourceType string) map[string]any {
	body := map[string]any{
		"resource":         "firewall",
		"resourceIdentity": map[string]any{"groupId": "sg-0abc"},
	}
	if resourceType != "" {
		body["resourceType"] = resourceType
	}
	return body
}

// wantHandedDropped checks d is the verb's Desired naming firewall, with its
// recorded type and stored identity.
func wantHandedDropped(t *testing.T, call string, d contract.Desired) {
	t.Helper()
	if d.Resource != "firewall" {
		t.Errorf("%s was handed Resource %q, want %q", call, d.Resource, "firewall")
	}
	if d.ResourceType != droppedType {
		t.Errorf("%s was handed ResourceType %q, want %q", call, d.ResourceType, droppedType)
	}
	if !reflect.DeepEqual(d.ResourceIdentity, droppedIdentity) {
		t.Errorf("%s was handed ResourceIdentity %v, want the stored identity %v", call, d.ResourceIdentity, droppedIdentity)
	}
}

// wantRefusedUndeclared checks a 400 undeclared-resource whose error quotes
// each of names, and that no resource or type handler was reached.
func wantRefusedUndeclared(t *testing.T, b *droppedBlock, status int, raw []byte, resp contract.Response, names ...string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", status, raw)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "undeclared-resource" {
		t.Errorf("refusal class = %q, want %q; statusDetail %q", got, "undeclared-resource", resp.StatusDetail)
	}
	for _, name := range names {
		if !strings.Contains(resp.Error, name) {
			t.Errorf("error = %q, want it to name %s", resp.Error, name)
		}
	}
	if len(b.log.calls) != 0 {
		t.Errorf("calls = %q, want none: a refused request reaches no resource", b.log.calls)
	}
	if len(resp.Resources) != 0 {
		t.Errorf("response reports resources %+v, want none: no resource was reached", resp.Resources)
	}
}

func TestDroppedResource_AnUnsubscribeNamingItWithItsRecordedTypeDeletesItThroughThatTypesHandlerAndProvesItAbsent(t *testing.T) {
	b := newDroppedBlock(true)

	status, raw, resp := serveDropped(t, b, b.types(), "unsubscribe", namingDropped(droppedType))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", status, raw)
	}
	if resp.Absent == nil || !*resp.Absent {
		t.Errorf("absent = %v, want true: firewall was deleted and observed absent; body %s", resp.Absent, raw)
	}
	wantCalls(t, b.log, "firewall delete with request", "firewall observe with request")
	wantUntouched(t, b.network, b.instance)
	wantOnlyEntries(t, resp, "firewall")

	f := outcomeOf(t, resp, "firewall")
	wantCallSucceeded(t, "firewall", "delete", f.Delete)
	wantObserved(t, "firewall", f, contract.Absent, nil)
	if f.Ensure != nil {
		t.Errorf("firewall reports an ensure outcome %+v on unsubscribe", *f.Ensure)
	}

	if len(b.firewall.deleted) != 1 {
		t.Fatalf("the type handler's DeleteWithRequest ran %d times, want 1", len(b.firewall.deleted))
	}
	wantHandedDropped(t, "DeleteWithRequest", b.firewall.deleted[0])
	if b.firewall.Count != 0 {
		t.Errorf("firewall count = %d, want 0: its type handler deleted it", b.firewall.Count)
	}
}

func TestDroppedResource_AValidateNamingItWithItsRecordedTypeAnswersTheStateThatTypesHandlerObserves(t *testing.T) {
	for _, tc := range []struct {
		name       string
		standing   bool
		wantAbsent bool
		wantState  contract.State
	}{
		{name: "present", standing: true, wantAbsent: false, wantState: contract.Present},
		{name: "absent", standing: false, wantAbsent: true, wantState: contract.Absent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newDroppedBlock(tc.standing)

			status, raw, resp := serveDropped(t, b, b.types(), "validate", namingDropped(droppedType))
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", status, raw)
			}
			if resp.Absent == nil || *resp.Absent != tc.wantAbsent {
				t.Errorf("absent = %v, want %v: the type handler observes firewall %s; body %s", resp.Absent, tc.wantAbsent, tc.wantState, raw)
			}
			if resp.Pending {
				t.Errorf("pending = true, want false: firewall is %s", tc.wantState)
			}
			wantCalls(t, b.log, "firewall observe with request")
			wantUntouched(t, b.network, b.instance)
			wantOnlyEntries(t, resp, "firewall")
			wantObserved(t, "firewall", outcomeOf(t, resp, "firewall"), tc.wantState, nil)

			if len(b.firewall.observed) != 1 {
				t.Fatalf("the type handler's ObserveWithRequest ran %d times, want 1", len(b.firewall.observed))
			}
			wantHandedDropped(t, "ObserveWithRequest", b.firewall.observed[0])
		})
	}
}

func TestDroppedResource_ASubscribeOrUpgradeNamingItWithAHandledTypeIs400UndeclaredResourceAndReachesNoHandler(t *testing.T) {
	for _, operation := range []string{"subscribe", "upgrade"} {
		t.Run(operation, func(t *testing.T) {
			b := newDroppedBlock(true)

			status, raw, resp := serveDropped(t, b, b.types(), operation, namingDropped(droppedType))
			wantRefusedUndeclared(t, b, status, raw, resp, `"firewall"`, `"`+droppedType+`"`)
			if b.firewall.EnsureCalls+b.firewall.ObserveCalls+b.firewall.DeleteCalls != 0 || len(b.firewall.observed)+len(b.firewall.deleted) != 0 {
				t.Errorf("the type handler was reached on %s, want it called by neither subscribe nor upgrade", operation)
			}
		})
	}
}

func TestDroppedResource_AnUnsubscribeOrValidateNamingItIs400UndeclaredResourceUnlessTheBlockHandlesItsRecordedType(t *testing.T) {
	for _, operation := range []string{"unsubscribe", "validate"} {
		for _, tc := range []struct {
			name         string
			resourceType string
			wantNamed    []string
		}{
			{name: "no resourceType", resourceType: "", wantNamed: []string{`"firewall"`, "resourceType"}},
			{name: "a catalogue type Config.Types does not hold", resourceType: "aws.ec2-instance", wantNamed: []string{`"firewall"`, `"aws.ec2-instance"`}},
			{name: "a type the catalogue does not know", resourceType: "aws.not-a-type", wantNamed: []string{`"firewall"`, `"aws.not-a-type"`}},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				b := newDroppedBlock(true)

				status, raw, resp := serveDropped(t, b, b.types(), operation, namingDropped(tc.resourceType))
				wantRefusedUndeclared(t, b, status, raw, resp, tc.wantNamed...)
				if b.firewall.Count != 1 {
					t.Errorf("firewall count = %d, want 1: a refused request deletes nothing", b.firewall.Count)
				}
			})
		}
	}
}

func TestNew_RefusesATypesKeyThatIsNotAResourceTypeIdInTheCatalogueQuotingIt(t *testing.T) {
	b := newDroppedBlock(false)
	s := kittest.NewSigner(t, "kid-dropped-new", 31)
	cfg := baseConfig(t, nil, s)
	cfg.Manifest = []byte(twoResourceManifest)
	cfg.Resources = declared(b.network, b.instance)
	cfg.Types = map[string]handler.DeclaredResource{
		droppedType:      b.firewall,
		"aws.not-a-type": b.firewall,
	}

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("handler.New accepted a Config.Types key that is not a resource-type id, want it refused")
	}
	if !strings.Contains(err.Error(), `"aws.not-a-type"`) {
		t.Errorf("error = %q, want it to quote the key %q", err.Error(), "aws.not-a-type")
	}
	if strings.Contains(err.Error(), `"`+droppedType+`"`) {
		t.Errorf("error = %q quotes %q, a key the catalogue knows", err.Error(), droppedType)
	}
}
