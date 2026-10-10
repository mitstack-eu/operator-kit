package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
	"github.com/mitstack-eu/operator-kit/kittest"
)

// twoResourceManifest declares network then instance, so creation order is
// network, instance and teardown order is instance, network.
const twoResourceManifest = `apiVersion: mitstack/v1beta
type: block
metadata:
  id: two-resources
  name: Two Resources
spec:
  version: 1.0.0
  resources:
    - name: network
      type: aws.vpc
    - name: instance
      type: aws.ec2-instance
  lifecycle:
    endpoint: https://two-resources.example/hook
    operations: [subscribe, unsubscribe, upgrade, validate]
    contract: kit/v1
`

// callLog records the calls made across all of a block's resources, in the
// order the Handler made them.
type callLog struct{ calls []string }

func (l *callLog) add(call string) { l.calls = append(l.calls, call) }

// recordedResource is a kittest.FakeResource that also writes each call,
// named by its resource, to a log shared by the block's resources, since
// FakeResource counts its own calls but cannot show their order across
// resources. It adds the SecretOutputs FakeResource does not report and a
// switch that panics in Ensure.
type recordedResource struct {
	*kittest.FakeResource
	name          string
	log           *callLog
	secretOutputs map[string]string
	panicOnEnsure bool
}

func (r *recordedResource) Ensure(ctx context.Context, d contract.Desired) error {
	r.log.add(r.name + " ensure")
	if r.panicOnEnsure {
		panic("ensure blew up for " + r.name)
	}
	return r.FakeResource.Ensure(ctx, d)
}

func (r *recordedResource) Observe(ctx context.Context, id contract.Identity) (contract.Observation, error) {
	r.log.add(r.name + " observe")
	obs, err := r.FakeResource.Observe(ctx, id)
	if err == nil && obs.State == contract.Present {
		obs.SecretOutputs = r.secretOutputs
	}
	return obs, err
}

func (r *recordedResource) Delete(ctx context.Context, id contract.Identity) error {
	r.log.add(r.name + " delete")
	return r.FakeResource.Delete(ctx, id)
}

// twoResources returns the network and instance resources the manifest
// declares, each converging normally, sharing one call log.
func twoResources() (network, instance *recordedResource, log *callLog) {
	log = &callLog{}
	network = &recordedResource{
		name: "network", log: log,
		FakeResource: &kittest.FakeResource{Outputs: map[string]string{"vpcId": "vpc-0abc"}},
	}
	instance = &recordedResource{
		name: "instance", log: log,
		FakeResource: &kittest.FakeResource{Outputs: map[string]string{"publicIp": "203.0.113.7"}},
	}
	return network, instance, log
}

func declared(network, instance *recordedResource) map[string]handler.DeclaredResource {
	return map[string]handler.DeclaredResource{"network": network, "instance": instance}
}

// newDeclaredHandler builds a Handler over resources under the two-resource
// manifest, with no single Resource.
func newDeclaredHandler(t *testing.T, s *kittest.Signer, resources map[string]handler.DeclaredResource) *handler.Handler {
	t.Helper()
	return newHandler(t, nil, s, func(c *handler.Config) {
		c.Manifest = []byte(twoResourceManifest)
		c.Resources = resources
	})
}

// serveDeclared serves one verb over the two resources and returns the
// status, the raw body and the decoded response.
func serveDeclared(t *testing.T, network, instance *recordedResource, operation string, extra map[string]any) (int, []byte, contract.Response) {
	t.Helper()
	s := kittest.NewSigner(t, "kid-declared", 13)
	h := newDeclaredHandler(t, s, declared(network, instance))
	fields := map[string]any{"operation": operation}
	for k, v := range extra {
		fields[k] = v
	}
	status, raw := postHook(t, h, s, envelope(t, s, "nonce-declared-"+operation, fields))
	assertConforms(t, raw)
	return status, raw, decodeResponse(t, raw)
}

func wantCalls(t *testing.T, log *callLog, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(log.calls, want) {
		t.Errorf("calls = %q, want %q", log.calls, want)
	}
}

// outcomeOf returns the entry the response carries for name, failing the
// test when there is none.
func outcomeOf(t *testing.T, resp contract.Response, name string) contract.ResourceOutcome {
	t.Helper()
	o, ok := resp.Resources[name]
	if !ok {
		t.Fatalf("response carries no entry for resource %q; resources: %+v", name, resp.Resources)
	}
	return o
}

func wantNoEntry(t *testing.T, resp contract.Response, name string) {
	t.Helper()
	if o, ok := resp.Resources[name]; ok {
		t.Errorf("response carries an entry for resource %q (%+v), but no call should have reached it", name, o)
	}
}

// wantObserved checks an entry's observe outcome carries state and outputs.
func wantObserved(t *testing.T, name string, o contract.ResourceOutcome, state contract.State, outputs map[string]string) {
	t.Helper()
	if o.Observe == nil {
		t.Errorf("resource %q has no observe outcome", name)
		return
	}
	if o.Observe.Error != "" {
		t.Errorf("resource %q observe error = %q, want none", name, o.Observe.Error)
	}
	if o.Observe.State != state {
		t.Errorf("resource %q observed %q, want %q", name, o.Observe.State, state)
	}
	if outputs != nil && !reflect.DeepEqual(o.Observe.Outputs, outputs) {
		t.Errorf("resource %q observe outputs = %v, want %v", name, o.Observe.Outputs, outputs)
	}
}

// wantCallSucceeded checks one call outcome is present and carries no error.
func wantCallSucceeded(t *testing.T, name, call string, c *contract.CallOutcome) {
	t.Helper()
	if c == nil {
		t.Errorf("resource %q has no %s outcome, but %s ran", name, call, call)
		return
	}
	if c.Error != "" {
		t.Errorf("resource %q %s error = %q, want none", name, call, c.Error)
	}
}

func TestDeclaredResources_SubscribeEnsuresEachResourceInManifestOrderAndReportsItByName(t *testing.T) {
	network, instance, log := twoResources()
	network.secretOutputs = map[string]string{"flowLogKey": "fl-key-1"}

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	wantCalls(t, log, "network ensure", "network observe", "instance ensure", "instance observe")

	if len(resp.Resources) != 2 {
		t.Errorf("response reports %d resources, want 2: %+v", len(resp.Resources), resp.Resources)
	}
	n := outcomeOf(t, resp, "network")
	wantCallSucceeded(t, "network", "ensure", n.Ensure)
	wantObserved(t, "network", n, contract.Present, map[string]string{"vpcId": "vpc-0abc"})
	if n.Delete != nil {
		t.Errorf("network reports a delete outcome %+v on subscribe", *n.Delete)
	}
	i := outcomeOf(t, resp, "instance")
	wantCallSucceeded(t, "instance", "ensure", i.Ensure)
	wantObserved(t, "instance", i, contract.Present, map[string]string{"publicIp": "203.0.113.7"})

	if resp.Pending {
		t.Error("pending = true, want false: every resource is present")
	}
	if resp.Error != "" {
		t.Errorf("error = %q, want none", resp.Error)
	}
	wantOutputs := map[string]string{"vpcId": "vpc-0abc", "publicIp": "203.0.113.7"}
	if !reflect.DeepEqual(resp.Outputs, wantOutputs) {
		t.Errorf("outputs = %v, want the union of both resources' outputs %v", resp.Outputs, wantOutputs)
	}
	if resp.SecretOutputs["flowLogKey"] != "fl-key-1" {
		t.Errorf("secretOutputs = %v, want network's flowLogKey", resp.SecretOutputs)
	}
}

func TestDeclaredResources_UpgradeEnsuresEachResourceInManifestOrder(t *testing.T) {
	network, instance, log := twoResources()
	network.Count, instance.Count = 1, 1

	status, _, resp := serveDeclared(t, network, instance, "upgrade", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	wantCalls(t, log, "network ensure", "network observe", "instance ensure", "instance observe")
	wantCallSucceeded(t, "network", "ensure", outcomeOf(t, resp, "network").Ensure)
	wantObserved(t, "instance", outcomeOf(t, resp, "instance"), contract.Present, map[string]string{"publicIp": "203.0.113.7"})
	if resp.Pending {
		t.Error("pending = true, want false: every resource is present")
	}
}

func TestDeclaredResources_ALaterResourceWinsAnOutputBothProduce(t *testing.T) {
	network, instance, _ := twoResources()
	network.Outputs = map[string]string{"endpoint": "from-network", "vpcId": "vpc-0abc"}
	instance.Outputs = map[string]string{"endpoint": "from-instance"}

	_, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	want := map[string]string{"endpoint": "from-instance", "vpcId": "vpc-0abc"}
	if !reflect.DeepEqual(resp.Outputs, want) {
		t.Errorf("outputs = %v, want %v: the resource later in creation order wins", resp.Outputs, want)
	}
}

func TestDeclaredResources_SubscribeAnswersPendingAndStopsAtAResourceNotYetPresent(t *testing.T) {
	network, instance, log := twoResources()
	network.ObserveState = stateOf(contract.Unknown)

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: pending is never an error", status)
	}
	if !resp.Pending {
		t.Error("pending = false, want true: network never became present within the budget")
	}
	wantCalls(t, log, "network ensure", "network observe", "network observe", "network observe")
	if instance.EnsureCalls != 0 {
		t.Errorf("instance ensured %d times, want 0: a resource after one still converging must wait", instance.EnsureCalls)
	}
	n := outcomeOf(t, resp, "network")
	wantCallSucceeded(t, "network", "ensure", n.Ensure)
	wantObserved(t, "network", n, contract.Unknown, nil)
	wantNoEntry(t, resp, "instance")
}

func TestDeclaredResources_SubscribeRefusalIsReportedUnderTheResourceThatRefused(t *testing.T) {
	network, instance, _ := twoResources()
	instance.EnsureErr = handler.Refuse("quota", "no capacity in %s", "eu-west-1")

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a refusal", status)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "quota" {
		t.Errorf("statusDetail %q carries refusal class %q, want %q", resp.StatusDetail, got, "quota")
	}
	n := outcomeOf(t, resp, "network")
	wantCallSucceeded(t, "network", "ensure", n.Ensure)
	wantObserved(t, "network", n, contract.Present, nil)
	i := outcomeOf(t, resp, "instance")
	if i.Ensure == nil || !strings.Contains(i.Ensure.Error, "no capacity in eu-west-1") {
		t.Errorf("instance ensure outcome = %+v, want the refusal's error", i.Ensure)
	}
	if i.Observe != nil {
		t.Errorf("instance reports an observe outcome %+v after its ensure failed", *i.Observe)
	}
}

func TestDeclaredResources_SubscribeErrorStopsBeforeTheNextResource(t *testing.T) {
	network, instance, log := twoResources()
	network.EnsureErr = errors.New("create VPC: limit exceeded")

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for an unclassified error", status)
	}
	wantCalls(t, log, "network ensure")
	n := outcomeOf(t, resp, "network")
	if n.Ensure == nil || !strings.Contains(n.Ensure.Error, "limit exceeded") {
		t.Errorf("network ensure outcome = %+v, want the error", n.Ensure)
	}
	wantNoEntry(t, resp, "instance")
}

func TestDeclaredResources_SubscribeObserveErrorIsReportedUnderItsResource(t *testing.T) {
	network, instance, _ := twoResources()
	network.ObserveErr = errors.New("describe VPC: throttled")

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	n := outcomeOf(t, resp, "network")
	if n.Observe == nil || !strings.Contains(n.Observe.Error, "throttled") {
		t.Errorf("network observe outcome = %+v, want the error", n.Observe)
	}
	if instance.EnsureCalls != 0 {
		t.Errorf("instance ensured %d times, want 0 after network failed", instance.EnsureCalls)
	}
	wantNoEntry(t, resp, "instance")
}

func TestDeclaredResources_UnsubscribeDeletesInReverseOrderAndProvesEachAbsent(t *testing.T) {
	network, instance, log := twoResources()
	network.Count, instance.Count = 1, 1

	status, _, resp := serveDeclared(t, network, instance, "unsubscribe", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	wantCalls(t, log, "instance delete", "instance observe", "network delete", "network observe")
	if resp.Absent == nil || !*resp.Absent {
		t.Errorf("absent = %v, want true: every resource was proved absent", resp.Absent)
	}
	for _, name := range []string{"instance", "network"} {
		o := outcomeOf(t, resp, name)
		wantCallSucceeded(t, name, "delete", o.Delete)
		wantObserved(t, name, o, contract.Absent, nil)
		if o.Ensure != nil {
			t.Errorf("resource %q reports an ensure outcome on unsubscribe", name)
		}
	}
}

func TestDeclaredResources_UnsubscribeStopsAtAResourceLeftStanding(t *testing.T) {
	network, instance, _ := twoResources()
	network.Count, instance.Count = 1, 1
	instance.LeakOnDelete = true

	status, _, resp := serveDeclared(t, network, instance, "unsubscribe", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if resp.Absent == nil || *resp.Absent {
		t.Errorf("absent = %v, want false: instance is still standing", resp.Absent)
	}
	if network.DeleteCalls != 0 {
		t.Errorf("network deleted %d times, want 0: a resource created earlier must outlive one still standing", network.DeleteCalls)
	}
	i := outcomeOf(t, resp, "instance")
	wantCallSucceeded(t, "instance", "delete", i.Delete)
	wantObserved(t, "instance", i, contract.Present, nil)
	wantNoEntry(t, resp, "network")
}

func TestDeclaredResources_UnsubscribeDeleteErrorIsReportedUnderItsResource(t *testing.T) {
	network, instance, _ := twoResources()
	network.Count, instance.Count = 1, 1
	instance.DeleteErr = handler.Refuse("dependents-remain", "an attached volume remains")

	status, _, resp := serveDeclared(t, network, instance, "unsubscribe", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a refusal", status)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "dependents-remain" {
		t.Errorf("refusal class = %q, want %q", got, "dependents-remain")
	}
	i := outcomeOf(t, resp, "instance")
	if i.Delete == nil || !strings.Contains(i.Delete.Error, "an attached volume remains") {
		t.Errorf("instance delete outcome = %+v, want the refusal's error", i.Delete)
	}
	if network.DeleteCalls != 0 {
		t.Errorf("network deleted %d times, want 0 after instance refused", network.DeleteCalls)
	}
}

func TestDeclaredResources_UnsubscribeCountsAResourceOfAMarkedTypeAbsentOnceItsDeleteSucceeds(t *testing.T) {
	present := contract.Present
	contribution := &kittest.FakeResource{Count: 1, ObserveState: &present}
	s := kittest.NewSigner(t, "kid-declared-marked", 13)
	h := newHandler(t, nil, s, func(c *handler.Config) {
		c.Manifest = []byte(contributionManifest(contributionType))
		c.Resources = map[string]handler.DeclaredResource{contributionType: contribution}
		c.Types = contributionSnapshot(t)
	})

	status, raw := postHook(t, h, s, envelope(t, s, "nonce-declared-marked", map[string]any{"operation": "unsubscribe"}))

	assertConforms(t, raw)
	resp := decodeResponse(t, raw)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; response %+v", status, resp)
	}
	if resp.Absent == nil || !*resp.Absent {
		t.Errorf("absent = %v, want true: the resource's type ends with its subscription; detail %q", resp.Absent, resp.StatusDetail)
	}
	if contribution.DeleteCalls != 1 || contribution.ObserveCalls != 0 {
		t.Errorf("Delete, Observe calls = %d, %d; want one Delete and no Observe", contribution.DeleteCalls, contribution.ObserveCalls)
	}
	o := outcomeOf(t, resp, contributionType)
	if o.Observe != nil || o.Delete == nil || o.Delete.State != contract.Absent || o.Delete.Type != contributionType {
		t.Errorf("resource %q outcome = %+v, want a delete of state absent and type %s with no observe", contributionType, o, contributionType)
	}
}

func TestDeclaredResources_ValidateObservesEachResourceOnce(t *testing.T) {
	cases := []struct {
		name                        string
		networkCount, instanceCount int
		wantAbsent                  *bool
		wantPending                 bool
		wantOutputs                 map[string]string
	}{
		{"all present", 1, 1, boolPtr(false), false, map[string]string{"vpcId": "vpc-0abc", "publicIp": "203.0.113.7"}},
		{"all absent", 0, 0, boolPtr(true), false, nil},
		{"one present one absent", 1, 0, nil, true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			network, instance, log := twoResources()
			network.Count, instance.Count = c.networkCount, c.instanceCount

			status, _, resp := serveDeclared(t, network, instance, "validate", nil)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			wantCalls(t, log, "network observe", "instance observe")
			if resp.Pending != c.wantPending {
				t.Errorf("pending = %v, want %v", resp.Pending, c.wantPending)
			}
			switch {
			case c.wantAbsent == nil:
				if resp.Absent != nil && *resp.Absent {
					t.Errorf("absent = true, want not absent: one resource is still present")
				}
			case resp.Absent == nil || *resp.Absent != *c.wantAbsent:
				t.Errorf("absent = %v, want %v", resp.Absent, *c.wantAbsent)
			}
			if c.wantOutputs != nil && !reflect.DeepEqual(resp.Outputs, c.wantOutputs) {
				t.Errorf("outputs = %v, want %v", resp.Outputs, c.wantOutputs)
			}
			for _, name := range []string{"network", "instance"} {
				o := outcomeOf(t, resp, name)
				if o.Observe == nil {
					t.Errorf("resource %q has no observe outcome on validate", name)
				}
				if o.Ensure != nil || o.Delete != nil {
					t.Errorf("resource %q reports an ensure or delete outcome on validate", name)
				}
			}
		})
	}
}

func TestDeclaredResources_ScrubsRequestSecretsFromEveryResourceEntry(t *testing.T) {
	network, instance, _ := twoResources()
	network.StatusDetail = "accepted token " + guardSecret
	instance.EnsureErr = errors.New("cloud API rejected " + guardSecret)

	status, raw, resp := serveDeclared(t, network, instance, "subscribe", map[string]any{"secrets": guardSecrets()})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if bytes.Contains(raw, []byte(guardSecret)) {
		t.Errorf("response carries a request secret: %s", raw)
	}
	n := outcomeOf(t, resp, "network")
	if n.Observe == nil || !strings.Contains(n.Observe.StatusDetail, "accepted token") {
		t.Errorf("network observe outcome = %+v, want its scrubbed statusDetail", n.Observe)
	}
	i := outcomeOf(t, resp, "instance")
	if i.Ensure == nil || !strings.Contains(i.Ensure.Error, "cloud API rejected") {
		t.Errorf("instance ensure outcome = %+v, want its scrubbed error", i.Ensure)
	}
}

func TestDeclaredResources_RefusesAResourceOutputCarryingASecret(t *testing.T) {
	network, instance, _ := twoResources()
	instance.Outputs = map[string]string{"token": guardSecret}

	status, raw, resp := serveDeclared(t, network, instance, "subscribe", map[string]any{"secrets": guardSecrets()})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if got := handler.RefusedClass(resp.StatusDetail); got != "secret-in-output" {
		t.Errorf("refusal class = %q, want %q", got, "secret-in-output")
	}
	if bytes.Contains(raw, []byte(guardSecret)) {
		t.Errorf("response carries a request secret: %s", raw)
	}
}

func TestDeclaredResources_ARepeatedDeliveryReplaysTheFirstAnswer(t *testing.T) {
	network, instance, _ := twoResources()
	s := kittest.NewSigner(t, "kid-declared-idem", 13)
	h := newDeclaredHandler(t, s, declared(network, instance))
	extra := map[string]any{"idempotencyKey": "idem-declared-1"}

	status1, raw1 := postHook(t, h, s, envelope(t, s, "nonce-declared-idem-1", extra))
	status2, raw2 := postHook(t, h, s, envelope(t, s, "nonce-declared-idem-2", extra))
	if status1 != http.StatusOK || status2 != status1 || !bytes.Equal(raw1, raw2) {
		t.Errorf("second delivery answered %d %s, want the first answer %d %s", status2, raw2, status1, raw1)
	}
	if network.EnsureCalls != 1 || instance.EnsureCalls != 1 {
		t.Errorf("ensure calls network=%d instance=%d, want 1 each: a replay must not reach the resources", network.EnsureCalls, instance.EnsureCalls)
	}
}

func TestDeclaredResources_APanickingResourceAnswers500(t *testing.T) {
	network, instance, _ := twoResources()
	instance.panicOnEnsure = true

	status, _, resp := serveDeclared(t, network, instance, "subscribe", nil)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if want := "handler: the block panicked during subscribe"; resp.Error != want {
		t.Errorf("error = %q, want %q", resp.Error, want)
	}
}

func declaredConfig(t *testing.T, s *kittest.Signer, resources map[string]handler.DeclaredResource) handler.Config {
	t.Helper()
	cfg := baseConfig(t, nil, s)
	cfg.Manifest = []byte(twoResourceManifest)
	cfg.Resources = resources
	return cfg
}

func TestNew_AcceptsAHandlerForEveryDeclaredResource(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	if _, err := handler.New(declaredConfig(t, s, declared(network, instance))); err != nil {
		t.Fatalf("handler.New: %v", err)
	}
}

func TestNew_RefusesResourceAndResourcesTogether(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	cfg := declaredConfig(t, s, declared(network, instance))
	cfg.Resource = &kittest.FakeResource{}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error for a Config setting both Resource and Resources, got none")
	}
}

func TestNew_RefusesResourcesWithoutAManifest(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	cfg := declaredConfig(t, s, declared(network, instance))
	cfg.Manifest = nil

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error for Resources with no manifest declaring them, got none")
	}
}

func TestNew_RefusesResourcesUnderAV1AlphaManifest(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	cfg := declaredConfig(t, s, declared(network, instance))
	cfg.Manifest = []byte(manifestWithNoHooksKey)

	_, err := handler.New(cfg)
	if err == nil {
		t.Fatal("expected an error for Resources under a mitstack/v1alpha manifest, got none")
	}
	if !strings.Contains(err.Error(), contract.ManifestAPIVersion) {
		t.Errorf("error %q does not name %s, the manifest version that declares resources", err.Error(), contract.ManifestAPIVersion)
	}
}

func TestNew_RefusesADeclaredResourceWithNoHandler(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, _, _ := twoResources()

	_, err := handler.New(declaredConfig(t, s, map[string]handler.DeclaredResource{"network": network}))
	if err == nil {
		t.Fatal("expected an error for a declared resource with no handler, got none")
	}
	if !strings.Contains(err.Error(), `"instance"`) {
		t.Errorf("error %q does not name the resource with no handler, %q", err.Error(), "instance")
	}
}

func TestNew_RefusesAHandlerForAnUndeclaredResource(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	resources := declared(network, instance)
	resources["cache"] = &kittest.FakeResource{}

	_, err := handler.New(declaredConfig(t, s, resources))
	if err == nil {
		t.Fatal("expected an error for a handler under a name the manifest does not declare, got none")
	}
	if !strings.Contains(err.Error(), `"cache"`) {
		t.Errorf("error %q does not name the undeclared resource %q", err.Error(), "cache")
	}
}

func TestNew_RefusesHooksTogetherWithResources(t *testing.T) {
	s := kittest.NewSigner(t, "kid-1", 7)
	network, instance, _ := twoResources()
	cfg := declaredConfig(t, s, declared(network, instance))
	cfg.Hooks = map[string]handler.Hook{handler.HookBeforeEnsure: noopHook}

	if _, err := handler.New(cfg); err == nil {
		t.Fatal("expected an error for Hooks set together with Resources, got none")
	}
}
