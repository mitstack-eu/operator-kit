// Package appsdns, an optional helper, writes, retires and observes an app's A record in the apps
// zone over a Client of stdlib types an operator adapts its Route 53 client to.
package appsdns

import (
	"context"
	"fmt"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/safe"
)

// RecordTTL is short because a replaced box gets a new Elastic IP, and a stale answer would point at an address that is no longer the app's.
const RecordTTL = 60

const (
	ActionUpsert = "UPSERT"
	ActionDelete = "DELETE"
	TypeA        = "A"
)

const (
	outputHostname = "appHostname"
	outputAddress  = "address"
	outputURL      = "appUrl"
)

// RecordSet is a record set as Route 53 holds it; a DELETE must match its TTL and values exactly.
type RecordSet struct {
	Name   string
	Type   string
	TTL    int64
	Values []string
}

type ListInput struct {
	ZoneID    string
	StartName string
	StartType string
	MaxItems  int32
}

type Change struct {
	Action    string
	RecordSet RecordSet
}

// Client is the Route 53 API an operator adapts its SDK client to; an adapter returns the SDK's errors as they are or wrapped with %w.
type Client interface {
	ListRecordSets(ctx context.Context, in ListInput) ([]RecordSet, error)
	ChangeRecordSet(ctx context.Context, zoneID string, c Change) error
}

// Zone is the apps zone, reached with the operator's own credentials because the zone lives in the platform's account.
type Zone struct {
	Name   string
	ID     string
	Client Client
}

func (z Zone) Hostname(appName string) string {
	if z.Name == "" || z.ID == "" || appName == "" {
		return ""
	}
	return appName + "." + strings.Trim(z.Name, ".")
}

// Publish points the app's hostname at address and reports whether the record now holds it.
func (z Zone) Publish(ctx context.Context, appName, address string) (bool, error) {
	host := z.Hostname(appName)
	if host == "" || address == "" {
		return false, nil
	}
	rec := RecordSet{Name: host, Type: TypeA, TTL: RecordTTL, Values: []string{address}}
	if err := z.change(ctx, ActionUpsert, rec); err != nil {
		return false, err
	}
	return true, nil
}

// Retire deletes the record with the TTL and values it holds, which Route 53 requires; a record already gone counts as deleted.
func (z Zone) Retire(ctx context.Context, appName string) error {
	host := z.Hostname(appName)
	if host == "" {
		return nil
	}
	rec, found, err := z.read(ctx, host)
	if err != nil || !found {
		return err
	}
	deleteErr := z.change(ctx, ActionDelete, rec)
	if deleteErr == nil {
		return nil
	}
	if _, stillHeld, readErr := z.read(ctx, host); readErr == nil && !stillHeld {
		return nil
	}
	return deleteErr
}

// ObserveResource reads only Route 53, so it answers the record alone, whatever the host's state.
func (z Zone) ObserveResource(ctx context.Context, ref contract.ResourceRef) (contract.Observation, error) {
	if z.Name == "" || z.ID == "" {
		return unknown("no apps zone is configured (MITSTACK_APPS_ZONE and MITSTACK_APPS_ZONE_ID), so the app's hostname record cannot be read"), nil
	}
	name := ref.Identity["name"]
	if name == "" {
		name = z.Hostname(ref.Desired.Params["appName"])
	}
	if name == "" {
		return unknown("no hostname is recorded and no appName names one"), nil
	}
	rec, found, err := z.read(ctx, name)
	if err != nil {
		return unknown("%s", safe.LogValue(err.Error())), nil
	}
	if !found {
		return contract.Observation{State: contract.Absent}, nil
	}
	return contract.Observation{
		State:   contract.Present,
		Outputs: map[string]string{outputHostname: name, outputAddress: rec.Values[0], outputURL: "https://" + name},
	}, nil
}

func (z Zone) read(ctx context.Context, name string) (RecordSet, bool, error) {
	sets, err := z.Client.ListRecordSets(ctx, ListInput{ZoneID: z.ID, StartName: name, StartType: TypeA, MaxItems: 1})
	if err != nil {
		return RecordSet{}, false, fmt.Errorf("reading the A record of %s: %w", name, err)
	}
	for _, set := range sets {
		if set.Type != TypeA || !strings.EqualFold(strings.TrimSuffix(set.Name, "."), name) {
			continue
		}
		rec := RecordSet{Name: name, Type: TypeA, TTL: set.TTL, Values: set.Values}
		return rec, len(rec.Values) > 0, nil
	}
	return RecordSet{}, false, nil
}

func (z Zone) change(ctx context.Context, action string, rec RecordSet) error {
	if err := z.Client.ChangeRecordSet(ctx, z.ID, Change{Action: action, RecordSet: rec}); err != nil {
		return fmt.Errorf("route53 %s of %s: %w", strings.ToLower(action), rec.Name, err)
	}
	return nil
}

func unknown(format string, args ...any) contract.Observation {
	return contract.Observation{State: contract.Unknown, StatusDetail: fmt.Sprintf(format, args...)}
}
