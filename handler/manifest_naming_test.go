// ManifestAppNameNaming reads spec.naming.appName as the parser does, and a declaration nobody can read is an error rather than an absent one.
package handler_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
)

const namingManifestHead = "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  id: aws-app-infra\n  name: AWS App Infra\nspec:\n  layer: infra\n"

// The ECR push role name is 37 fixed characters around the app name, inside IAM's 64-character limit.
const awsAppInfraNaming = "  naming:\n    appName:\n      maxLength: 27\n      derivedNames:\n        - template: \"mitstack-app-{appName}-github-actions-ecr-push\"\n          limit: 64\n"

func TestManifestAppNameNaming(t *testing.T) {
	declared := &contract.AppNameNaming{MaxLength: 27, DerivedNames: []contract.DerivedName{{Template: "mitstack-app-{appName}-github-actions-ecr-push", Limit: 64}}}
	onSpecLine := func(replacement string) string { return strings.Replace(namingManifestHead, "spec:\n", replacement, 1) }
	field := contract.AppNameNamingField
	cases := []struct {
		name     string
		manifest string
		want     *contract.AppNameNaming
		naming   []string
		parser   string
	}{
		{name: "the declaration is read", manifest: namingManifestHead + awsAppInfraNaming, want: declared},
		{name: "a comment at column 0 between spec children does not hide it", manifest: namingManifestHead + "# note\n" + awsAppInfraNaming, want: declared},
		{name: "an over-indented comment as spec's first line does not hide it", manifest: onSpecLine("spec:\n    # note\n") + awsAppInfraNaming, want: declared},
		{name: "a trailing comment on the spec line does not hide it", manifest: onSpecLine("spec: # block spec\n") + awsAppInfraNaming, want: declared},
		{name: "no spec.naming declares nothing", manifest: namingManifestHead},
		{name: "a v1beta manifest declares nothing", manifest: string(plainV1Beta(t))},
		{name: "a derived name missing its limit is an error", manifest: namingManifestHead + strings.Replace(awsAppInfraNaming, "          limit: 64\n", "", 1), naming: []string{field, "limit"}},
		{name: "a derived name missing its template is an error", manifest: namingManifestHead + strings.Replace(awsAppInfraNaming,
			"- template: \"mitstack-app-{appName}-github-actions-ecr-push\"\n          limit: 64", "- limit: 64", 1), naming: []string{field, "template"}},
		{name: "a maxLength that is not an integer is an error", manifest: namingManifestHead + strings.Replace(awsAppInfraNaming, "maxLength: 27", "maxLength: twenty-seven", 1), naming: []string{field}},
		{name: "a limit that is not an integer is an error", manifest: namingManifestHead + strings.Replace(awsAppInfraNaming, "limit: 64", "limit: sixty-four", 1), naming: []string{field}},
		{name: "an unknown key under appName is an error", manifest: namingManifestHead + "  naming:\n    appName:\n      maxLength: 27\n      bogus: 1\n", naming: []string{field}},
		{name: "an empty manifest carries the parser's refusal", manifest: "", parser: "apiVersion"},
		{name: "naming with no appName carries the parser's refusal", manifest: namingManifestHead + "  naming:\n    somethingElse:\n      maxLength: 10\n", parser: "spec.naming.somethingElse"},
		{name: "an unknown spec field beside the declaration carries the parser's refusal", manifest: namingManifestHead + "  bogus: 1\n" + awsAppInfraNaming, parser: "spec.bogus"},
		{name: "spec.naming in a v1beta manifest carries the parser's refusal", manifest: strings.Replace(string(plainV1Beta(t)), "  version: 3.0.0\n", "  version: 3.0.0\n"+awsAppInfraNaming, 1), parser: "spec.naming"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handler.ManifestAppNameNaming([]byte(tc.manifest))

			if refused := tc.naming != nil || tc.parser != ""; (err != nil) != refused {
				t.Fatalf("ManifestAppNameNaming = %+v, %v; want an error %v\nmanifest:\n%s", got, err, refused, tc.manifest)
			}
			if err == nil && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ManifestAppNameNaming = %+v, want %+v\nmanifest:\n%s", got, tc.want, tc.manifest)
			}
			for _, want := range tc.naming {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to name %q", err, want)
				}
			}
			if tc.parser != "" {
				wantParserRefusal(t, []byte(tc.manifest), tc.parser, err)
			}
		})
	}
}
