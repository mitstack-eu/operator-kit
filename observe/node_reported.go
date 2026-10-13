package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// NodeReport is one report a node of the Hetzner cluster sent the platform,
// as GET /v1/subscriptions/{id}/node-reports serves it.
type NodeReport struct {
	ReceivedAt time.Time
	Body       json.RawMessage
}

type NodeReportsAPI interface {
	NodeReports(ctx context.Context, d contract.Desired, subscriptionID string) ([]NodeReport, error)
}

// NodeReported observes a resource the platform has no route to, such as a
// kubernetes.namespace, by the kind, name and optional namespace its
// identity names, reporting presence and identity only. It reads the
// reports of the cluster subscription the identity's clusterSubscription
// names, else its own, and matches an object labelled with the observing
// subscription's id. A report counts
// when its madeAt, else its receivedAt, is after the verb, and the newest
// counting report decides: a listed object is present, and an unlisted one
// is absent only when that report has a madeAt after the verb and marks its
// list complete. Anything else is unknown.
type NodeReported struct {
	API NodeReportsAPI
}

type reportedObject struct {
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Subscription string `json:"subscription"`
}

type reportBody struct {
	MadeAt                        *time.Time      `json:"madeAt"`
	SubscriptionResources         json.RawMessage `json:"subscriptionResources"`
	SubscriptionResourcesComplete bool            `json:"subscriptionResourcesComplete"`
}

func (o NodeReported) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "kind", "name")
	if !ok {
		return obs, nil
	}
	cluster := r.Identity["clusterSubscription"]
	if cluster == "" {
		cluster = r.Desired.SubscriptionID
	}
	reports, err := o.API.NodeReports(ctx, r.Desired, cluster)
	if err != nil {
		return contract.Observation{}, err
	}
	var newest *reportBody
	var newestAt time.Time
	for _, report := range reports {
		var body reportBody
		if err := json.Unmarshal(report.Body, &body); err != nil {
			return contract.Observation{}, fmt.Errorf("observe: reading the node report received at %s: %w", report.ReceivedAt.Format(time.RFC3339), err)
		}
		at := report.ReceivedAt
		if body.MadeAt != nil {
			at = *body.MadeAt
		}
		if at.After(r.Desired.VerbAt) && (newest == nil || at.After(newestAt)) {
			newest, newestAt = &body, at
		}
	}
	if newest == nil {
		return unknown("resource %q: no node report made since the verb has arrived", r.Name), nil
	}
	var listed []reportedObject
	if json.Unmarshal(newest.SubscriptionResources, &listed) != nil {
		return unknown("resource %q: the newest node report could not list the subscription's objects", r.Name), nil
	}
	want := reportedObject{Namespace: r.Identity["namespace"], Kind: ids[0], Name: ids[1], Subscription: r.Desired.SubscriptionID}
	for _, item := range listed {
		if item == want {
			return present(nil), nil
		}
	}
	if newest.MadeAt == nil || !newest.SubscriptionResourcesComplete {
		return unknown("resource %q: the newest node report does not list it, but proves no absence: it carries no madeAt or does not mark its list complete", r.Name), nil
	}
	return absent(), nil
}
