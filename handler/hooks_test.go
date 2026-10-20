// Each registered hook runs at its own point around the verb it guards, and a before hook sees its verb not yet run.
package handler_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// hookRun is safe without a lock because verb dispatch is single-threaded and synchronous.
type hookRun struct {
	hook      string
	verbCalls int
	region    string
}

func TestServe_RunsEachRegisteredHookAroundItsVerb(t *testing.T) {
	bE := handler.HookBeforeEnsure
	ensureHooks, deleteHooks := []string{bE, handler.HookAfterEnsure}, []string{handler.HookBeforeDelete, handler.HookAfterDelete}
	every := append(slices.Clone(ensureHooks), deleteHooks...)
	all, onlyBE := manifestDeclaring(every...), []string{bE}
	ensured, observed, deleted := [3]int{1, 1, 0}, [3]int{0, 1, 0}, [3]int{0, 1, 1}
	golden, twin := string(contract.HooksManifestGolden()), string(contract.TypeBlockHooksManifestGolden())
	cases := []struct {
		name     string
		manifest string
		hooks    []string
		op       string
		count    int
		fields   map[string]any
		want     []string
		calls    [3]int
		region   string
	}{
		{"a subscribe runs both ensure hooks around one Ensure and stops observing once present", all, every, "subscribe", 0, nil, ensureHooks, ensured, ""},
		{"an upgrade runs the same call sequence as a subscribe", all, every, "upgrade", 0, nil, ensureHooks, ensured, ""},
		{"a validate runs no hook and observes once", all, every, "validate", 1, nil, nil, observed, ""},
		{"an unsubscribe runs both delete hooks around one Delete and stops observing once absent", all, every, "unsubscribe", 1, nil, deleteHooks, deleted, ""},
		{"both delete hooks receive the request's instance config", manifestDeclaring(deleteHooks...), deleteHooks, "unsubscribe", 1,
			map[string]any{"instances": awsInstances("eu-central-1")}, deleteHooks, deleted, "eu-central-1"},
		{"a declared and registered beforeEnsure runs before Ensure", manifestDeclaring(bE), onlyBE, "subscribe", 0, nil, onlyBE, ensured, ""},
		{"a declared hook nobody registers leaves the verb unchanged", manifestDeclaring(ensureHooks...), nil, "subscribe", 0, nil, nil, ensured, ""},
		{"the flow form hooks: [beforeEnsure] is read", manifestWithLifecycle("    hooks: [beforeEnsure]\n"), onlyBE, "subscribe", 0, nil, onlyBE, ensured, ""},
		{"the shared hooks golden reaches its beforeEnsure hook", golden, contract.HooksManifestGoldenDeclares(), "subscribe", 0, nil, onlyBE, ensured, ""},
		{"the type: block twin reaches its beforeEnsure hook", twin, contract.HooksManifestGoldenDeclares(), "subscribe", 0, nil, onlyBE, ensured, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &kittest.FakeResource{Count: tc.count}
			var runs []hookRun
			hooks := map[string]handler.Hook{}
			for _, name := range tc.hooks {
				hooks[name] = func(_ context.Context, d contract.Desired) error {
					calls := f.EnsureCalls
					if strings.HasSuffix(name, "Delete") {
						calls = f.DeleteCalls
					}
					runs = append(runs, hookRun{name, calls, instanceRegion(d.Instances)})
					return nil
				}
			}
			x := newHarness(t, f, withManifest(tc.manifest), func(c *handler.Config) { c.Hooks = hooks })
			fields := map[string]any{"operation": tc.op}
			for k, v := range tc.fields {
				fields[k] = v
			}

			status, _ := x.answer(fields)

			if status != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200", tc.op, status)
			}
			var got []string
			for _, run := range runs {
				got = append(got, run.hook)
				want := 1
				if strings.HasPrefix(run.hook, "before") {
					want = 0
				}
				if run.verbCalls != want {
					t.Errorf("hook %s ran after %d calls of its verb, want %d", run.hook, run.verbCalls, want)
				}
				if run.region != tc.region {
					t.Errorf("hook %s saw region %q, want %q", run.hook, run.region, tc.region)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("hooks ran %v, want %v", got, tc.want)
			}
			assertCalls(t, f, tc.calls)
		})
	}
}
