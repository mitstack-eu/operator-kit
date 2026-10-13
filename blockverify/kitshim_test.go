package main

// A legacy block binds to the kit through a kitshim.go identical to the kit's canonical, so no block's trust boundary drifts from the contract.

import (
	"strings"
	"testing"
)

const kitV1PathGoMod = "module fixture\n\ngo 1.25\n\nrequire github.com/mitstack-eu/operator-kit v1.32.0\n"

func TestTrustPlumbingSuite_LegacyShimMatchesTheCanonical(t *testing.T) {
	tests := []struct {
		name       string
		files      func(t *testing.T) map[string]string
		wantStatus string
		wantDetail string
	}{
		{
			name:       "a legacy block whose kitshim.go is the canonical passes",
			files:      noContractFixtureFiles,
			wantStatus: "pass",
			wantDetail: "kitshim.go matches the kit's canonical",
		},
		{
			name: "a legacy block whose kitshim.go drifts from the canonical fails, naming the file",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["kitshim.go"] = kitShimCanonical(t) + "\nfunc localHelper() {}\n"
				return files
			},
			wantStatus: "fail",
			wantDetail: "kitshim.go differs from the kit's canonical",
		},
		{
			name: "a legacy block on the kit's v1 module path is judged against the canonical with that path",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["go.mod"] = kitV1PathGoMod
				files["kitshim.go"] = strings.ReplaceAll(kitShimCanonical(t), "operator-kit/v2/", "operator-kit/")
				return files
			},
			wantStatus: "pass",
			wantDetail: "kitshim.go matches the kit's canonical",
		},
		{
			name: "a legacy block on the kit's v1 module path that carries the v2 shim fails",
			files: func(t *testing.T) map[string]string {
				files := noContractFixtureFiles(t)
				files["go.mod"] = kitV1PathGoMod
				return files
			},
			wantStatus: "fail",
			wantDetail: "kitshim.go differs from the kit's canonical",
		},
		{
			name: "a kit/v1 block's kitshim.go is not judged, because the kit owns its binding",
			files: func(t *testing.T) map[string]string {
				files := kitV1FixtureFiles(t)
				files["kitshim.go"] = "package main\n\nfunc localHelper() {}\n"
				return files
			},
			wantStatus: "pass",
			wantDetail: "kit/v1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newFixtureFrom(t, tc.files(t))

			got := trustPlumbingSuite(dir)

			if got.Status != tc.wantStatus {
				t.Fatalf("trustPlumbingSuite(%s).Status = %q; want %q; detail: %s", dir, got.Status, tc.wantStatus, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("trustPlumbingSuite(%s).Detail = %q; want it to contain %q", dir, got.Detail, tc.wantDetail)
			}
		})
	}
}
