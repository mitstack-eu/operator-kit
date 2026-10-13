package observe_test

// A report counts only when its madeAt, else its receivedAt, is after the verb, and an object is absent only when a report made after the verb marks its list complete.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/observe"
)

var _ handler.Observer = observe.NodeReported{}

type fakeReports struct {
	reports []observe.NodeReport
	bySub   map[string][]observe.NodeReport
	asked   []string
	err     error
}

func (f *fakeReports) NodeReports(_ context.Context, _ contract.Desired, subscriptionID string) ([]observe.NodeReport, error) {
	f.asked = append(f.asked, subscriptionID)
	if f.bySub != nil {
		return f.bySub[subscriptionID], f.err
	}
	return f.reports, f.err
}

var verbAt = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// nodeReport describes one report: when the platform received it, when the
// node made it (unset leaves madeAt out), whether it says its list is
// complete (nil leaves the key out), and the list itself.
type nodeReport struct {
	received time.Duration
	made     *time.Duration
	complete *bool
	items    any
}

func at(d time.Duration) *time.Duration { return &d }
func flag(b bool) *bool                 { return &b }

func (r nodeReport) build(t *testing.T) observe.NodeReport {
	t.Helper()
	body := map[string]any{"subscriptionResources": r.items}
	if r.made != nil {
		body["madeAt"] = verbAt.Add(*r.made).Format(time.RFC3339Nano)
	}
	if r.complete != nil {
		body["subscriptionResourcesComplete"] = *r.complete
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return observe.NodeReport{ReceivedAt: verbAt.Add(r.received), Body: raw}
}

var (
	appNamespace = map[string]string{"namespace": "", "kind": "Namespace", "name": "app-alpha", "subscription": "sub-1"}
	otherSubs    = map[string]string{"namespace": "", "kind": "Namespace", "name": "app-alpha", "subscription": "sub-2"}
	otherObject  = map[string]string{"namespace": "app-alpha", "kind": "ServiceAccount", "name": "app-alpha-runtime", "subscription": "sub-1"}
	readFailed   = map[string]any{"exitStatus": 1, "stderr": "error: You must be logged in to the server (Unauthorized)"}
)

func list(items ...map[string]string) []map[string]string {
	return append([]map[string]string{}, items...)
}

func namespaceRef() contract.ResourceRef {
	r := refFor("kubernetes.namespace", map[string]string{"kind": "Namespace", "name": "app-alpha"})
	r.Desired.VerbAt = verbAt
	return r
}

func TestNodeReported_ClaimsAbsenceOnlyFromACompleteReportMadeAfterTheVerb(t *testing.T) {
	cases := []struct {
		label   string
		reports []nodeReport
		want    contract.State
		detail  string
	}{
		{"a report received after the verb listing it",
			[]nodeReport{{received: time.Minute, items: list(otherObject, appNamespace)}}, contract.Present, ""},
		{"a complete report made after the verb not listing it",
			[]nodeReport{{received: time.Minute, made: at(30 * time.Second), complete: flag(true), items: list(otherObject)}}, contract.Absent, ""},
		{"a complete report made after the verb listing it for another subscription",
			[]nodeReport{{received: time.Minute, made: at(30 * time.Second), complete: flag(true), items: list(otherSubs)}}, contract.Absent, ""},
		{"a report with no made-at time not listing it",
			[]nodeReport{{received: time.Minute, complete: flag(true), items: list(otherObject)}}, contract.Unknown, "absence"},
		{"a report made after the verb that may be truncated",
			[]nodeReport{{received: time.Minute, made: at(30 * time.Second), items: list(otherObject)}}, contract.Unknown, "absence"},
		{"a report made after the verb marked incomplete",
			[]nodeReport{{received: time.Minute, made: at(30 * time.Second), complete: flag(false), items: list(otherObject)}}, contract.Unknown, "absence"},
		{"a report the node could not read the cluster for",
			[]nodeReport{{received: time.Minute, made: at(30 * time.Second), complete: flag(true), items: readFailed}}, contract.Unknown, ""},
		{"a report made before the verb, received after it, listing it",
			[]nodeReport{{received: time.Minute, made: at(-30 * time.Second), complete: flag(true), items: list(appNamespace)}}, contract.Unknown, ""},
		{"a report received before the verb listing it",
			[]nodeReport{{received: -time.Minute, items: list(appNamespace)}}, contract.Unknown, ""},
		{"a report received at the verb",
			[]nodeReport{{received: 0, items: list()}}, contract.Unknown, ""},
		{"no report", nil, contract.Unknown, ""},
		{"the newest of several proves it gone",
			[]nodeReport{
				{received: 2 * time.Minute, made: at(time.Minute), complete: flag(true), items: list(appNamespace)},
				{received: 2 * time.Minute, made: at(90 * time.Second), complete: flag(true), items: list()},
			}, contract.Absent, ""},
		{"the newest of several lists it",
			[]nodeReport{
				{received: 3 * time.Minute, made: at(2 * time.Minute), complete: flag(true), items: list(appNamespace)},
				{received: 3 * time.Minute, made: at(time.Minute), complete: flag(true), items: list()},
			}, contract.Present, ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			reports := make([]observe.NodeReport, 0, len(tc.reports))
			for _, r := range tc.reports {
				reports = append(reports, r.build(t))
			}
			api := &fakeReports{reports: reports}
			got, err := observe.NodeReported{API: api}.ObserveResource(context.Background(), namespaceRef())
			wantObservation(t, got, err, tc.want, nil, tc.detail)
			if len(api.asked) != 1 || api.asked[0] != "sub-1" {
				t.Errorf("asked for the reports of %q, want sub-1 once", api.asked)
			}
		})
	}
}

// Node reports are stored under the cluster's own subscription, whose nodes
// send them, while a namespace carries the label of the subscription that
// created it, here sub-1.
func TestNodeReported_ReadsTheReportsOfTheClusterSubscriptionTheIdentityNames(t *testing.T) {
	complete := func(items ...map[string]any) nodeReport {
		return nodeReport{received: time.Minute, made: at(30 * time.Second), complete: flag(true), items: append([]map[string]any{}, items...)}
	}
	nullNamespace := map[string]any{"namespace": nil, "kind": "Namespace", "name": "app-alpha", "subscription": "sub-1"}
	noNamespace := map[string]any{"kind": "Namespace", "name": "app-alpha", "subscription": "sub-1"}
	clusterOwn := map[string]any{"kind": "Namespace", "name": "app-alpha", "subscription": "sub-cluster"}
	cases := []struct {
		label     string
		cluster   string
		bySub     map[string]nodeReport
		wantAsked string
		want      contract.State
	}{
		{"another block's namespace with a null namespace", "sub-cluster",
			map[string]nodeReport{"sub-cluster": complete(nullNamespace), "sub-1": complete()}, "sub-cluster", contract.Present},
		{"another block's namespace with no namespace key", "sub-cluster",
			map[string]nodeReport{"sub-cluster": complete(noNamespace), "sub-1": complete()}, "sub-cluster", contract.Present},
		{"a namespace labelled with the cluster's own subscription", "sub-cluster",
			map[string]nodeReport{"sub-cluster": complete(clusterOwn), "sub-1": complete(noNamespace)}, "sub-cluster", contract.Absent},
		{"no cluster subscription named, so the observing subscription's own", "",
			map[string]nodeReport{"sub-cluster": complete(), "sub-1": complete(noNamespace)}, "sub-1", contract.Present},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			api := &fakeReports{bySub: map[string][]observe.NodeReport{}}
			for sub, r := range tc.bySub {
				api.bySub[sub] = []observe.NodeReport{r.build(t)}
			}
			ref := namespaceRef()
			if tc.cluster != "" {
				ref.Identity["clusterSubscription"] = tc.cluster
			}
			got, err := observe.NodeReported{API: api}.ObserveResource(context.Background(), ref)
			wantObservation(t, got, err, tc.want, nil, "")
			if len(api.asked) != 1 || api.asked[0] != tc.wantAsked {
				t.Errorf("asked for the reports of %q, want %s once", api.asked, tc.wantAsked)
			}
		})
	}
}

func TestNodeReported_AnIdentityNamingNoObjectIsUnknownAndAsksNothing(t *testing.T) {
	api := &fakeReports{}
	r := namespaceRef()
	r.Identity = map[string]string{"kind": "Namespace"}
	got, err := observe.NodeReported{API: api}.ObserveResource(context.Background(), r)
	wantObservation(t, got, err, contract.Unknown, nil, "")
	if len(api.asked) != 0 {
		t.Errorf("asked for reports %q, want no call without a name", api.asked)
	}
}

func TestNodeReported_ReturnsTheAPIsError(t *testing.T) {
	boom := errors.New("platform: 503")
	_, err := observe.NodeReported{API: &fakeReports{err: boom}}.ObserveResource(context.Background(), namespaceRef())
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the API's error", err)
	}
}
