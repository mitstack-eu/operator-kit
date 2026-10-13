package handler_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
)

const namingManifestHead = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: aws-app-infra
  name: AWS App Infra
spec:
  layer: infra
`

// The ECR push role name is 37 fixed characters around the app name, inside IAM's 64-character limit.
const awsAppInfraNaming = `  naming:
    appName:
      maxLength: 27
      derivedNames:
        - template: "mitstack-app-{appName}-github-actions-ecr-push"
          limit: 64
`

func wantAWSAppInfraNaming() *contract.AppNameNaming {
	return &contract.AppNameNaming{
		MaxLength: 27,
		DerivedNames: []contract.DerivedName{
			{Template: "mitstack-app-{appName}-github-actions-ecr-push", Limit: 64},
		},
	}
}

func TestManifestAppNameNaming_ReadsTheDeclaration(t *testing.T) {
	got, err := handler.ManifestAppNameNaming([]byte(namingManifestHead + awsAppInfraNaming))
	if err != nil {
		t.Fatalf("ManifestAppNameNaming: unexpected error: %v", err)
	}
	if want := wantAWSAppInfraNaming(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ManifestAppNameNaming = %+v, want %+v", got, want)
	}
}

func TestManifestAppNameNaming_ACommentLineDoesNotHideTheDeclaration(t *testing.T) {
	cases := map[string]string{
		"comment at column 0 between spec children": namingManifestHead + "# note\n" + awsAppInfraNaming,
		"over-indented comment as spec's first line": strings.Replace(namingManifestHead, "spec:\n", "spec:\n    # note\n", 1) +
			awsAppInfraNaming,
		"trailing comment on the spec line": strings.Replace(namingManifestHead, "spec:\n", "spec: # block spec\n", 1) +
			awsAppInfraNaming,
	}
	for label, manifest := range cases {
		t.Run(label, func(t *testing.T) {
			got, err := handler.ManifestAppNameNaming([]byte(manifest))
			if err != nil {
				t.Fatalf("ManifestAppNameNaming: unexpected error: %v", err)
			}
			if want := wantAWSAppInfraNaming(); !reflect.DeepEqual(got, want) {
				t.Fatalf("ManifestAppNameNaming = %+v, want %+v\nmanifest:\n%s", got, want, manifest)
			}
		})
	}
}

func TestManifestAppNameNaming_ADerivedNameMissingAKeyIsAnError(t *testing.T) {
	cases := map[string]string{
		"limit":    strings.Replace(awsAppInfraNaming, "          limit: 64\n", "", 1),
		"template": strings.Replace(awsAppInfraNaming, "- template: \"mitstack-app-{appName}-github-actions-ecr-push\"\n          limit: 64", "- limit: 64", 1),
	}
	for missing, spec := range cases {
		t.Run("missing "+missing, func(t *testing.T) {
			got, err := handler.ManifestAppNameNaming([]byte(namingManifestHead + spec))
			if err == nil {
				t.Fatalf("ManifestAppNameNaming = %+v, nil error; want an error for a derivedNames item with no %s\nspec:\n%s", got, missing, spec)
			}
			for _, want := range []string{contract.AppNameNamingField, missing} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestManifestAppNameNaming_AbsentDeclaresNothing(t *testing.T) {
	cases := map[string]string{
		"no spec.naming":    namingManifestHead,
		"a v1beta manifest": string(plainV1Beta(t)),
	}
	for label, manifest := range cases {
		t.Run(label, func(t *testing.T) {
			got, err := handler.ManifestAppNameNaming([]byte(manifest))
			if err != nil {
				t.Fatalf("ManifestAppNameNaming: unexpected error on a manifest with no %s: %v", contract.AppNameNamingField, err)
			}
			if got != nil {
				t.Fatalf("ManifestAppNameNaming = %+v, want nil when no %s is declared", got, contract.AppNameNamingField)
			}
		})
	}
}

func TestManifestAppNameNaming_AnUnreadableValueIsAnError(t *testing.T) {
	cases := map[string]string{
		"maxLength is not an integer":    strings.Replace(awsAppInfraNaming, "maxLength: 27", "maxLength: twenty-seven", 1),
		"limit is not an integer":        strings.Replace(awsAppInfraNaming, "limit: 64", "limit: sixty-four", 1),
		"appName carries an unknown key": "  naming:\n    appName:\n      maxLength: 27\n      bogus: 1\n",
	}
	for label, spec := range cases {
		t.Run(label, func(t *testing.T) {
			got, err := handler.ManifestAppNameNaming([]byte(namingManifestHead + spec))
			if err == nil {
				t.Fatalf("ManifestAppNameNaming = %+v, nil error; want an error, since a declaration nobody can read must not be mistaken for an absent one", got)
			}
			if !strings.Contains(err.Error(), contract.AppNameNamingField) {
				t.Errorf("error = %q, want it to name %s", err, contract.AppNameNamingField)
			}
		})
	}
}

// A refused manifest fails the naming suite rather than read as declaring no limit.
func TestManifestAppNameNaming_AManifestTheParserRefusesIsAnErrorCarryingItsMessage(t *testing.T) {
	cases := []struct {
		label, path, manifest string
	}{
		{"an empty manifest", "apiVersion", ""},
		{"naming with no appName", "spec.naming.somethingElse",
			namingManifestHead + "  naming:\n    somethingElse:\n      maxLength: 10\n"},
		{"an unknown spec field beside the declaration", "spec.bogus",
			namingManifestHead + "  bogus: 1\n" + awsAppInfraNaming},
		{"spec.naming in a v1beta manifest", "spec.naming",
			strings.Replace(string(plainV1Beta(t)), "  version: 3.0.0\n", "  version: 3.0.0\n"+awsAppInfraNaming, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, perr := contract.ParseManifest([]byte(tc.manifest))
			if perr == nil {
				t.Fatalf("contract.ParseManifest accepts the manifest, which must be refused naming %s:\n%s", tc.path, tc.manifest)
			}
			if !strings.Contains(perr.Error(), tc.path) {
				t.Errorf("contract.ParseManifest error = %q, want it to name %s", perr, tc.path)
			}
			got, err := handler.ManifestAppNameNaming([]byte(tc.manifest))
			if err == nil {
				t.Fatalf("ManifestAppNameNaming = %+v, nil error, for a manifest contract.ParseManifest refuses (%v)", got, perr)
			}
			if !strings.Contains(err.Error(), perr.Error()) {
				t.Errorf("ManifestAppNameNaming error = %q, want it to carry the parser's message %q", err, perr)
			}
		})
	}
}
