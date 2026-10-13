package contract_test

// A troubleshooting rule accepts exactly signature, title, matches, fixes and doc, and refuses any other key naming its path.

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// troubleshootingKeys are the keys a troubleshooting rule accepts, in the
// order its type declares them.
var troubleshootingKeys = []string{"signature", "title", "matches", "fixes", "doc"}

var troubleshootingFields = []string{"Signature", "Title", "Matches", "Fixes", "Doc"}

const twoTroubleshootingRules = `  troubleshooting:
    - signature: quota-exceeded
      title: The account is out of quota
      matches:
        - QuotaExceeded
        - limit reached
      fixes:
        - Raise the quota in the provider's console.
        - Retry the subscribe.
      doc: start.md
    - signature: bad-token
      title: The token was refused
      matches: [401 Unauthorized]
      fixes: [Reconnect the account.]
`

func TestParseManifest_V1AlphaReadsATroubleshootingRulesFixesUnderFixes(t *testing.T) {
	a := alphaSpec(t, twoTroubleshootingRules)
	requireLen(t, "spec.troubleshooting", a.Spec.Troubleshooting, 2)
	r := a.Spec.Troubleshooting[0]
	wantField(t, "spec.troubleshooting[0].signature", r.Signature, "quota-exceeded")
	wantField(t, "spec.troubleshooting[0].title", r.Title, "The account is out of quota")
	wantField(t, "spec.troubleshooting[0].matches", r.Matches, []string{"QuotaExceeded", "limit reached"})
	wantField(t, "spec.troubleshooting[0].fixes", r.Fixes, []string{"Raise the quota in the provider's console.", "Retry the subscribe."})
	wantField(t, "spec.troubleshooting[0].doc", r.Doc, "start.md")

	second := a.Spec.Troubleshooting[1]
	wantField(t, "spec.troubleshooting[1].signature", second.Signature, "bad-token")
	wantField(t, "spec.troubleshooting[1].matches", second.Matches, []string{"401 Unauthorized"})
	wantField(t, "spec.troubleshooting[1].fixes", second.Fixes, []string{"Reconnect the account."})
	wantField(t, "spec.troubleshooting[1].doc", second.Doc, "")
}

// The rule's type holds exactly the five fields that carry its keys.
func TestParseManifest_V1AlphaTroubleshootingRuleAcceptsExactlyItsFiveKeys(t *testing.T) {
	values := map[string]string{
		"signature": "  troubleshooting:\n    - signature: quota-exceeded\n",
		"title":     "  troubleshooting:\n    - title: The account is out of quota\n",
		"matches":   "  troubleshooting:\n    - matches: [QuotaExceeded]\n",
		"fixes":     "  troubleshooting:\n    - fixes: [Raise the quota.]\n",
		"doc":       "  troubleshooting:\n    - doc: start.md\n",
	}
	read := map[string]func(contract.V1AlphaManifest) any{
		"signature": func(a contract.V1AlphaManifest) any { return a.Spec.Troubleshooting[0].Signature },
		"title":     func(a contract.V1AlphaManifest) any { return a.Spec.Troubleshooting[0].Title },
		"matches":   func(a contract.V1AlphaManifest) any { return a.Spec.Troubleshooting[0].Matches },
		"fixes":     func(a contract.V1AlphaManifest) any { return a.Spec.Troubleshooting[0].Fixes },
		"doc":       func(a contract.V1AlphaManifest) any { return a.Spec.Troubleshooting[0].Doc },
	}
	want := map[string]any{
		"signature": "quota-exceeded",
		"title":     "The account is out of quota",
		"matches":   []string{"QuotaExceeded"},
		"fixes":     []string{"Raise the quota."},
		"doc":       "start.md",
	}
	if len(values) != len(troubleshootingKeys) {
		t.Fatalf("the table covers %d keys, want the %d of %v", len(values), len(troubleshootingKeys), troubleshootingKeys)
	}
	for _, key := range troubleshootingKeys {
		t.Run(key, func(t *testing.T) {
			a := alphaSpec(t, values[key])
			requireLen(t, "spec.troubleshooting", a.Spec.Troubleshooting, 1)
			wantField(t, "spec.troubleshooting[0]."+key, read[key](*a), want[key])
		})
	}

	a := alphaSpec(t, twoTroubleshootingRules)
	requireLen(t, "spec.troubleshooting", a.Spec.Troubleshooting, 2)
	rule := reflect.TypeOf(a.Spec.Troubleshooting[0])
	var fields []string
	for i := 0; i < rule.NumField(); i++ {
		fields = append(fields, rule.Field(i).Name)
	}
	if !reflect.DeepEqual(fields, troubleshootingFields) {
		t.Errorf("a troubleshooting rule (%s) has the fields %v, want exactly %v, one per accepted key",
			rule, fields, troubleshootingFields)
	}
}

func TestParseManifest_V1AlphaRefusesAnyOtherTroubleshootingKeyNamingItsPath(t *testing.T) {
	others := []string{
		"bogus", "fix", "fixList", "remedies", "actions", "resolution", "hint",
		"description", "name", "kind", "docs", "match", "Fixes", "FIXES", "Signature",
	}
	for _, key := range others {
		for i := 0; i < 2; i++ {
			t.Run(fmt.Sprintf("%s at index %d", key, i), func(t *testing.T) {
				rules := "  troubleshooting:\n"
				for j := 0; j <= i; j++ {
					rules += fmt.Sprintf("    - signature: rule-%d\n      fixes: [Retry.]\n", j)
				}
				rules += "      " + key + ": [Retry.]\n"
				requireRefusedNaming(t, []byte(alphaHead+rules), fmt.Sprintf("spec.troubleshooting[%d].%s", i, key))
			})
		}
	}
}
