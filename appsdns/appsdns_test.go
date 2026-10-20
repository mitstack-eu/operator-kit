package appsdns_test

// appsdns_test.go pins the app's A record in the apps zone: written with TTL 60, deleted with the values it holds, and read alone by its observer.

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/appsdns"
	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
)

var _ handler.Observer = appsdns.Zone{}

const (
	zoneName = "apps.mitstack.dev"
	zoneID   = "Z0FAKEAPPS"
	appName  = "myapp"
	hostname = "myapp.apps.mitstack.dev"
	address  = "203.0.113.10"
)

var errAccessDenied = errors.New("AccessDenied: not authorized to perform route53:ChangeResourceRecordSets")

type fakeRecord struct {
	ttl    int64
	values []string
}

type fakeRoute53 struct {
	mu      sync.Mutex
	records map[string]fakeRecord
	calls   []string
	changes []appsdns.Change
	lists   []appsdns.ListInput

	listErr   error
	changeErr map[string]error
	// beforeDelete runs ahead of a DELETE, so a test can remove the record between the read and the delete.
	beforeDelete func(records map[string]fakeRecord)
}

func newFake() *fakeRoute53 {
	return &fakeRoute53{records: map[string]fakeRecord{}, changeErr: map[string]error{}}
}

func (f *fakeRoute53) seed(name string, ttl int64, values ...string) {
	f.records[name] = fakeRecord{ttl: ttl, values: values}
}

func (f *fakeRoute53) record(name string) (fakeRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[name]
	return r, ok
}

func (f *fakeRoute53) zone() appsdns.Zone {
	return appsdns.Zone{Name: zoneName, ID: zoneID, Client: f}
}

func (f *fakeRoute53) ListRecordSets(_ context.Context, in appsdns.ListInput) ([]appsdns.RecordSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ListRecordSets")
	f.lists = append(f.lists, in)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if in.ZoneID != zoneID {
		return nil, errors.New("NoSuchHostedZone: No hosted zone found with ID: " + in.ZoneID)
	}
	limit := int(in.MaxItems)
	if limit == 0 {
		limit = 300
	}
	start := strings.TrimSuffix(in.StartName, ".")
	names := make([]string, 0, len(f.records))
	for name := range f.records {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []appsdns.RecordSet
	for _, name := range names {
		if name < start {
			continue
		}
		if len(out) == limit {
			break
		}
		r := f.records[name]
		out = append(out, appsdns.RecordSet{Name: name + ".", Type: appsdns.TypeA, TTL: r.ttl, Values: slices.Clone(r.values)})
	}
	return out, nil
}

func (f *fakeRoute53) ChangeRecordSet(_ context.Context, id string, c appsdns.Change) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ChangeRecordSet")
	if err := f.changeErr[c.Action]; err != nil {
		return err
	}
	if id != zoneID {
		return errors.New("NoSuchHostedZone: No hosted zone found with ID: " + id)
	}
	if c.RecordSet.Type != appsdns.TypeA {
		return errors.New("InvalidInput: only A records are modelled")
	}
	name := strings.TrimSuffix(c.RecordSet.Name, ".")
	switch c.Action {
	case appsdns.ActionUpsert:
		f.records[name] = fakeRecord{ttl: c.RecordSet.TTL, values: slices.Clone(c.RecordSet.Values)}
	case appsdns.ActionDelete:
		if f.beforeDelete != nil {
			f.beforeDelete(f.records)
		}
		cur, ok := f.records[name]
		if !ok || cur.ttl != c.RecordSet.TTL || !slices.Equal(cur.values, c.RecordSet.Values) {
			return errors.New("InvalidChangeBatch: [Tried to delete resource record set [name='" + name + ".', type='A'] but it was not found]")
		}
		delete(f.records, name)
	default:
		return errors.New("InvalidInput: unmodelled action " + c.Action)
	}
	f.changes = append(f.changes, c)
	return nil
}

func observeHostname(t *testing.T, z appsdns.Zone, identity map[string]string, params map[string]string) contract.Observation {
	t.Helper()
	if params == nil {
		params = map[string]string{"appName": appName}
	}
	obs, err := z.ObserveResource(context.Background(), contract.ResourceRef{
		Name: "mitstack.app-hostname", Type: "mitstack.app-hostname", Identity: identity,
		Desired: contract.Desired{Params: params},
	})
	if err != nil {
		t.Fatalf("ObserveResource(identity %v, params %v) error = %v; want nil, since a failed read is an unknown observation", identity, params, err)
	}
	return obs
}

func TestRecordTTL_IsSixtySeconds(t *testing.T) {
	if appsdns.RecordTTL != 60 {
		t.Errorf("RecordTTL = %d; want 60", appsdns.RecordTTL)
	}
}

func TestHostname_IsTheAppNameUnderTheZone(t *testing.T) {
	cases := []struct {
		label         string
		zone, id, app string
		want          string
	}{
		{"the zone and its id are configured", zoneName, zoneID, appName, hostname},
		{"the zone carries a leading and a trailing dot", "." + zoneName + ".", zoneID, appName, hostname},
		{"no zone is configured", "", zoneID, appName, ""},
		{"no zone id is configured", zoneName, "", appName, ""},
		{"there is no app name", zoneName, zoneID, "", ""},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			z := appsdns.Zone{Name: c.zone, ID: c.id}

			got := z.Hostname(c.app)

			if got != c.want {
				t.Errorf("Zone{Name: %q, ID: %q}.Hostname(%q) = %q; want %q", c.zone, c.id, c.app, got, c.want)
			}
		})
	}
}

func TestPublish_WritesAnARecordWithTTL60ToTheAddress(t *testing.T) {
	f := newFake()

	published, err := f.zone().Publish(context.Background(), appName, address)

	if err != nil || !published {
		t.Fatalf("Publish(%q, %q) = %t, %v; want true, nil", appName, address, published, err)
	}
	rec, held := f.record(hostname)
	if !held || rec.ttl != 60 || !slices.Equal(rec.values, []string{address}) {
		t.Errorf("record %s = %+v (held %t); want an A record with TTL 60 to %s", hostname, rec, held, address)
	}
}

func TestPublish_IsIdempotent(t *testing.T) {
	f := newFake()
	z := f.zone()

	for i := 0; i < 2; i++ {
		if published, err := z.Publish(context.Background(), appName, address); err != nil || !published {
			t.Fatalf("Publish #%d = %t, %v; want true, nil", i+1, published, err)
		}
	}

	if len(f.records) != 1 {
		t.Errorf("the zone holds %d record(s) %v; want one", len(f.records), f.records)
	}
	for _, c := range f.changes {
		if c.Action != appsdns.ActionUpsert || c.RecordSet.Name != hostname {
			t.Errorf("record change %s %s; want only UPSERTs of %s", c.Action, c.RecordSet.Name, hostname)
		}
	}
}

func TestPublish_PointsAnExistingRecordAtANewAddress(t *testing.T) {
	f := newFake()
	f.seed(hostname, 60, "198.51.100.1")

	published, err := f.zone().Publish(context.Background(), appName, address)

	rec, _ := f.record(hostname)
	if err != nil || !published || !slices.Equal(rec.values, []string{address}) {
		t.Errorf("Publish over 198.51.100.1 = %t, %v with record %+v; want true, nil and the record at %s", published, err, rec, address)
	}
}

func TestPublish_WritesNothingWithoutAZoneAnAppOrAnAddress(t *testing.T) {
	cases := []struct {
		label         string
		zone, id, app string
		address       string
	}{
		{"there is no address yet", zoneName, zoneID, appName, ""},
		{"no zone is configured", "", zoneID, appName, address},
		{"no zone id is configured", zoneName, "", appName, address},
		{"there is no app name", zoneName, zoneID, "", address},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			f := newFake()
			z := appsdns.Zone{Name: c.zone, ID: c.id, Client: f}

			published, err := z.Publish(context.Background(), c.app, c.address)

			if err != nil || published || len(f.calls) != 0 {
				t.Errorf("Publish(%q, %q) = %t, %v with calls %v; want false, nil and no call", c.app, c.address, published, err, f.calls)
			}
		})
	}
}

func TestPublish_ARefusedWriteReturnsTheClientErrorAndNothingPublished(t *testing.T) {
	f := newFake()
	f.changeErr[appsdns.ActionUpsert] = errAccessDenied

	published, err := f.zone().Publish(context.Background(), appName, address)

	if published || !errors.Is(err, errAccessDenied) {
		t.Errorf("Publish over a refused UPSERT = %t, %v; want false and an error wrapping %v", published, err, errAccessDenied)
	}
	if err != nil && !strings.Contains(err.Error(), hostname) {
		t.Errorf("Publish error %q does not name the hostname %s", err, hostname)
	}
}

func TestRetire(t *testing.T) {
	throttled := errors.New("Throttling: Rate exceeded")
	neighbour := "myapp2.apps.mitstack.dev"
	repoint := func(records map[string]fakeRecord) {
		records[hostname] = fakeRecord{ttl: 60, values: []string{"198.51.100.9"}}
	}
	cases := []struct {
		name      string
		noZoneID  bool
		arrange   func(f *fakeRoute53)
		wantErrIs error
		wantInErr string
		check     func(t *testing.T, f *fakeRoute53)
	}{
		{"a published record is deleted", false, func(f *fakeRoute53) { f.seed(hostname, 60, address) }, nil, "",
			func(t *testing.T, f *fakeRoute53) {
				if rec, held := f.record(hostname); held {
					t.Errorf("the zone still holds %s = %+v", hostname, rec)
				}
			}},
		{"the DELETE carries the stored TTL and values exactly, which Route 53 requires", false, func(f *fakeRoute53) { f.seed(hostname, 300, "198.51.100.1", "198.51.100.2") }, nil, "",
			func(t *testing.T, f *fakeRoute53) {
				want := []appsdns.Change{{Action: appsdns.ActionDelete, RecordSet: appsdns.RecordSet{Name: hostname, Type: appsdns.TypeA, TTL: 300, Values: []string{"198.51.100.1", "198.51.100.2"}}}}
				if !reflect.DeepEqual(f.changes, want) {
					t.Errorf("changes = %+v; want %+v", f.changes, want)
				}
			}},
		{"the record is read from its name alone", false, func(f *fakeRoute53) { f.seed(hostname, 60, address) }, nil, "",
			func(t *testing.T, f *fakeRoute53) {
				want := appsdns.ListInput{ZoneID: zoneID, StartName: hostname, StartType: appsdns.TypeA, MaxItems: 1}
				if len(f.lists) == 0 || !reflect.DeepEqual(f.lists[0], want) {
					t.Errorf("first listing = %+v; want %+v", f.lists, want)
				}
			}},
		{"a refused DELETE returns the client error naming the hostname and keeps the record", false, func(f *fakeRoute53) {
			f.seed(hostname, 60, address)
			f.changeErr[appsdns.ActionDelete] = errAccessDenied
		}, errAccessDenied, hostname, func(t *testing.T, f *fakeRoute53) {
			if _, held := f.record(hostname); !held {
				t.Error("the record is gone though the DELETE was refused")
			}
		}},
		{"a DELETE refused because the record changed after the read returns that error", false, func(f *fakeRoute53) {
			f.seed(hostname, 60, address)
			f.beforeDelete = repoint
		}, nil, "but it was not found", nil},
		{"a record already gone needs no change", false, nil, nil, "", func(t *testing.T, f *fakeRoute53) {
			if slices.Contains(f.calls, "ChangeRecordSet") {
				t.Errorf("calls = %v; want no change", f.calls)
			}
		}},
		{"a record deleted between the read and the delete counts as deleted", false, func(f *fakeRoute53) {
			f.seed(hostname, 60, address)
			f.beforeDelete = func(records map[string]fakeRecord) { delete(records, hostname) }
		}, nil, "", nil},
		{"a failed read returns the client error and changes nothing", false, func(f *fakeRoute53) {
			f.seed(hostname, 60, address)
			f.listErr = throttled
		}, throttled, "", func(t *testing.T, f *fakeRoute53) {
			if slices.Contains(f.calls, "ChangeRecordSet") {
				t.Errorf("calls = %v; want no change", f.calls)
			}
		}},
		{"a neighbouring record is left alone", false, func(f *fakeRoute53) { f.seed(neighbour, 60, "198.51.100.7") }, nil, "",
			func(t *testing.T, f *fakeRoute53) {
				if _, held := f.record(neighbour); !held {
					t.Errorf("the neighbour %s is gone", neighbour)
				}
			}},
		{"no zone id configured does nothing", true, nil, nil, "", func(t *testing.T, f *fakeRoute53) {
			if len(f.calls) != 0 {
				t.Errorf("calls = %v; want none", f.calls)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			if tc.arrange != nil {
				tc.arrange(f)
			}
			z := f.zone()
			if tc.noZoneID {
				z.ID = ""
			}

			err := z.Retire(context.Background(), appName)

			switch {
			case tc.wantErrIs == nil && tc.wantInErr == "" && err != nil:
				t.Errorf("Retire = %v; want nil", err)
			case tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs):
				t.Errorf("Retire = %v; want an error wrapping %v", err, tc.wantErrIs)
			case tc.wantInErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantInErr)):
				t.Errorf("Retire = %v; want an error naming %q", err, tc.wantInErr)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}

func TestObserve_ReportsTheRecordPresentThenAbsentOnceRetired(t *testing.T) {
	f := newFake()
	z := f.zone()
	if _, err := z.Publish(context.Background(), appName, address); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	before := observeHostname(t, z, map[string]string{"name": hostname}, nil)
	if err := z.Retire(context.Background(), appName); err != nil {
		t.Fatalf("Retire: %v", err)
	}
	after := observeHostname(t, z, map[string]string{"name": hostname}, nil)

	want := map[string]string{"appHostname": hostname, "address": address, "appUrl": "https://" + hostname}
	if before.State != contract.Present || !reflect.DeepEqual(before.Outputs, want) {
		t.Errorf("observation before retire = %+v; want present with outputs %v", before, want)
	}
	if after.State != contract.Absent {
		t.Errorf("observation after retire = %+v; want absent", after)
	}
}

func TestObserve_WritesNothing(t *testing.T) {
	f := newFake()
	f.seed(hostname, 60, "198.51.100.1")

	obs := observeHostname(t, f.zone(), nil, nil)

	if obs.State != contract.Present || obs.Outputs["address"] != "198.51.100.1" {
		t.Errorf("observation = %+v; want present with the address the record holds", obs)
	}
	if slices.Contains(f.calls, "ChangeRecordSet") {
		t.Errorf("the observer made calls %v; want reads only", f.calls)
	}
}

func TestObserve_Cases(t *testing.T) {
	rows := []struct {
		name       string
		zone, id   string
		arrange    func(f *fakeRoute53)
		identity   map[string]string
		params     map[string]string
		want       contract.State
		wantDetail string
	}{
		{"no apps zone is configured", "", zoneID, nil, nil, nil, contract.Unknown, "zone"},
		{"no apps zone id is configured", zoneName, "", nil, nil, nil, contract.Unknown, "zone"},
		{"no name is recorded and no appName names one", zoneName, zoneID, nil, nil, map[string]string{}, contract.Unknown, "appName"},
		{"the record is not held", zoneName, zoneID, nil, nil, nil, contract.Absent, ""},
		{"only a neighbouring record is held", zoneName, zoneID, func(f *fakeRoute53) { f.seed("myapp2.apps.mitstack.dev", 60, "198.51.100.7") }, nil, nil, contract.Absent, ""},
		{"Route 53 cannot be read", zoneName, zoneID, func(f *fakeRoute53) { f.listErr = errors.New("Throttling: Rate exceeded") }, nil, nil, contract.Unknown, "Rate exceeded"},
		{"the record is held under the recorded name", zoneName, zoneID, func(f *fakeRoute53) { f.seed("other.apps.mitstack.dev", 60, "203.0.113.50") }, map[string]string{"name": "other.apps.mitstack.dev"}, nil, contract.Present, ""},
		{"the record is held under the name the appName gives", zoneName, zoneID, func(f *fakeRoute53) { f.seed(hostname, 60, "203.0.113.50") }, nil, nil, contract.Present, ""},
		{"the recorded name differs from the record only in case", zoneName, zoneID, func(f *fakeRoute53) { f.seed(hostname, 60, "203.0.113.50") }, map[string]string{"name": "MyApp.apps.mitstack.dev"}, nil, contract.Present, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f := newFake()
			if row.arrange != nil {
				row.arrange(f)
			}
			z := appsdns.Zone{Name: row.zone, ID: row.id, Client: f}

			obs := observeHostname(t, z, row.identity, row.params)

			if obs.State != row.want || !strings.Contains(obs.StatusDetail, row.wantDetail) {
				t.Errorf("observation = %+v; want %s with a detail containing %q", obs, row.want, row.wantDetail)
			}
		})
	}
}
