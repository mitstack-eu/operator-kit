package observe_test

// The aws.ec2-instance observer reads the identity's instance through the EC2 API on every call, never from what an ensure remembered.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/observe"
)

var (
	_ handler.Observer = observe.EC2Instance{}
	_ handler.Observer = observe.EgressProxy{}
)

// fakeEC2 answers DescribeInstance from the instances it holds and records
// each instance id and subscription it was asked about.
type fakeEC2 struct {
	mu        sync.Mutex
	instances map[string]observe.EC2InstanceDescription
	asked     []string
	subs      []string
	err       error
}

func (f *fakeEC2) DescribeInstance(ctx context.Context, d contract.Desired, instanceID string) (observe.EC2InstanceDescription, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, instanceID)
	f.subs = append(f.subs, d.SubscriptionID)
	if f.err != nil {
		return observe.EC2InstanceDescription{}, false, f.err
	}
	desc, ok := f.instances[instanceID]
	return desc, ok, nil
}

func (f *fakeEC2) lose(instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.instances, instanceID)
}

func refFor(typ string, identity map[string]string) contract.ResourceRef {
	return contract.ResourceRef{
		Name:     typ,
		Type:     typ,
		Identity: identity,
		Desired:  contract.Desired{Identity: contract.Identity{SubscriptionID: "sub-1", OrgID: "org-1"}},
	}
}

// wantObservation checks state, that every output in outputs is reported
// with its value, and that detail, when set, is in the status detail.
func wantObservation(t *testing.T, got contract.Observation, err error, state contract.State, outputs map[string]string, detail string) {
	t.Helper()
	if err != nil {
		t.Fatalf("ObserveResource: %v", err)
	}
	if got.State != state {
		t.Errorf("state = %q, want %q (detail %q)", got.State, state, got.StatusDetail)
	}
	for k, v := range outputs {
		if got.Outputs[k] != v {
			t.Errorf("output %q = %q, want %q; outputs %v", k, got.Outputs[k], v, got.Outputs)
		}
	}
	if detail != "" && !strings.Contains(got.StatusDetail, detail) {
		t.Errorf("statusDetail = %q, want it to name %q", got.StatusDetail, detail)
	}
}

func instance(state string) observe.EC2InstanceDescription {
	return observe.EC2InstanceDescription{
		InstanceID: "i-0abc", State: state, PublicIP: "203.0.113.7", Region: "eu-central-1",
		Tags: map[string]string{observe.AWSSubscriptionTag: "sub-1"},
	}
}

func TestEC2Instance_ObservesTheReportedInstanceThroughTheAPI(t *testing.T) {
	cases := []struct {
		label   string
		held    map[string]observe.EC2InstanceDescription
		state   contract.State
		outputs map[string]string
		detail  string
	}{
		{"running", map[string]observe.EC2InstanceDescription{"i-0abc": instance("running")}, contract.Present,
			map[string]string{"hostId": "i-0abc", "publicIp": "203.0.113.7", "cloud.region": "eu-central-1", "execKind": "ssm", "execTarget": "i-0abc"}, ""},
		{"terminated", map[string]observe.EC2InstanceDescription{"i-0abc": instance("terminated")}, contract.Absent, nil, ""},
		{"not found", nil, contract.Absent, nil, ""},
		{"stopped", map[string]observe.EC2InstanceDescription{"i-0abc": instance("stopped")}, contract.Unknown, nil, "stopped"},
		{"pending", map[string]observe.EC2InstanceDescription{"i-0abc": instance("pending")}, contract.Unknown, nil, "pending"},
		{"shutting-down", map[string]observe.EC2InstanceDescription{"i-0abc": instance("shutting-down")}, contract.Unknown, nil, "shutting-down"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			api := &fakeEC2{instances: tc.held}
			got, err := observe.EC2Instance{API: api}.ObserveResource(context.Background(),
				refFor("aws.ec2-instance", map[string]string{"instanceId": "i-0abc"}))
			wantObservation(t, got, err, tc.state, tc.outputs, tc.detail)
			if len(api.asked) != 1 || api.asked[0] != "i-0abc" || api.subs[0] != "sub-1" {
				t.Errorf("asked about %q for %q, want i-0abc once for sub-1", api.asked, api.subs)
			}
		})
	}
}

func TestEC2Instance_AnIdentityWithNoInstanceIDIsUnknownAndAsksNothing(t *testing.T) {
	api := &fakeEC2{instances: map[string]observe.EC2InstanceDescription{"i-0abc": instance("running")}}
	got, err := observe.EC2Instance{API: api}.ObserveResource(context.Background(), refFor("aws.ec2-instance", map[string]string{"hostname": "app"}))
	wantObservation(t, got, err, contract.Unknown, nil, "instanceId")
	if len(api.asked) != 0 {
		t.Errorf("the API was asked about %q, want no call without an instance id", api.asked)
	}
}

// Observe asks the provider each time: once the provider has lost the
// instance, the next observe finds it absent.
func TestEC2Instance_AsksTheAPIOnEveryObserve(t *testing.T) {
	api := &fakeEC2{instances: map[string]observe.EC2InstanceDescription{"i-0abc": instance("running")}}
	o := observe.EC2Instance{API: api}
	ref := refFor("aws.ec2-instance", map[string]string{"instanceId": "i-0abc"})

	first, err := o.ObserveResource(context.Background(), ref)
	wantObservation(t, first, err, contract.Present, nil, "")
	api.lose("i-0abc")
	second, err := o.ObserveResource(context.Background(), ref)
	wantObservation(t, second, err, contract.Absent, nil, "")
	if len(api.asked) != 2 {
		t.Errorf("the API was asked %d times, want once per observe", len(api.asked))
	}
}

func TestEC2Instance_ReturnsTheAPIsError(t *testing.T) {
	boom := errors.New("ec2: throttled")
	_, err := observe.EC2Instance{API: &fakeEC2{err: boom}}.ObserveResource(context.Background(),
		refFor("aws.ec2-instance", map[string]string{"instanceId": "i-0abc"}))
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the API's error", err)
	}
}

// aws.egress-proxy is an instance of its own, read through the same API by
// the instance id its identity names; it reports no outputs.
func TestEgressProxy_ObservesItsInstanceThroughTheEC2API(t *testing.T) {
	cases := []struct {
		label string
		held  map[string]observe.EC2InstanceDescription
		state contract.State
	}{
		{"running", map[string]observe.EC2InstanceDescription{"i-0abc": instance("running")}, contract.Present},
		{"terminated", map[string]observe.EC2InstanceDescription{"i-0abc": instance("terminated")}, contract.Absent},
		{"not found", nil, contract.Absent},
		{"stopped", map[string]observe.EC2InstanceDescription{"i-0abc": instance("stopped")}, contract.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got, err := observe.EgressProxy{API: &fakeEC2{instances: tc.held}}.ObserveResource(context.Background(),
				refFor("aws.egress-proxy", map[string]string{"instanceId": "i-0abc"}))
			wantObservation(t, got, err, tc.state, nil, "")
		})
	}
}
