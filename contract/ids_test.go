package contract_test

// Block, capability and resource-type ids are a bare community slug or @<org slug>/<name>, and a refusal names both forms.

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// slug40 and slug41 sit one inside and one past the 40-character limit.
var (
	slug40 = "a" + strings.Repeat("b", 38) + "c"
	slug41 = "a" + strings.Repeat("b", 39) + "c"
)

func TestParseNamespacedIDs_AcceptOnlyACommunityOrAPrivateID(t *testing.T) {
	parsers := map[string]func(id string) (namespace, name string, err error){
		"ParseBlockID": contract.ParseBlockID, "ParseCapabilityID": contract.ParseCapabilityID,
	}
	accepted := []struct{ id, ns, name string }{
		{"a", "", "a"}, {"0", "", "0"}, {"github", "", "github"}, {"app-scaffold", "", "app-scaffold"},
		{"a1-b2", "", "a1-b2"}, {"a--b", "", "a--b"}, {slug40, "", slug40},
		{"@acme/billing-db", "acme", "billing-db"}, {"@org-a/api", "org-a", "api"}, {"@a/b", "a", "b"}, {"@0/9", "0", "9"},
		{"@" + slug40 + "/" + slug40, slug40, slug40},
	}
	refused := map[string]string{
		"empty": "", "namespace with no name": "@acme/", "uppercase namespace": "@Acme/x", "empty namespace": "@/x",
		"namespace without its @": "acme/x", "two slashes": "@acme/x/y", "namespace with no slash": "@acme", "double @": "@@acme/x",
		"empty segment between slashes": "@acme//x", "@ inside the name": "@acme/@x", "bare slash": "/x", "uppercase bare": "Billing",
		"uppercase inside bare": "billing-DB", "leading dash": "-billing", "trailing dash": "billing-",
		"leading dash in namespace": "@-acme/x", "trailing dash in namespace": "@acme-/x", "leading dash in name": "@acme/-x",
		"trailing dash in name": "@acme/x-", "underscore": "billing_db", "dot": "billing.db", "41-character bare": slug41,
		"41-character namespace": "@" + slug41 + "/x", "41-character name": "@acme/" + slug41, "leading space": " billing",
		"trailing space": "billing ", "leading tab": "\tbilling", "trailing newline": "@acme/x\n", "space after @": "@ acme/x",
		"space before slash": "@acme /x", "non-ascii letter": "bïlling",
	}
	for fn, parse := range parsers {
		for _, tc := range accepted {
			t.Run(fn+"/accepts "+tc.id, func(t *testing.T) {
				ns, name, err := parse(tc.id)

				if err != nil || ns != tc.ns || name != tc.name {
					t.Errorf("%s(%q) = (%q, %q, %v), want (%q, %q, nil)", fn, tc.id, ns, name, err, tc.ns, tc.name)
				}
			})
		}
		for label, id := range refused {
			t.Run(fn+"/refuses "+label, func(t *testing.T) {
				ns, name, err := parse(id)

				if err == nil || ns != "" || name != "" {
					t.Errorf("%s(%q) = (%q, %q, %v), want an error and both empty", fn, id, ns, name, err)
				}
			})
		}
		// The error teaches the private form, so a reader who wrote acme/x or @Acme/x learns to write @<org>/<name>.
		for _, id := range []string{"acme/x", "@Acme/x", "@acme/", "Billing"} {
			t.Run(fn+"/names both forms refusing "+id, func(t *testing.T) {
				_, _, err := parse(id)

				requireErrorNaming(t, fn+"("+id+")", err, "community id <name>", "private id @<org>/<name>")
			})
		}
	}
}

func TestParseResourceTypeID_AcceptsOnlyACommunityOrAPrivateType(t *testing.T) {
	accepted := []struct{ id, ns, capability, name string }{
		{"aws.ebs-volume", "", "aws", "ebs-volume"}, {"app-host.postgres-database", "", "app-host", "postgres-database"},
		{"github.repository", "", "github", "repository"}, {"a.b", "", "a", "b"}, {"s3.v2-bucket", "", "s3", "v2-bucket"},
		{slug40 + ".x", "", slug40, "x"},
		{"@acme/aws.special-bucket", "acme", "aws", "special-bucket"}, {"@org-a/github.repository", "org-a", "github", "repository"},
		{"@a/b.c", "a", "b", "c"},
	}
	refused := map[string]string{
		"empty": "", "namespace with no type": "@acme/", "uppercase namespace": "@Acme/aws.x", "capability with no name": "aws",
		"empty name": "aws.", "empty capability": ".x", "private capability with no name": "@acme/aws", "uppercase name": "aws.Bucket",
		"two dots": "aws.a.b", "uppercase capability": "AWS.x", "leading dash in capability": "-aws.x",
		"trailing dash in capability": "aws-.x", "leading dash in name": "aws.-x", "trailing dash in name": "aws.x-",
		"double dash in name": "aws.x--y", "underscore in name": "aws.x_y", "private empty name": "@acme/aws.",
		"private empty capability": "@acme/.x", "empty namespace": "@/aws.x", "namespace without its @": "acme/aws.x",
		"slash after the name": "@acme/aws.x/y", "two namespaces": "@acme/x/aws.y", "41-character capability": slug41 + ".x",
		"41-character namespace": "@" + slug41 + "/aws.x", "leading space": " aws.x", "trailing space": "aws.x ",
		"trailing newline": "aws.x\n",
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.id, func(t *testing.T) {
			ns, capability, name, err := contract.ParseResourceTypeID(tc.id)

			if err != nil || ns != tc.ns || capability != tc.capability || name != tc.name {
				t.Errorf("ParseResourceTypeID(%q) = (%q, %q, %q, %v), want (%q, %q, %q, nil)", tc.id, ns, capability, name, err, tc.ns, tc.capability, tc.name)
			}
		})
	}
	for label, id := range refused {
		t.Run("refuses "+label, func(t *testing.T) {
			ns, capability, name, err := contract.ParseResourceTypeID(id)

			if err == nil || ns != "" || capability != "" || name != "" {
				t.Errorf("ParseResourceTypeID(%q) = (%q, %q, %q, %v), want an error and all empty", id, ns, capability, name, err)
			}
		})
	}
	for _, id := range []string{"aws", "@Acme/aws.x", "@acme/", "aws.Bucket"} {
		t.Run("names both forms refusing "+id, func(t *testing.T) {
			_, _, _, err := contract.ParseResourceTypeID(id)

			requireErrorNaming(t, "ParseResourceTypeID("+id+")", err, "community type <capability>.<name>", "private type @<org>/<capability>.<name>")
		})
	}
}
