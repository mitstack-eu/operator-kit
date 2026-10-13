package contract_test

// manifest_type_block_test.go pins the type: block twin of the lifecycle
// hooks golden: the same block written with type, format and store. It is
// embedded beside the kind-named golden, names its document with type: block
// and no top-level kind:, carries no layer, types a capability contract with
// format: and a continuity unit with store:, and declares the same hooks.
// The kit takes no YAML dependency, so these read the golden line by line.

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const typeBlockGoldenName = "lifecycle-hooks-type-block.yaml"

func goldenLines(raw []byte) []string { return strings.Split(string(raw), "\n") }

func lineIndent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

// topLevelLine returns the index of the first unindented line whose trimmed
// text equals key, or -1.
func topLevelLine(lines []string, key string) int {
	for i, line := range lines {
		if lineIndent(line) == 0 && strings.TrimSpace(line) == key {
			return i
		}
	}
	return -1
}

// blockUnder returns the lines nested under lines[at]: every following line
// indented deeper than it, up to the first non-blank line that is not.
func blockUnder(lines []string, at int) []string {
	if at < 0 {
		return nil
	}
	var out []string
	for _, line := range lines[at+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if lineIndent(line) <= lineIndent(lines[at]) {
			break
		}
		out = append(out, line)
	}
	return out
}

// directChild returns the index in lines of the first direct child of
// lines[at] whose trimmed text equals key, or -1.
func directChild(lines []string, at int, key string) int {
	if at < 0 {
		return -1
	}
	child := -1
	for i := at + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if lineIndent(line) <= lineIndent(lines[at]) {
			break
		}
		if child < 0 {
			child = lineIndent(line)
		}
		if lineIndent(line) == child && strings.TrimSpace(line) == key {
			return i
		}
	}
	return -1
}

// hasKeyLine reports whether any of lines carries key as a mapping key,
// either on its own line or as the first key of a sequence item.
func hasKeyLine(lines []string, key string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if strings.HasPrefix(trimmed, key+":") {
			return true
		}
	}
	return false
}

// declaredHooks reads the names under spec.lifecycle.hooks, written either as
// an indented "- name" sequence or as a flow list.
func declaredHooks(t *testing.T, raw []byte) []string {
	t.Helper()
	lines := goldenLines(raw)
	lifecycle := directChild(lines, topLevelLine(lines, "spec:"), "lifecycle:")
	if lifecycle < 0 {
		t.Fatalf("golden carries no spec.lifecycle:\n%s", raw)
	}
	for i, line := range lines[lifecycle+1:] {
		idx := lifecycle + 1 + i
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if lineIndent(line) <= lineIndent(lines[lifecycle]) {
			break
		}
		rest, ok := strings.CutPrefix(trimmed, "hooks:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
			var names []string
			for _, n := range strings.Split(rest[1:len(rest)-1], ",") {
				if n = strings.TrimSpace(n); n != "" {
					names = append(names, n)
				}
			}
			return names
		}
		var names []string
		for _, item := range lines[idx+1:] {
			it := strings.TrimSpace(item)
			if it == "" {
				continue
			}
			if lineIndent(item) < lineIndent(line) || !strings.HasPrefix(it, "- ") {
				break
			}
			names = append(names, strings.TrimSpace(strings.TrimPrefix(it, "- ")))
		}
		return names
	}
	t.Fatalf("golden declares no spec.lifecycle.hooks:\n%s", raw)
	return nil
}

func TestManifestGoldens_EmbedsTheTypeBlockHooksGolden(t *testing.T) {
	raw, err := fs.ReadFile(contract.ManifestGoldens, contract.ManifestGoldensDir+"/"+typeBlockGoldenName)
	if err != nil {
		t.Fatalf("reading %s from contract.ManifestGoldens: %v", typeBlockGoldenName, err)
	}
	if string(raw) != string(contract.TypeBlockHooksManifestGolden()) {
		t.Errorf("contract.TypeBlockHooksManifestGolden() differs from the embedded manifest-goldens/%s", typeBlockGoldenName)
	}
}

func TestTypeBlockHooksManifestGolden_NamesItsDocumentWithTypeBlockAndNoKind(t *testing.T) {
	raw := contract.TypeBlockHooksManifestGolden()
	lines := goldenLines(raw)
	if topLevelLine(lines, "type: block") < 0 {
		t.Errorf("twin golden carries no top-level \"type: block\":\n%s", raw)
	}
	for _, line := range lines {
		if lineIndent(line) == 0 && strings.HasPrefix(strings.TrimSpace(line), "kind:") {
			t.Errorf("twin golden carries a top-level kind: line %q", line)
		}
	}
}

func TestTypeBlockHooksManifestGolden_CarriesNoLayer(t *testing.T) {
	raw := contract.TypeBlockHooksManifestGolden()
	lines := goldenLines(raw)
	spec := topLevelLine(lines, "spec:")
	if spec < 0 {
		t.Fatalf("twin golden carries no spec:\n%s", raw)
	}
	if hasKeyLine(blockUnder(lines, spec), "layer") {
		t.Errorf("twin golden carries a layer: under spec:\n%s", raw)
	}
}

func TestTypeBlockHooksManifestGolden_TypesACapabilityContractWithFormat(t *testing.T) {
	raw := contract.TypeBlockHooksManifestGolden()
	lines := goldenLines(raw)
	spec := topLevelLine(lines, "spec:")
	var found bool
	for _, key := range []string{"requires:", "providedContracts:"} {
		if hasKeyLine(blockUnder(lines, directChild(lines, spec, key)), "format") {
			found = true
		}
	}
	if !found {
		t.Errorf("twin golden types no requirement or provided contract with format::\n%s", raw)
	}
}

func TestTypeBlockHooksManifestGolden_DeclaresAContinuityUnitWithStore(t *testing.T) {
	raw := contract.TypeBlockHooksManifestGolden()
	lines := goldenLines(raw)
	continuity := directChild(lines, topLevelLine(lines, "spec:"), "continuity:")
	if !hasKeyLine(blockUnder(lines, continuity), "store") {
		t.Errorf("twin golden declares no spec.continuity unit with store::\n%s", raw)
	}
}

func TestTypeBlockHooksManifestGolden_DeclaresTheSameHooksAsTheHooksGolden(t *testing.T) {
	got := declaredHooks(t, contract.TypeBlockHooksManifestGolden())
	if want := contract.HooksManifestGoldenDeclares(); !reflect.DeepEqual(got, want) {
		t.Fatalf("twin golden declares hooks %v, contract.HooksManifestGoldenDeclares() = %v", got, want)
	}
	if old := declaredHooks(t, contract.HooksManifestGolden()); !reflect.DeepEqual(got, old) {
		t.Fatalf("twin golden declares hooks %v, the hooks golden declares %v", got, old)
	}
}

// TestTypeBlockHooksManifestGolden_IsTheHooksGoldenInTheNewNames pins that the
// twin is the same block: every line of the kind-named golden appears in it,
// except its top-level kind: line and its spec layer: line, which the twin
// does not carry.
func TestTypeBlockHooksManifestGolden_IsTheHooksGoldenInTheNewNames(t *testing.T) {
	twin := map[string]bool{}
	for _, line := range goldenLines(contract.TypeBlockHooksManifestGolden()) {
		twin[line] = true
	}
	for _, line := range goldenLines(contract.HooksManifestGolden()) {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case lineIndent(line) == 0 && strings.HasPrefix(trimmed, "kind:"):
			continue
		case strings.HasPrefix(trimmed, "layer:"):
			continue
		}
		if !twin[line] {
			t.Errorf("twin golden lacks the hooks golden's line %q", line)
		}
	}
}
