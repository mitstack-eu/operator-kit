package contract_test

// manifest_v1alpha_syntax_test.go pins that contract.ParseManifest reads a
// v1alpha manifest's YAML as the platform's decoder reads it: what that
// decoder refuses as YAML syntax or as a type is refused here, naming the
// path, and what it accepts is read to the value it decodes. Three
// refusals are the kit's own and are kept on purpose: a second YAML
// document, a float in an integer field that has no integer value, and an
// alias or a merge key.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// verifyHead is a spec.requires entry with a verify probe, ready for one
// more verify key at eight spaces.
const verifyHead = "  requires:\n    - capability: github\n      verify:\n        urlTemplate: https://api.example.test/user\n"

// scalarPositions writes one scalar value, given as the text after the key
// or item marker, into a string field, a list item, a map value and a
// struct field inside a list item, by the path each is named at.
var scalarPositions = map[string]func(value string) string{
	"spec.description":                  func(v string) string { return "  description: " + v + "\n" },
	"spec.tags[0]":                      func(v string) string { return "  tags:\n    - " + v + "\n" },
	"spec.requires[0].verify.headers.h": func(v string) string { return verifyHead + "        headers:\n          h: " + v + "\n" },
	"spec.params[0].default":            func(v string) string { return "  params:\n    - name: image\n      default: " + v + "\n" },
}

// What the platform's decoder refuses as YAML is refused, naming the path:
// a plain value holding ": ", a value starting with %, a ? complex key
// where a value belongs, and a bare - as a mapping value.
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

// A tag is read as the platform's decoder reads it: !!str and !!int give a
// string field their text, !!int gives an integer field its number, and
// !!str into an integer, a list or a bool is refused, naming the path.
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

// A UTF-8 byte order mark before the document is not part of it.
func TestParseManifest_V1AlphaAcceptsALeadingByteOrderMark(t *testing.T) {
	a := parseAlpha(t, "\xef\xbb\xbf"+alphaHead+"  description: hi\n")
	wantField(t, "kind", a.Kind, "Block")
	wantField(t, "metadata.id", a.Metadata.ID, "fixture-block")
	wantField(t, "spec.description", a.Spec.Description, "hi")
}

// A scalar written over two lines is read as the platform's decoder reads
// it, in a list item, a map value and a param's default: a single- or
// double-quoted one folds to "multi line" whether its second line sits
// deeper than its key or back at column 4, and a plain one folds the same
// when its second line sits deeper than its key and is refused when it
// does not.
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

// A manifest is one YAML document. The platform's decoder reads only the
// first of several and drops the rest unread; the kit refuses the manifest
// instead, saying why.
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

// A float with no integer value in range is refused in an integer field,
// naming the path, though the platform's decoder reads -.inf and -1e30 as
// the smallest int64.
func TestParseManifest_V1AlphaRefusesANonFiniteOrOutOfRangeFloatInAnIntegerField(t *testing.T) {
	for _, value := range []string{"-.inf", ".inf", ".nan", "-1e30", "1e30"} {
		t.Run(value, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+verifyHead+"        expectStatus: ["+value+"]\n"),
				"spec.requires[0].verify.expectStatus[0]")
		})
	}
}

// An alias or a merge key brings in text from elsewhere in the document, so
// a v1alpha manifest carrying one is refused, as a v1beta one is, though
// the platform's decoder follows both.
func TestParseManifest_V1AlphaRefusesAnAliasOrAMergeKey(t *testing.T) {
	requireRefusedNaming(t, []byte(alphaHead+"  description: &d hello\n  releaseNote: *d\n"), "spec.releaseNote", "alias")
	requireRefusedNaming(t, []byte(alphaHead+"  lifecycle:\n    <<: {endpoint: https://fixture.example.test/hook}\n"),
		"spec.lifecycle", "merge key")
}

// A null list item is dropped, as the platform's decoder drops it, in a flow
// list and a block list alike.
func TestParseManifest_V1AlphaDropsANullListItem(t *testing.T) {
	a := alphaSpec(t, "  outputs: [host, ~, port]\n  tags:\n    - fixture\n    -\n    - parser\n")
	wantField(t, "spec.outputs", a.Spec.Outputs, []string{"host", "port"})
	wantField(t, "spec.tags", a.Spec.Tags, []string{"fixture", "parser"})
	wantField(t, "V1AlphaManifest.Outputs", a.Outputs, []string{"host", "port"})
}

// An escaped space before a line break in a double-quoted scalar is kept,
// and the break then folds to a space, as the platform's decoder reads it.
func TestParseManifest_V1AlphaKeepsAnEscapedSpaceBeforeALineBreak(t *testing.T) {
	a := alphaSpec(t, "  description: \"a\\ \n    b\"\n")
	wantField(t, "spec.description", a.Spec.Description, "a  b")
}

// A core tag whose text does not resolve to it is refused, naming the path,
// as the platform's decoder refuses it: !!bool yes, !!int abc and !!null x.
// A flow list ending in a bare tag and a %YAML 1.2 directive are refused
// too. A !!binary value is base64, which the kit's YAML subset does not
// decode, so the kit refuses the tag rather than read the base64 text as
// the value.
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

// A document marker followed by a second one opens an empty document before
// the manifest, which the platform's decoder takes as the whole manifest;
// the kit refuses it as more than one YAML document.
func TestParseManifest_V1AlphaRefusesAnEmptyDocumentBeforeTheManifest(t *testing.T) {
	requireRefusedNaming(t, []byte("---\n---\n"+alphaHead), "more than one YAML document")
}

// A document may start on its marker line, as a flow mapping after ---.
func TestParseManifest_V1AlphaReadsADocumentStartingOnItsMarkerLine(t *testing.T) {
	a := parseAlpha(t, "--- {apiVersion: mitstack/v1alpha, kind: Block, metadata: {id: fixture-block, name: Fixture Block}, "+
		"spec: {version: 1.0.0, description: hi}}\n")
	wantField(t, "kind", a.Kind, "Block")
	wantField(t, "metadata.id", a.Metadata.ID, "fixture-block")
	wantField(t, "spec.description", a.Spec.Description, "hi")
}
