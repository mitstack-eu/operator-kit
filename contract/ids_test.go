package contract_test

// ids_test.go pins the grammar of block, capability and resource-type ids.
// A community id is bare; a private id sits under its org's namespace as
// @<org slug>/<name>.
//
//   - a slug is 1 to 40 characters of a-z, 0-9 and dashes, starting and
//     ending with a letter or digit: the platform's
//     ^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$;
//   - ParseBlockID and ParseCapabilityID accept a bare slug, giving namespace
//     "", and @<org slug>/<slug>, giving the org slug without its @, and
//     refuse anything else with an error that names both forms;
//   - ParseResourceTypeID accepts [@<org slug>/]<capability slug>.<name>,
//     the name in lowercase kebab case;
//   - every catalogue entry is a community type of its own capability.

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// parseNameFunc is the shape ParseBlockID and ParseCapabilityID share.
type parseNameFunc func(id string) (namespace, name string, err error)

// slug40 and slug41 are slugs one inside and one past the 40-character
// limit.
var (
	slug40 = "a" + strings.Repeat("b", 38) + "c"
	slug41 = "a" + strings.Repeat("b", 39) + "c"
)

// refusedNames are ids neither form allows.
var refusedNames = map[string]string{
	"empty":                         "",
	"namespace with no name":        "@acme/",
	"uppercase namespace":           "@Acme/x",
	"empty namespace":               "@/x",
	"namespace without its @":       "acme/x",
	"two slashes":                   "@acme/x/y",
	"namespace with no slash":       "@acme",
	"double @":                      "@@acme/x",
	"empty segment between slashes": "@acme//x",
	"@ inside the name":             "@acme/@x",
	"bare slash":                    "/x",
	"uppercase bare":                "Billing",
	"uppercase inside bare":         "billing-DB",
	"leading dash":                  "-billing",
	"trailing dash":                 "billing-",
	"leading dash in namespace":     "@-acme/x",
	"trailing dash in namespace":    "@acme-/x",
	"leading dash in name":          "@acme/-x",
	"trailing dash in name":         "@acme/x-",
	"underscore":                    "billing_db",
	"dot":                           "billing.db",
	"41-character bare":             slug41,
	"41-character namespace":        "@" + slug41 + "/x",
	"41-character name":             "@acme/" + slug41,
	"leading space":                 " billing",
	"trailing space":                "billing ",
	"leading tab":                   "\tbilling",
	"trailing newline":              "@acme/x\n",
	"space after @":                 "@ acme/x",
	"space before slash":            "@acme /x",
	"non-ascii letter":              "bïlling",
}

func requireAcceptsBare(t *testing.T, fn string, parse parseNameFunc) {
	t.Helper()
	for _, id := range []string{"a", "0", "github", "app-scaffold", "a1-b2", "a--b", slug40} {
		ns, name, err := parse(id)
		if err != nil {
			t.Errorf("%s(%q) error = %v, want a bare community id accepted", fn, id, err)
			continue
		}
		if ns != "" || name != id {
			t.Errorf("%s(%q) = (%q, %q), want (%q, %q)", fn, id, ns, name, "", id)
		}
	}
}

func requireAcceptsNamespaced(t *testing.T, fn string, parse parseNameFunc) {
	t.Helper()
	cases := []struct{ id, ns, name string }{
		{"@acme/billing-db", "acme", "billing-db"},
		{"@org-a/api", "org-a", "api"},
		{"@a/b", "a", "b"},
		{"@0/9", "0", "9"},
		{"@" + slug40 + "/" + slug40, slug40, slug40},
	}
	for _, tc := range cases {
		ns, name, err := parse(tc.id)
		if err != nil {
			t.Errorf("%s(%q) error = %v, want a private id accepted", fn, tc.id, err)
			continue
		}
		if ns != tc.ns || name != tc.name {
			t.Errorf("%s(%q) = (%q, %q), want (%q, %q)", fn, tc.id, ns, name, tc.ns, tc.name)
		}
	}
}

func requireRefusesOtherForms(t *testing.T, fn string, parse parseNameFunc) {
	t.Helper()
	for label, id := range refusedNames {
		ns, name, err := parse(id)
		if err == nil {
			t.Errorf("%s: %s %q accepted as (%q, %q), want an error", fn, label, id, ns, name)
			continue
		}
		if ns != "" || name != "" {
			t.Errorf("%s: %s %q refused but returned (%q, %q), want both empty", fn, label, id, ns, name)
		}
	}
}

// The error teaches the private form, so a reader who wrote acme/x or
// @Acme/x learns to write @<org>/<name>.
func requireErrorNamesBothForms(t *testing.T, fn string, parse parseNameFunc) {
	t.Helper()
	for _, id := range []string{"acme/x", "@Acme/x", "@acme/", "Billing"} {
		_, _, err := parse(id)
		if err == nil {
			t.Errorf("%s(%q) accepted, want an error naming both forms", fn, id)
			continue
		}
		for _, form := range []string{"community id <name>", "private id @<org>/<name>"} {
			if !strings.Contains(err.Error(), form) {
				t.Errorf("%s(%q) error = %q, want it to name the form %q", fn, id, err, form)
			}
		}
	}
}

// A refused resource-type id names both allowed forms too.
func TestParseResourceTypeID_ErrorNamesBothForms(t *testing.T) {
	for _, id := range []string{"aws", "@Acme/aws.x", "@acme/", "aws.Bucket"} {
		_, _, _, err := contract.ParseResourceTypeID(id)
		if err == nil {
			t.Errorf("ParseResourceTypeID(%q) accepted, want an error naming both forms", id)
			continue
		}
		for _, form := range []string{"community type <capability>.<name>", "private type @<org>/<capability>.<name>"} {
			if !strings.Contains(err.Error(), form) {
				t.Errorf("ParseResourceTypeID(%q) error = %q, want it to name the form %q", id, err, form)
			}
		}
	}
}

func TestParseBlockID_AcceptsABareCommunitySlug(t *testing.T) {
	requireAcceptsBare(t, "ParseBlockID", contract.ParseBlockID)
}

func TestParseBlockID_AcceptsAnOrgNamespacedName(t *testing.T) {
	requireAcceptsNamespaced(t, "ParseBlockID", contract.ParseBlockID)
}

func TestParseBlockID_RefusesEveryOtherForm(t *testing.T) {
	requireRefusesOtherForms(t, "ParseBlockID", contract.ParseBlockID)
}

func TestParseBlockID_ErrorNamesBothForms(t *testing.T) {
	requireErrorNamesBothForms(t, "ParseBlockID", contract.ParseBlockID)
}

func TestParseCapabilityID_AcceptsABareCommunitySlug(t *testing.T) {
	requireAcceptsBare(t, "ParseCapabilityID", contract.ParseCapabilityID)
}

func TestParseCapabilityID_AcceptsAnOrgNamespacedName(t *testing.T) {
	requireAcceptsNamespaced(t, "ParseCapabilityID", contract.ParseCapabilityID)
}

func TestParseCapabilityID_RefusesEveryOtherForm(t *testing.T) {
	requireRefusesOtherForms(t, "ParseCapabilityID", contract.ParseCapabilityID)
}

func TestParseCapabilityID_ErrorNamesBothForms(t *testing.T) {
	requireErrorNamesBothForms(t, "ParseCapabilityID", contract.ParseCapabilityID)
}

func TestParseResourceTypeID_AcceptsACommunityType(t *testing.T) {
	cases := []struct{ id, capability, name string }{
		{"aws.ebs-volume", "aws", "ebs-volume"},
		{"app-host.postgres-database", "app-host", "postgres-database"},
		{"github.repository", "github", "repository"},
		{"a.b", "a", "b"},
		{"s3.v2-bucket", "s3", "v2-bucket"},
		{slug40 + ".x", slug40, "x"},
	}
	for _, tc := range cases {
		ns, capability, name, err := contract.ParseResourceTypeID(tc.id)
		if err != nil {
			t.Errorf("ParseResourceTypeID(%q) error = %v, want a community type accepted", tc.id, err)
			continue
		}
		if ns != "" || capability != tc.capability || name != tc.name {
			t.Errorf("ParseResourceTypeID(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.id, ns, capability, name, "", tc.capability, tc.name)
		}
	}
}

func TestParseResourceTypeID_AcceptsAPrivateTypeUnderItsOrg(t *testing.T) {
	cases := []struct{ id, ns, capability, name string }{
		{"@acme/aws.special-bucket", "acme", "aws", "special-bucket"},
		{"@org-a/github.repository", "org-a", "github", "repository"},
		{"@a/b.c", "a", "b", "c"},
	}
	for _, tc := range cases {
		ns, capability, name, err := contract.ParseResourceTypeID(tc.id)
		if err != nil {
			t.Errorf("ParseResourceTypeID(%q) error = %v, want a private type accepted", tc.id, err)
			continue
		}
		if ns != tc.ns || capability != tc.capability || name != tc.name {
			t.Errorf("ParseResourceTypeID(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.id, ns, capability, name, tc.ns, tc.capability, tc.name)
		}
	}
}

func TestParseResourceTypeID_RefusesEveryOtherForm(t *testing.T) {
	refused := map[string]string{
		"empty":                           "",
		"namespace with no type":          "@acme/",
		"uppercase namespace":             "@Acme/aws.x",
		"capability with no name":         "aws",
		"empty name":                      "aws.",
		"empty capability":                ".x",
		"private capability with no name": "@acme/aws",
		"uppercase name":                  "aws.Bucket",
		"two dots":                        "aws.a.b",
		"uppercase capability":            "AWS.x",
		"leading dash in capability":      "-aws.x",
		"trailing dash in capability":     "aws-.x",
		"leading dash in name":            "aws.-x",
		"trailing dash in name":           "aws.x-",
		"double dash in name":             "aws.x--y",
		"underscore in name":              "aws.x_y",
		"private empty name":              "@acme/aws.",
		"private empty capability":        "@acme/.x",
		"empty namespace":                 "@/aws.x",
		"namespace without its @":         "acme/aws.x",
		"slash after the name":            "@acme/aws.x/y",
		"two namespaces":                  "@acme/x/aws.y",
		"41-character capability":         slug41 + ".x",
		"41-character namespace":          "@" + slug41 + "/aws.x",
		"leading space":                   " aws.x",
		"trailing space":                  "aws.x ",
		"trailing newline":                "aws.x\n",
	}
	for label, id := range refused {
		ns, capability, name, err := contract.ParseResourceTypeID(id)
		if err == nil {
			t.Errorf("%s %q accepted as (%q, %q, %q), want an error", label, id, ns, capability, name)
			continue
		}
		if ns != "" || capability != "" || name != "" {
			t.Errorf("%s %q refused but returned (%q, %q, %q), want all empty", label, id, ns, capability, name)
		}
	}
}
