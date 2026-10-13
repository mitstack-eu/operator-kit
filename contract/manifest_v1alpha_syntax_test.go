package contract_test

// contract.ParseManifest reads v1alpha YAML as the platform's decoder does, but for three refusals the kit keeps: a second document, a float with no integer value in an integer field, and an alias or merge key.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const verifyHead = "  requires:\n    - capability: github\n      verify:\n        urlTemplate: https://api.example.test/user\n"

var scalarPositions = map[string]func(value string) string{
	"spec.description":                  func(v string) string { return "  description: " + v + "\n" },
	"spec.tags[0]":                      func(v string) string { return "  tags:\n    - " + v + "\n" },
	"spec.requires[0].verify.headers.h": func(v string) string { return verifyHead + "        headers:\n          h: " + v + "\n" },
	"spec.params[0].default":            func(v string) string { return "  params:\n    - name: image\n      default: " + v + "\n" },
}

func TestParseManifest_V1AlphaRefusesWhatYAMLRefusesNamingThePath(t *testing.T) {
	cases := []struct {
		label, value string
		positions    []string
	}{
		{"a plain value holding \": \"", "a: b", []string{"spec.description", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a value starting with %", "%x", []string{"spec.description", "spec.tags[0]", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a ? complex key", "? a", []string{"spec.description", "spec.tags[0]", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a bare - as a mapping value", "-", []string{"spec.description", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
	}
	for _, tc := range cases {
		for _, path := range tc.positions {
			t.Run(tc.label+" at "+path, func(t *testing.T) {
				requireRefusedNaming(t, []byte(alphaHead+scalarPositions[path](tc.value)), path)
			})
		}
	}
}

// !!str and !!int give a string field their text, !!int gives an integer field its number, and !!str into an integer, a list or a bool is refused.
func TestParseManifest_V1AlphaReadsTagsAsYAMLDoes(t *testing.T) {
	for _, tag := range []string{"!!str 5", "!!int 5"} {
		t.Run("description: "+tag, func(t *testing.T) {
			wantField(t, "spec.description", alphaSpec(t, "  description: "+tag+"\n").Spec.Description, "5")
		})
		t.Run("a param default "+tag, func(t *testing.T) {
			a := alphaSpec(t, scalarPositions["spec.params[0].default"](tag))
			requireLen(t, "spec.params", a.Spec.Params, 1)
			wantField(t, "spec.params[0].default", a.Spec.Params[0].Default, "5")
		})
	}
	t.Run("expectStatus: [!!int 5]", func(t *testing.T) {
		a := alphaSpec(t, verifyHead+"        expectStatus: [!!int 5]\n")
		requireLen(t, "spec.requires", a.Spec.Requires, 1)
		requirePresent(t, "spec.requires[0].verify", a.Spec.Requires[0].Verify)
		wantField(t, "spec.requires[0].verify.expectStatus", a.Spec.Requires[0].Verify.ExpectStatus, []int{5})
	})
	for _, tc := range []struct{ label, path, lines string }{
		{"!!str 5 in a flow expectStatus", "spec.requires[0].verify.expectStatus[0]", verifyHead + "        expectStatus: [!!str 5]\n"},
		{"!!str 5 in a block expectStatus", "spec.requires[0].verify.expectStatus[0]", verifyHead + "        expectStatus:\n          - !!str 5\n"},
		{"!!str 5 as the tags list", "spec.tags", "  tags: !!str 5\n"},
		{"!!str 5 as a bool", "spec.optional", "  optional: !!str 5\n"},
	} {
		t.Run("refuses "+tc.label, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+tc.lines), tc.path)
		})
	}
}

func TestParseManifest_V1AlphaAcceptsALeadingByteOrderMark(t *testing.T) {
	a := parseAlpha(t, "\xef\xbb\xbf"+alphaHead+"  description: hi\n")
	wantField(t, "kind", a.Kind, "Block")
	wantField(t, "metadata.id", a.Metadata.ID, "fixture-block")
	wantField(t, "spec.description", a.Spec.Description, "hi")
}

// A quoted scalar over two lines folds to "multi line", and a plain one folds the same only when its second line sits deeper than its key.
func TestParseManifest_V1AlphaReadsMultiLineScalarsAsYAMLDoes(t *testing.T) {
	const deep = "              "
	const col4 = "    "
	accepted := map[string]string{
		"single-quoted, second line deeper":      "'multi\n" + deep + "line'",
		"double-quoted, second line deeper":      "\"multi\n" + deep + "line\"",
		"plain, second line deeper":              "multi\n" + deep + "line",
		"single-quoted, second line at column 4": "'multi\n" + col4 + "line'",
		"double-quoted, second line at column 4": "\"multi\n" + col4 + "line\"",
	}
	read := map[string]func(t *testing.T, raw string) string{
		"spec.tags[0]": func(t *testing.T, raw string) string {
			a := parseAlpha(t, raw)
			requireLen(t, "spec.tags", a.Spec.Tags, 1)
			return a.Spec.Tags[0]
		},
		"spec.requires[0].verify.headers.h": func(t *testing.T, raw string) string {
			a := parseAlpha(t, raw)
			requireLen(t, "spec.requires", a.Spec.Requires, 1)
			requirePresent(t, "spec.requires[0].verify", a.Spec.Requires[0].Verify)
			return a.Spec.Requires[0].Verify.Headers["h"]
		},
		"spec.params[0].default": func(t *testing.T, raw string) string {
			a := parseAlpha(t, raw)
			requireLen(t, "spec.params", a.Spec.Params, 1)
			return a.Spec.Params[0].Default
		},
	}
	for path, readBack := range read {
		for label, value := range accepted {
			t.Run(label+" at "+path, func(t *testing.T) {
				wantField(t, path, readBack(t, alphaHead+scalarPositions[path](value)), "multi line")
			})
		}
		t.Run("plain, second line at column 4, at "+path, func(t *testing.T) {
			raw := alphaHead + scalarPositions[path]("multi\n"+col4+"line")
			if _, err := contract.ParseManifest([]byte(raw)); err == nil {
				t.Fatalf("ParseManifest accepted a plain scalar continued at column 4, which YAML refuses:\n%s", raw)
			}
		})
	}
}

// The platform's decoder reads only the first of several documents and drops the rest unread, so the kit refuses the manifest instead.
func TestParseManifest_V1AlphaRefusesASecondYAMLDocument(t *testing.T) {
	for label, raw := range map[string]string{
		"a second document with a key":           alphaHead + "---\nfoo: bar\n",
		"a second copy of the manifest":          alphaHead + "---\n" + alphaHead,
		"a second document after a document end": alphaHead + "...\n---\nfoo: bar\n",
	} {
		t.Run(label, func(t *testing.T) {
			requireRefusedNaming(t, []byte(raw), "more than one YAML document")
		})
	}
	for label, raw := range map[string]string{
		"a leading document marker": "---\n" + alphaHead,
		"a document end marker":     alphaHead + "...\n",
	} {
		t.Run("accepts "+label, func(t *testing.T) {
			wantField(t, "metadata.id", parseAlpha(t, raw).Metadata.ID, "fixture-block")
		})
	}
}

// The platform's decoder reads -.inf and -1e30 as the smallest int64, which the kit refuses.
func TestParseManifest_V1AlphaRefusesANonFiniteOrOutOfRangeFloatInAnIntegerField(t *testing.T) {
	for _, value := range []string{"-.inf", ".inf", ".nan", "-1e30", "1e30"} {
		t.Run(value, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+verifyHead+"        expectStatus: ["+value+"]\n"),
				"spec.requires[0].verify.expectStatus[0]")
		})
	}
}

// An alias or merge key brings in text from elsewhere in the document, so it is refused though the platform's decoder follows both.
func TestParseManifest_V1AlphaRefusesAnAliasOrAMergeKey(t *testing.T) {
	requireRefusedNaming(t, []byte(alphaHead+"  description: &d hello\n  releaseNote: *d\n"), "spec.releaseNote", "alias")
	requireRefusedNaming(t, []byte(alphaHead+"  lifecycle:\n    <<: {endpoint: https://fixture.example.test/hook}\n"),
		"spec.lifecycle", "merge key")
}

func TestParseManifest_V1AlphaDropsANullListItem(t *testing.T) {
	a := alphaSpec(t, "  outputs: [host, ~, port]\n  tags:\n    - fixture\n    -\n    - parser\n")
	wantField(t, "spec.outputs", a.Spec.Outputs, []string{"host", "port"})
	wantField(t, "spec.tags", a.Spec.Tags, []string{"fixture", "parser"})
	wantField(t, "V1AlphaManifest.Outputs", a.Outputs, []string{"host", "port"})
}

func TestParseManifest_V1AlphaKeepsAnEscapedSpaceBeforeALineBreak(t *testing.T) {
	a := alphaSpec(t, "  description: \"a\\ \n    b\"\n")
	wantField(t, "spec.description", a.Spec.Description, "a  b")
}

// A !!binary value is base64, which the kit's YAML subset does not decode, so the kit refuses the tag rather than read the base64 text as the value.
func TestParseManifest_V1AlphaRefusesATagOrDirectiveYAMLRefuses(t *testing.T) {
	for _, tc := range []struct {
		label, raw string
		wants      []string
	}{
		{"!!bool yes", alphaHead + "  optional: !!bool yes\n", []string{"spec.optional"}},
		{"!!int abc", alphaHead + "  description: !!int abc\n", []string{"spec.description"}},
		{"!!null x", alphaHead + "  description: !!null x\n", []string{"spec.description"}},
		{"a flow list ending in a bare tag", alphaHead + "  tags: [!!str 5, !!str]\n", []string{"spec.tags"}},
		{"a %YAML 1.2 directive", "%YAML 1.2\n---\n" + alphaHead, []string{"%YAML"}},
		{"!!binary", alphaHead + "  description: !!binary aGk=\n", []string{"spec.description", "!!binary"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, []byte(tc.raw), tc.wants...)
		})
	}
}

// The platform's decoder takes an empty document before the manifest as the whole manifest, so the kit refuses it as more than one document.
func TestParseManifest_V1AlphaRefusesAnEmptyDocumentBeforeTheManifest(t *testing.T) {
	requireRefusedNaming(t, []byte("---\n---\n"+alphaHead), "more than one YAML document")
}

func TestParseManifest_V1AlphaReadsADocumentStartingOnItsMarkerLine(t *testing.T) {
	a := parseAlpha(t, "--- {apiVersion: mitstack/v1alpha, kind: Block, metadata: {id: fixture-block, name: Fixture Block}, "+
		"spec: {version: 1.0.0, description: hi}}\n")
	wantField(t, "kind", a.Kind, "Block")
	wantField(t, "metadata.id", a.Metadata.ID, "fixture-block")
	wantField(t, "spec.description", a.Spec.Description, "hi")
}
