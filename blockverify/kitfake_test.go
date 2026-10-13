package main

// A secret-leak or teardown test built over the kit's FakeResource proves nothing about the block, so the suite that runs it refuses it.

import (
	"strings"
	"testing"
)

const leakTestOverTheKitFakeGo = `package main

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestNoSecretLeakThroughTheFake(t *testing.T) {
	_ = &kittest.FakeResource{}
}
`

const leakTestOverAnAliasedKitFakeGo = `package main

import (
	"testing"

	kt "github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestNoSecretLeakThroughTheFake(t *testing.T) {
	_ = &kt.FakeResource{}
}
`

const kitFakeOutsideAnyMandatoryTestGo = `package main

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestTheKitFakeBuilds(t *testing.T) {
	_ = &kittest.FakeResource{}
}
`

const teardownTestOverTheKitFakeGo = `package main

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestUnsubscribeDeletesThroughTheFake(t *testing.T) {
	_ = &kittest.FakeResource{}
}
`

func TestSuites_RefuseAMandatoryTestBuiltOverTheKitFake(t *testing.T) {
	tests := []struct {
		name       string
		suite      func(target string) SuiteResult
		files      func(t *testing.T) map[string]string
		wantStatus string
		wantDetail string
	}{
		{
			name:  "a secret-leak test that builds kittest.FakeResource fails secret-safety, naming its file",
			suite: secretSafetySuite,
			files: func(t *testing.T) map[string]string {
				files := goldenFixtureFiles(t)
				files["secrets_fake_test.go"] = leakTestOverTheKitFakeGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "secrets_fake_test.go builds the kit's FakeResource",
		},
		{
			name:  "a secret-leak test that builds the fake through an aliased kittest import fails",
			suite: secretSafetySuite,
			files: func(t *testing.T) map[string]string {
				files := goldenFixtureFiles(t)
				files["secrets_fake_test.go"] = leakTestOverAnAliasedKitFakeGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "secrets_fake_test.go builds the kit's FakeResource",
		},
		{
			name:  "a secret-leak test that names FakeResource only in a comment passes",
			suite: secretSafetySuite,
			files: func(t *testing.T) map[string]string {
				files := goldenFixtureFiles(t)
				files["secrets_leak_test.go"] += "\n// The leak test never builds kittest.FakeResource, so it judges this block.\n"
				return files
			},
			wantStatus: "pass",
			wantDetail: "TestNoSecretLeak",
		},
		{
			name:  "a test file that declares no mandatory test may build the fake",
			suite: secretSafetySuite,
			files: func(t *testing.T) map[string]string {
				files := goldenFixtureFiles(t)
				files["fake_test.go"] = kitFakeOutsideAnyMandatoryTestGo
				return files
			},
			wantStatus: "pass",
			wantDetail: "TestNoSecretLeak",
		},
		{
			name:  "a teardown test that builds kittest.FakeResource fails teardown, naming its file",
			suite: teardownSuite,
			files: func(t *testing.T) map[string]string {
				files := goldenFixtureFiles(t)
				files["teardown_fake_test.go"] = teardownTestOverTheKitFakeGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "teardown_fake_test.go builds the kit's FakeResource",
		},
		{
			name:  "a kit/v1 block's teardown test is held to the same rule",
			suite: teardownSuite,
			files: func(t *testing.T) map[string]string {
				files := kitV1FixtureFiles(t)
				files["teardown_test.go"] = teardownTestOverTheKitFakeGo
				return files
			},
			wantStatus: "fail",
			wantDetail: "teardown_test.go builds the kit's FakeResource",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newFixtureFrom(t, tc.files(t))

			got := tc.suite(dir)

			if got.Status != tc.wantStatus {
				t.Fatalf("suite(%s).Status = %q; want %q; detail: %s", dir, got.Status, tc.wantStatus, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("suite(%s).Detail = %q; want it to contain %q", dir, got.Detail, tc.wantDetail)
			}
		})
	}
}
