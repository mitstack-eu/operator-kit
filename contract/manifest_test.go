package contract_test

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

func TestLifecycleHooksField_IsSpecLifecycleHooks(t *testing.T) {
	if contract.LifecycleHooksField != "spec.lifecycle.hooks" {
		t.Fatalf("contract.LifecycleHooksField = %q, want %q", contract.LifecycleHooksField, "spec.lifecycle.hooks")
	}
}

func TestLifecycleHookPoints_AreTheFourInOrder(t *testing.T) {
	want := []string{"beforeEnsure", "afterEnsure", "beforeDelete", "afterDelete"}
	if got := contract.LifecycleHookPoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("contract.LifecycleHookPoints() = %v, want %v", got, want)
	}
}

func TestLifecycleHookPoints_ReturnsACopy(t *testing.T) {
	first := contract.LifecycleHookPoints()
	if len(first) == 0 {
		t.Fatal("contract.LifecycleHookPoints() is empty")
	}
	first[0] = "mutated"
	if got := contract.LifecycleHookPoints(); got[0] != "beforeEnsure" {
		t.Fatalf("mutating the returned slice changed the next call: got[0] = %q, want %q", got[0], "beforeEnsure")
	}
}

func TestManifestGoldensDir_IsManifestGoldens(t *testing.T) {
	if contract.ManifestGoldensDir != "manifest-goldens" {
		t.Fatalf("contract.ManifestGoldensDir = %q, want %q", contract.ManifestGoldensDir, "manifest-goldens")
	}
}

func TestManifestGoldens_EmbedsTheLifecycleHooksGolden(t *testing.T) {
	raw, err := fs.ReadFile(contract.ManifestGoldens, contract.ManifestGoldensDir+"/lifecycle-hooks.yaml")
	if err != nil {
		t.Fatalf("reading lifecycle-hooks.yaml from contract.ManifestGoldens: %v", err)
	}
	if string(raw) != string(contract.HooksManifestGolden()) {
		t.Error("contract.HooksManifestGolden() differs from the embedded manifest-goldens/lifecycle-hooks.yaml")
	}
}

// TestHooksManifestGolden_NestsHooksUnderLifecycle pins that the golden
// carries hooks: as a child of spec.lifecycle, the one path the field
// constant names.
func TestHooksManifestGolden_NestsHooksUnderLifecycle(t *testing.T) {
	raw := contract.HooksManifestGolden()
	if len(raw) == 0 {
		t.Fatal("contract.HooksManifestGolden() is empty")
	}
	lines := strings.Split(string(raw), "\n")
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

	specAt, lifecycleAt, hooksAt := -1, -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case specAt < 0 && trimmed == "spec:" && indent(line) == 0:
			specAt = i
		case specAt >= 0 && lifecycleAt < 0 && trimmed == "lifecycle:" && indent(line) > 0:
			lifecycleAt = i
		case lifecycleAt >= 0 && hooksAt < 0 && trimmed == "hooks:" && indent(line) > indent(lines[lifecycleAt]):
			hooksAt = i
		}
	}
	if specAt < 0 || lifecycleAt < 0 || hooksAt < 0 {
		t.Fatalf("golden does not nest hooks: under spec.lifecycle (spec at %d, lifecycle at %d, hooks at %d):\n%s",
			specAt, lifecycleAt, hooksAt, raw)
	}
}

func TestHooksManifestGoldenDeclares_IsBeforeEnsureAndAfterDelete(t *testing.T) {
	want := []string{"beforeEnsure", "afterDelete"}
	if got := contract.HooksManifestGoldenDeclares(); !reflect.DeepEqual(got, want) {
		t.Fatalf("contract.HooksManifestGoldenDeclares() = %v, want %v", got, want)
	}
}

func TestHooksManifestGoldenDeclares_ReturnsACopy(t *testing.T) {
	first := contract.HooksManifestGoldenDeclares()
	if len(first) == 0 {
		t.Fatal("contract.HooksManifestGoldenDeclares() is empty")
	}
	first[0] = "mutated"
	if got := contract.HooksManifestGoldenDeclares(); got[0] != "beforeEnsure" {
		t.Fatalf("mutating the returned slice changed the next call: got[0] = %q, want %q", got[0], "beforeEnsure")
	}
}

// TestHooksManifestGoldenDeclares_EveryNameIsAHookPoint pins that the
// golden declares only names from the closed hook-point set.
func TestHooksManifestGoldenDeclares_EveryNameIsAHookPoint(t *testing.T) {
	known := map[string]bool{}
	for _, p := range contract.LifecycleHookPoints() {
		known[p] = true
	}
	for _, name := range contract.HooksManifestGoldenDeclares() {
		if !known[name] {
			t.Errorf("golden declares %q, which is not in contract.LifecycleHookPoints() %v", name, contract.LifecycleHookPoints())
		}
	}
}

// TestHooksManifestGoldenDeclares_MatchesTheGoldenText pins that the "- name"
// items under the golden's hooks: key are exactly the accessor's names, in
// order, so the accessor neither omits an item the file carries nor names
// one it lacks.
func TestHooksManifestGoldenDeclares_MatchesTheGoldenText(t *testing.T) {
	raw := string(contract.HooksManifestGolden())
	lines := strings.Split(raw, "\n")
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

	hooksAt := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "hooks:" {
			hooksAt = i
			break
		}
	}
	if hooksAt < 0 {
		t.Fatalf("golden carries no \"hooks:\" block-sequence key:\n%s", raw)
	}

	var items []string
	for _, line := range lines[hooksAt+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indent(line) <= indent(lines[hooksAt]) || !strings.HasPrefix(trimmed, "- ") {
			break
		}
		items = append(items, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
	}

	if want := contract.HooksManifestGoldenDeclares(); !reflect.DeepEqual(items, want) {
		t.Fatalf("golden hooks: items = %v, contract.HooksManifestGoldenDeclares() = %v:\n%s", items, want, raw)
	}
}

func TestAppNameNamingField_IsSpecNamingAppName(t *testing.T) {
	if contract.AppNameNamingField != "spec.naming.appName" {
		t.Fatalf("contract.AppNameNamingField = %q, want %q", contract.AppNameNamingField, "spec.naming.appName")
	}
}
