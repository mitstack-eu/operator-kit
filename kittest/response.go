package kittest

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// respGoldenEntries fails the test rather than skipping, and reads the goldens embedded in the contract package so an external operator needs no working-directory path.
func respGoldenEntries(t *testing.T) []fs.DirEntry {
	t.Helper()
	entries, err := fs.ReadDir(contract.ResponseGoldens, contract.ResponseGoldensDir)
	if err != nil {
		t.Fatalf("kittest: cannot read embedded response contract goldens: %v\n"+
			"The goldens are the contract; a conformance test that cannot find them proves nothing.", err)
	}
	if len(entries) == 0 {
		t.Fatalf("kittest: embedded response contract goldens are empty — this harness would pass vacuously")
	}
	return entries
}

func readRespGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := contract.ResponseGoldens.ReadFile(contract.ResponseGoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("kittest: reading embedded response golden %q: %v", name, err)
	}
	return raw
}

// ValidResponseGoldens returns the well-formed response goldens, excluding names starting with "invalid-".
func ValidResponseGoldens(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, e := range respGoldenEntries(t) {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, "invalid-") {
			continue
		}
		out[strings.TrimSuffix(name, ".json")] = readRespGolden(t, name)
	}
	if len(out) == 0 {
		t.Fatal("kittest: no valid response goldens found — this harness would pass vacuously")
	}
	return out
}

// InvalidResponseGoldens returns the malformed response goldens CheckResponse must reject.
func InvalidResponseGoldens(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, e := range respGoldenEntries(t) {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "invalid-") {
			continue
		}
		out[strings.TrimSuffix(name, ".json")] = readRespGolden(t, name)
	}
	if len(out) == 0 {
		t.Fatal("kittest: no invalid response goldens found — the rejection half of this harness would pass vacuously")
	}
	return out
}

// CheckResponse is hand-written because the kit module has no third-party dependencies, and it accepts an unknown top-level property so a new field never fails a shipping operator's test.
func CheckResponse(raw []byte) []string {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return []string{fmt.Sprintf("response body is not a JSON object: %v", err)}
	}

	var problems []string

	checkStringMap := func(field string) {
		v, present := doc[field]
		if !present || v == nil {
			return // absent or explicit null — tolerated (a Go nil map encodes to null)
		}
		obj, ok := v.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%q must be an object of string values (or null), got %T", field, v))
			return
		}
		for k, ev := range obj {
			if _, ok := ev.(string); !ok {
				problems = append(problems, fmt.Sprintf("%q.%q must be a string value, got %T", field, k, ev))
			}
		}
	}

	checkBool := func(field string, nullable bool) {
		v, present := doc[field]
		if !present {
			return
		}
		if v == nil {
			if nullable {
				return
			}
			problems = append(problems, fmt.Sprintf("%q must be a boolean, got null", field))
			return
		}
		if _, ok := v.(bool); !ok {
			problems = append(problems, fmt.Sprintf("%q must be a boolean, got %T", field, v))
		}
	}

	checkString := func(field string) {
		v, present := doc[field]
		if !present || v == nil {
			return
		}
		if _, ok := v.(string); !ok {
			problems = append(problems, fmt.Sprintf("%q must be a string, got %T", field, v))
		}
	}

	checkStringMap("outputs")
	checkStringMap("secretOutputs")
	checkBool("pending", false)
	checkBool("redrive", false)
	checkBool("absent", true)
	checkString("error")
	checkString("statusDetail")
	checkString("lastError")
	problems = append(problems, checkResources(doc)...)
	problems = append(problems, checkResourceSecretOutputs(doc)...)

	return problems
}

func checkResourceSecretOutputs(doc map[string]any) []string {
	v, present := doc["resourceSecretOutputs"]
	if !present {
		return nil
	}
	byResource, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%q must be an object of per-resource secret outputs, got %s", "resourceSecretOutputs", jsonKind(v))}
	}

	var problems []string
	for _, name := range sortedKeys(byResource) {
		path := fmt.Sprintf("%q.%q", "resourceSecretOutputs", name)
		secrets, ok := byResource[name].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s must be an object of string values, got %s", path, jsonKind(byResource[name])))
			continue
		}
		for _, k := range sortedKeys(secrets) {
			if _, ok := secrets[k].(string); !ok {
				problems = append(problems, fmt.Sprintf("%s.%q must be a string value, got %s", path, k, jsonKind(secrets[k])))
			}
		}
	}
	return problems
}

var resourceCalls = []string{"ensure", "observe", "delete"}

// checkResources accepts a key the contract does not name at every level.
func checkResources(doc map[string]any) []string {
	v, present := doc["resources"]
	if !present {
		return nil
	}
	entries, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%q must be an object of resource entries, got %s", "resources", jsonKind(v))}
	}

	var problems []string
	for _, name := range sortedKeys(entries) {
		entryPath := fmt.Sprintf("%q.%q", "resources", name)
		entry, ok := entries[name].(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s must be an object, got %s", entryPath, jsonKind(entries[name])))
			continue
		}
		for _, call := range resourceCalls {
			cv, present := entry[call]
			if !present {
				continue
			}
			callPath := fmt.Sprintf("%s.%q", entryPath, call)
			fields, ok := cv.(map[string]any)
			if !ok {
				problems = append(problems, fmt.Sprintf("%s must be an object, got %s", callPath, jsonKind(cv)))
				continue
			}
			problems = append(problems, checkCallOutcome(callPath, fields)...)
		}
	}
	return problems
}

func checkCallOutcome(path string, fields map[string]any) []string {
	var problems []string
	for _, field := range []string{"error", "state", "statusDetail"} {
		v, present := fields[field]
		if !present {
			continue
		}
		if _, ok := v.(string); !ok {
			problems = append(problems, fmt.Sprintf("%s.%q must be a string, got %s", path, field, jsonKind(v)))
		}
	}
	for _, field := range []string{"outputs", "identity"} {
		v, present := fields[field]
		if !present {
			continue
		}
		fieldPath := fmt.Sprintf("%s.%q", path, field)
		obj, ok := v.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s must be an object of string values, got %s", fieldPath, jsonKind(v)))
			continue
		}
		for _, k := range sortedKeys(obj) {
			if _, ok := obj[k].(string); !ok {
				problems = append(problems, fmt.Sprintf("%s.%q must be a string value, got %s", fieldPath, k, jsonKind(obj[k])))
			}
		}
	}
	return problems
}

func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// AssertEmitsConformingResponses judges the exact bytes the handler wrote, never a re-encoded struct, and fails on an empty bodies map rather than passing vacuously.
func AssertEmitsConformingResponses(t *testing.T, bodies map[string][]byte) {
	t.Helper()
	if len(bodies) == 0 {
		t.Fatal("kittest: AssertEmitsConformingResponses received no response bodies — " +
			"an operator conformance test that captures nothing proves nothing about what this operator emits")
	}

	for label, raw := range bodies {
		t.Run(label, func(t *testing.T) {
			if problems := CheckResponse(raw); len(problems) != 0 {
				t.Errorf("response %q does not conform to the lifecycle webhook response contract: %v\n%s",
					label, problems, raw)
			}
		})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
