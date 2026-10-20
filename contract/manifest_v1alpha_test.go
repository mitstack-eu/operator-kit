package contract_test

// contract.ParseManifest reads every field the platform's manifest decoder reads under mitstack/v1alpha, as YAML reads it, and the kit's own spec.naming.appName.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// alphaHead is a complete minimal v1alpha manifest ending inside spec, to which a test appends its spec lines.
const alphaHead = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: fixture-block
  name: Fixture Block
  owner: fixture-org
spec:
  version: 1.0.0
`

const verifyHead = "  requires:\n    - capability: github\n      verify:\n        urlTemplate: https://api.example.test/user\n"

// scalarPositions writes one scalar value at each kind of position a v1alpha manifest holds one.
var scalarPositions = map[string]func(value string) string{
	"spec.description":                  func(v string) string { return "  description: " + v + "\n" },
	"spec.tags[0]":                      func(v string) string { return "  tags:\n    - " + v + "\n" },
	"spec.requires[0].verify.headers.h": func(v string) string { return verifyHead + "        headers:\n          h: " + v + "\n" },
	"spec.params[0].default":            func(v string) string { return "  params:\n    - name: image\n      default: " + v + "\n" },
}

func indent(lines string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for _, line := range strings.SplitAfter(lines, "\n") {
		if line != "" {
			b.WriteString(pad + line)
		}
	}
	return b.String()
}

type alphaRead struct {
	label string
	raw   string
	got   func(a *contract.V1AlphaManifest) any
	want  any
}

func hooksOf(a *contract.V1AlphaManifest) any {
	if a.Spec.Lifecycle == nil {
		return "no lifecycle"
	}
	if len(a.Spec.Lifecycle.Hooks) == 0 {
		return []string(nil)
	}
	return a.Spec.Lifecycle.Hooks
}

func sectionReads() []alphaRead {
	s := func(lines string) string { return alphaHead + lines }
	lifecycle := func(a *contract.V1AlphaManifest) any { return a.Spec.Lifecycle }
	return []alphaRead{
		{"kind", alphaHead, func(a *contract.V1AlphaManifest) any { return a.Kind }, "Block"},
		{"metadata", alphaHead, func(a *contract.V1AlphaManifest) any { return a.Metadata },
			contract.V1AlphaMetadata{ID: "fixture-block", Name: "Fixture Block", Owner: "fixture-org"}},
		{"the manifest's ID beside metadata.id", alphaHead, func(a *contract.V1AlphaManifest) any { return a.ID }, "fixture-block"},
		{"the spec scalars and lists", s(`  layer: backend
  description: A fixture block for the parser.
  optional: true
  tags: [fixture, parser]
  provides:
    - fixture-capability
    - fixture-extra
  outputs: [host, port]
  upgradeStrategy: in-place
  releaseClass: correctness-fix
  releaseNote: Fixes the fixture.
  receivesAppSecrets: true
`), func(a *contract.V1AlphaManifest) any { return a.Spec }, contract.V1AlphaSpec{Layer: "backend", Version: "1.0.0", Description: "A fixture block for the parser.", Optional: true,
			Tags: []string{"fixture", "parser"}, Provides: []string{"fixture-capability", "fixture-extra"}, Outputs: []string{"host", "port"},
			UpgradeStrategy: "in-place", ReleaseClass: "correctness-fix", ReleaseNote: "Fixes the fixture.", ReceivesAppSecrets: true}},
		{"each resource's name, description and produces", s(`  resources:
    - name: Database
      description: The app's Postgres database.
      produces: [DATABASE_URL, DATABASE_HOST]
    - name: Cache
      produces:
        - CACHE_URL
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Resources }, []contract.V1AlphaResource{
			{Name: "Database", Description: "The app's Postgres database.", Produces: []string{"DATABASE_URL", "DATABASE_HOST"}},
			{Name: "Cache", Produces: []string{"CACHE_URL"}},
		}},
		{"provided contracts", s("  provides: [app-host]\n  providedContracts:\n    - capability: app-host\n      kind: config\n      schema: '{\"type\":\"object\"}'\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.ProvidedContracts },
			[]contract.V1AlphaProvidedContract{{Capability: "app-host", Kind: "config", Schema: `{"type":"object"}`}}},
		{"every requirement field", s(`  requires:
    - capability: github
      hint: Connect a GitHub organisation
      kind: config
      schema: '{"type":"object"}'
      requiresOrgAccount: true
      optional: true
      scope: own-app
      reads: [repo, repoUrl]
      tokenSchema:
        - name: api_token
          kind: string
          secret: true
          placeholder: ghp_example
        - name: org
      verify:
        method: GET
        urlTemplate: https://api.example.test/user?token={api_token}
        headers:
          Authorization: Bearer {api_token}
          Accept: application/json
        basicAuthUser: "{api_token}"
        basicAuthPass: x-oauth
        expectStatus: [200, 204]
    - extension: retired-fan-in
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Requires }, []contract.V1AlphaRequirement{
			{Capability: "github", Hint: "Connect a GitHub organisation", Kind: "config", Schema: `{"type":"object"}`,
				RequiresOrgAccount: true, Optional: true, Scope: "own-app", Reads: []string{"repo", "repoUrl"},
				TokenSchema: []contract.V1AlphaTokenSchemaField{{Name: "api_token", Kind: "string", Secret: true, Placeholder: "ghp_example"}, {Name: "org"}},
				Verify: &contract.V1AlphaVerifyProbe{Method: "GET", URLTemplate: "https://api.example.test/user?token={api_token}",
					Headers:       map[string]string{"Authorization": "Bearer {api_token}", "Accept": "application/json"},
					BasicAuthUser: "{api_token}", BasicAuthPass: "x-oauth", ExpectStatus: []int{200, 204}}},
			{Extension: "retired-fan-in"},
		}},
		{"every lifecycle key", s(`  lifecycle:
    endpoint: https://fixture.example.test/hook
    operations: [subscribe, unsubscribe, upgrade, validate, rotate]
    needsCredentialRefresh: true
    absenceProbe: true
    exclusiveNames: true
    verbBudgets:
      subscribe: 20m
      unsubscribe: 15m
    contract: kit/v1
    retainsOnUnsubscribe: true
    hooks:
      - beforeEnsure
      - afterDelete
`), lifecycle, &contract.V1AlphaLifecycle{Endpoint: "https://fixture.example.test/hook",
			Operations: []string{"subscribe", "unsubscribe", "upgrade", "validate", "rotate"}, NeedsCredentialRefresh: true, AbsenceProbe: true,
			ExclusiveNames: true, VerbBudgets: map[string]string{"subscribe": "20m", "unsubscribe": "15m"}, Contract: "kit/v1",
			RetainsOnUnsubscribe: true, Hooks: []string{"beforeEnsure", "afterDelete"}}},
		{"a lifecycle declaring no hooks or retainsOnUnsubscribe reads none and false", s("  lifecycle:\n    endpoint: https://fixture.example.test/hook\n"),
			lifecycle, &contract.V1AlphaLifecycle{Endpoint: "https://fixture.example.test/hook"}},
		{"no lifecycle key reads no lifecycle", alphaHead, lifecycle, (*contract.V1AlphaLifecycle)(nil)},
		{"a lifecycle key with no value reads no lifecycle", s("  lifecycle:\n"), lifecycle, (*contract.V1AlphaLifecycle)(nil)},
		{"a lifecycle of comments only reads no lifecycle", s("  lifecycle:\n    # hooks:\n    #   - beforeEnsure\n"), lifecycle, (*contract.V1AlphaLifecycle)(nil)},
		{"params", s(`  params:
    - name: image
      default: ghcr.io/example/app:1.0
      label: Container image
      description: The image the block runs.
      required: true
      secret: false
    - name: apiKey
      secret: true
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Params }, []contract.V1AlphaParam{
			{Name: "image", Default: "ghcr.io/example/app:1.0", Label: "Container image", Description: "The image the block runs.", Required: true},
			{Name: "apiKey", Secret: true},
		}},
		{"observability resources, metrics and cost", s(`  observability:
    resources:
      - id: app-instance
        type: aws.ec2-instance
        from: outputs.instanceId
        priced:
          service: AmazonEC2
          attributes:
            instanceType: t3.small
            location: EU (Frankfurt)
          usageBased: true
      - id: app-data
        type: aws.ebs-volume
        from: outputs.volumeId
    metrics:
      - resource: app-instance
        namespace: AWS/EC2
        region: eu-central-1
        dimensions:
          InstanceId: outputs.instanceId
        series:
          - name: cpu
            metric: CPUUtilization
            stat: Average
            unit: Percent
          - name: network
            metric: NetworkIn
            stat: Sum
    cost:
      groupBy: [app, environment]
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Observability }, &contract.V1AlphaObservability{
			Resources: []contract.V1AlphaObservabilityResource{
				{ID: "app-instance", Type: "aws.ec2-instance", From: "outputs.instanceId", Priced: &contract.V1AlphaPriced{Service: "AmazonEC2",
					Attributes: map[string]string{"instanceType": "t3.small", "location": "EU (Frankfurt)"}, UsageBased: true}},
				{ID: "app-data", Type: "aws.ebs-volume", From: "outputs.volumeId"},
			},
			Metrics: []contract.V1AlphaMetricGroup{{Resource: "app-instance", Namespace: "AWS/EC2", Region: "eu-central-1",
				Dimensions: map[string]string{"InstanceId": "outputs.instanceId"},
				Series: []contract.V1AlphaMetricSeries{{Name: "cpu", Metric: "CPUUtilization", Stat: "Average", Unit: "Percent"},
					{Name: "network", Metric: "NetworkIn", Stat: "Sum"}}}},
			Cost: &contract.V1AlphaCost{GroupBy: []string{"app", "environment"}},
		}},
		{"docs", s(`  docs:
    source:
      git: https://example.test/fixture.git
      ref: v1.0.0
      path: docs
    index: README.md
    pages:
      - title: Getting started
        path: start.md
        anchors:
          image: container-image
          apiKey: api-key
      - title: Reference
        path: reference.md
    links:
      - rel: source
        title: Source code
        url: https://example.test/fixture
      - rel: issues
        url: https://example.test/fixture/issues
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Docs }, &contract.V1AlphaDocs{
			Source: &contract.V1AlphaDocsSource{Git: "https://example.test/fixture.git", Ref: "v1.0.0", Path: "docs"},
			Index:  "README.md",
			Pages: []contract.V1AlphaDocsPage{
				{Title: "Getting started", Path: "start.md", Anchors: map[string]string{"image": "container-image", "apiKey": "api-key"}},
				{Title: "Reference", Path: "reference.md"},
			},
			Links: []contract.V1AlphaDocsLink{
				{Rel: "source", Title: "Source code", URL: "https://example.test/fixture"},
				{Rel: "issues", URL: "https://example.test/fixture/issues"},
			},
		}},
		{"continuity units", s(`  continuity:
    - name: app-data
      kind: volume
      stateful: true
      handover: snapshot
      handoverOutput: dataSnapshotId
    - name: scratch
      kind: ephemeral
      stateful: false
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Continuity }, []contract.V1AlphaContinuityUnit{
			{Name: "app-data", Kind: "volume", Stateful: true, Handover: "snapshot", HandoverOutput: "dataSnapshotId"},
			{Name: "scratch", Kind: "ephemeral"},
		}},
		{"spec.naming.appName, the kit's own field", s(`  naming:
    appName:
      maxLength: 27
      derivedNames:
        - template: "mitstack-app-{appName}-github-actions-ecr-push"
          limit: 64
        - template: "{appName}-db"
          limit: 63
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Naming }, &contract.V1AlphaNaming{AppName: &contract.AppNameNaming{MaxLength: 27,
			DerivedNames: []contract.DerivedName{{Template: "mitstack-app-{appName}-github-actions-ecr-push", Limit: 64}, {Template: "{appName}-db", Limit: 63}}}}},
		{"no spec.naming reads none", alphaHead, func(a *contract.V1AlphaManifest) any { return a.Spec.Naming }, (*contract.V1AlphaNaming)(nil)},
		{"two troubleshooting rules", s(`  troubleshooting:
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
`), func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{
			{Signature: "quota-exceeded", Title: "The account is out of quota", Matches: []string{"QuotaExceeded", "limit reached"},
				Fixes: []string{"Raise the quota in the provider's console.", "Retry the subscribe."}, Doc: "start.md"},
			{Signature: "bad-token", Title: "The token was refused", Matches: []string{"401 Unauthorized"}, Fixes: []string{"Reconnect the account."}},
		}},
		{"a troubleshooting rule's signature alone", s("  troubleshooting:\n    - signature: quota-exceeded\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{{Signature: "quota-exceeded"}}},
		{"a troubleshooting rule's title alone", s("  troubleshooting:\n    - title: The account is out of quota\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{{Title: "The account is out of quota"}}},
		{"a troubleshooting rule's matches alone", s("  troubleshooting:\n    - matches: [QuotaExceeded]\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{{Matches: []string{"QuotaExceeded"}}}},
		{"a troubleshooting rule's fixes alone", s("  troubleshooting:\n    - fixes: [Raise the quota.]\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{{Fixes: []string{"Raise the quota."}}}},
		{"a troubleshooting rule's doc alone", s("  troubleshooting:\n    - doc: start.md\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Troubleshooting }, []contract.V1AlphaTroubleshootingRule{{Doc: "start.md"}}},
	}
}

func yamlReads(t *testing.T) []alphaRead {
	s := func(lines string) string { return alphaHead + lines }
	const deep, col4 = "              ", "    "
	reads := []alphaRead{
		{"a flow mapping reads as its block form", string(edit(t, alphaHead,
			"metadata:\n  id: fixture-block\n  name: Fixture Block\n  owner: fixture-org\n",
			"metadata: {id: fixture-block, name: Fixture Block, owner: fixture-org}\n")),
			func(a *contract.V1AlphaManifest) any { return a.Metadata },
			contract.V1AlphaMetadata{ID: "fixture-block", Name: "Fixture Block", Owner: "fixture-org"}},
		{"a flow sequence of flow mappings reads as its block form", s("  requires:\n    - {capability: app-host, reads: [region]}\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Requires }, []contract.V1AlphaRequirement{{Capability: "app-host", Reads: []string{"region"}}}},
		{"a leading byte order mark", "\xef\xbb\xbf" + alphaHead + "  description: hi\n",
			func(a *contract.V1AlphaManifest) any { return []string{a.Kind, a.Metadata.ID, a.Spec.Description} }, []string{"Block", "fixture-block", "hi"}},
		{"a document starting on its marker line", "--- {apiVersion: mitstack/v1alpha, kind: Block, metadata: {id: fixture-block, name: Fixture Block}, " +
			"spec: {version: 1.0.0, description: hi}}\n",
			func(a *contract.V1AlphaManifest) any { return []string{a.Kind, a.Metadata.ID, a.Spec.Description} }, []string{"Block", "fixture-block", "hi"}},
		{"a leading document marker", "---\n" + alphaHead, func(a *contract.V1AlphaManifest) any { return a.Metadata.ID }, "fixture-block"},
		{"a document end marker", alphaHead + "...\n", func(a *contract.V1AlphaManifest) any { return a.Metadata.ID }, "fixture-block"},
		{"a null list item is dropped", s("  outputs: [host, ~, port]\n  tags:\n    - fixture\n    -\n    - parser\n"),
			func(a *contract.V1AlphaManifest) any { return [][]string{a.Spec.Outputs, a.Spec.Tags, a.Outputs} },
			[][]string{{"host", "port"}, {"fixture", "parser"}, {"host", "port"}}},
		{"an escaped space before a line break is kept", s("  description: \"a\\ \n    b\"\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Description }, "a  b"},
		{"expectStatus: [!!int 5]", s(verifyHead + "        expectStatus: [!!int 5]\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Requires[0].Verify.ExpectStatus }, []int{5}},
		{"a comment on the spec line", strings.Replace(alphaHead, "spec:\n", "spec: # note\n", 1) + "  lifecycle:\n    hooks:\n      - beforeEnsure\n",
			hooksOf, []string{"beforeEnsure"}},
	}
	for _, tag := range []string{"!!str 5", "!!int 5"} {
		reads = append(reads,
			alphaRead{"description: " + tag, s("  description: " + tag + "\n"), func(a *contract.V1AlphaManifest) any { return a.Spec.Description }, "5"},
			alphaRead{"a param default " + tag, s(scalarPositions["spec.params[0].default"](tag)),
				func(a *contract.V1AlphaManifest) any { return a.Spec.Params[0].Default }, "5"})
	}
	multiLine := map[string]func(a *contract.V1AlphaManifest) any{
		"spec.tags[0]":                      func(a *contract.V1AlphaManifest) any { return a.Spec.Tags },
		"spec.requires[0].verify.headers.h": func(a *contract.V1AlphaManifest) any { return []string{a.Spec.Requires[0].Verify.Headers["h"]} },
		"spec.params[0].default":            func(a *contract.V1AlphaManifest) any { return []string{a.Spec.Params[0].Default} },
	}
	for path, read := range multiLine {
		for label, value := range map[string]string{
			"single-quoted, second line deeper":      "'multi\n" + deep + "line'",
			"double-quoted, second line deeper":      "\"multi\n" + deep + "line\"",
			"plain, second line deeper":              "multi\n" + deep + "line",
			"single-quoted, second line at column 4": "'multi\n" + col4 + "line'",
			"double-quoted, second line at column 4": "\"multi\n" + col4 + "line\"",
		} {
			reads = append(reads, alphaRead{label + " at " + path, s(scalarPositions[path](value)), read, []string{"multi line"}})
		}
	}
	for _, tc := range []struct{ value, want string }{
		{"2048", "2048"}, {"1.5", "1.5"}, {"true", "true"}, {`"quoted text"`, "quoted text"},
		{"'single quoted'", "single quoted"}, {"", ""}, {"~", ""}, {"null", ""},
	} {
		reads = append(reads, alphaRead{"a string field reads layer: " + tc.value, s("  layer: " + tc.value + "\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Layer }, tc.want})
	}
	for value, want := range map[string]bool{"true": true, "false": false} {
		reads = append(reads, alphaRead{"a bool field accepts " + value, s("  optional: " + value + "\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Optional }, want})
	}
	// The platform's decoder takes the YAML 1.1 words for a typed bool, so a manifest the platform accepts is never refused here.
	for word, want := range map[string]bool{
		"y": true, "Y": true, "yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
		"n": false, "N": false, "no": false, "No": false, "NO": false, "off": false, "Off": false, "OFF": false,
	} {
		for _, written := range []string{word, `"` + word + `"`} {
			reads = append(reads, alphaRead{"a bool field takes the YAML 1.1 word " + written,
				s("  params:\n    - name: image\n      secret: " + written + "\n  lifecycle:\n    absenceProbe: " + written + "\n"),
				func(a *contract.V1AlphaManifest) any {
					return []bool{a.Spec.Params[0].Secret, a.Spec.Lifecycle.AbsenceProbe}
				}, []bool{want, want}})
		}
	}
	// An integer field reads a plain number as the platform's decoder does for a typed int.
	for _, tc := range []struct {
		written string
		want    int
	}{{"200.0", 200}, {"200.9", 200}, {"0x10", 16}, {"0o17", 15}, {"1_000", 1000}, {"+200", 200}} {
		reads = append(reads, alphaRead{"an integer field reads " + tc.written, s(verifyHead + "        expectStatus: [" + tc.written + "]\n"),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Requires[0].Verify.ExpectStatus }, []int{tc.want}})
	}
	for _, tc := range []struct {
		label, lines string
		want         []string
	}{
		{"double-quoted items", "    hooks:\n      - \"beforeEnsure\"\n      - \"afterDelete\"\n", []string{"beforeEnsure", "afterDelete"}},
		{"single-quoted items", "    hooks:\n      - 'beforeEnsure'\n      - 'afterDelete'\n", []string{"beforeEnsure", "afterDelete"}},
		{"an item with an inline comment", "    hooks:\n      - beforeEnsure # pre\n      - afterDelete\n", []string{"beforeEnsure", "afterDelete"}},
		{"a quoted item with an inline comment", "    hooks:\n      - 'beforeEnsure'   # pre\n", []string{"beforeEnsure"}},
		{"a comment after the hooks key", "    hooks: # the hook points\n      - beforeEnsure\n", []string{"beforeEnsure"}},
		{"a comment line between items", "    hooks:\n      - beforeEnsure\n      # the delete side\n      - afterDelete\n", []string{"beforeEnsure", "afterDelete"}},
		{"a flow list with quoted items", "    hooks: [\"beforeEnsure\", 'afterDelete']\n", []string{"beforeEnsure", "afterDelete"}},
		{"a flow list with a trailing comment", "    hooks: [beforeEnsure] # c\n", []string{"beforeEnsure"}},
		{"an empty flow list", "    hooks: [] # none yet\n", nil},
		{"a sequence at the key's own indent", "    hooks:\n    - beforeEnsure\n    - afterDelete\n    contract: kit/v1\n", []string{"beforeEnsure", "afterDelete"}},
		{"a comment at column 0 before the hooks key", "# note\n    hooks:\n      - beforeEnsure\n", []string{"beforeEnsure"}},
		{"a # inside quotes", "    hooks:\n      - \"before#Ensure\"\n", []string{"before#Ensure"}},
		{"CRLF line endings", "    hooks:\r\n      - beforeEnsure\r\n      - afterDelete\r\n", []string{"beforeEnsure", "afterDelete"}},
	} {
		reads = append(reads, alphaRead{"hooks as " + tc.label, s("  lifecycle:\n" + tc.lines), hooksOf, tc.want})
	}
	return reads
}

// foldReads covers the second spellings, which are read into kind and never kept beside it.
func foldReads() []alphaRead {
	kind := func(a *contract.V1AlphaManifest) any { return a.Kind }
	reads := []alphaRead{
		{"type: block alone folds into kind", strings.Replace(alphaHead, "kind: Block\n", "type: block\n", 1), kind, "Block"},
		{"type: block beside kind: Block folds into kind", strings.Replace(alphaHead, "kind: Block\n", "type: block\nkind: Block\n", 1), kind, "Block"},
	}
	for _, e := range foldEntries {
		for label, keys := range map[string]string{"format alone": "format: config\n", "kind alone": "kind: config\n",
			"format and kind that agree": "format: config\nkind: config\n"} {
			reads = append(reads, alphaRead{e.label + ": " + label, alphaHead + e.lines(keys), e.kindOf, "config"})
		}
	}
	for label, keys := range map[string]string{"store alone": "store: volume\n", "kind alone": "kind: volume\n",
		"store and kind that agree": "store: volume\nkind: volume\n"} {
		reads = append(reads, alphaRead{"a continuity unit: " + label, alphaHead + continuityUnit(keys),
			func(a *contract.V1AlphaManifest) any { return a.Spec.Continuity[0].Kind }, "volume"})
	}
	for label, lines := range map[string]string{
		"a block list": "  dependsOn:\n    - weather-app\n    - postgres-db\n", "a flow list": "  dependsOn: [weather-app]\n",
		"an empty list": "  dependsOn: []\n", "no value at all": "  dependsOn:\n",
	} {
		reads = append(reads, alphaRead{"a retired spec.dependsOn as " + label + " is accepted and dropped", alphaHead + lines + "  layer: backend\n",
			func(a *contract.V1AlphaManifest) any { return a.Spec.Layer }, "backend"})
	}
	return reads
}

var foldEntries = []struct {
	label, where string
	lines        func(keys string) string
	kindOf       func(a *contract.V1AlphaManifest) any
}{
	{"a requirement", "spec.requires", func(keys string) string { return "  requires:\n    - capability: database\n" + indent(keys, 6) },
		func(a *contract.V1AlphaManifest) any { return a.Spec.Requires[0].Kind }},
	{"a token-schema field", "spec.requires", func(keys string) string {
		return "  requires:\n    - capability: github\n      tokenSchema:\n        - name: api_token\n" + indent(keys, 10)
	}, func(a *contract.V1AlphaManifest) any { return a.Spec.Requires[0].TokenSchema[0].Kind }},
	{"a provided contract", "spec.providedContracts", func(keys string) string {
		return "  provides: [app-host]\n  providedContracts:\n    - capability: app-host\n" + indent(keys, 6)
	}, func(a *contract.V1AlphaManifest) any { return a.Spec.ProvidedContracts[0].Kind }},
}

func continuityUnit(keys string) string {
	return "  continuity:\n    - name: app-data\n" + indent(keys, 6) + "      stateful: false\n"
}

func TestParseManifest_V1AlphaReadsEachFieldAsYAMLDoes(t *testing.T) {
	cases := append(append(sectionReads(), yamlReads(t)...), foldReads()...)
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			a := parseAlpha(t, tc.raw)

			var got any
			func() {
				defer func() {
					if p := recover(); p != nil {
						got = fmt.Sprintf("a missing section (%v)", p)
					}
				}()
				got = tc.got(a)
			}()

			wantField(t, tc.label, got, tc.want)
		})
	}
}

func TestParseManifest_V1AlphaRefusesAManifestNamingWhatIsWrong(t *testing.T) {
	type refusal struct {
		label, raw string
		wants      []string
	}
	s := func(lines string) string { return alphaHead + lines }
	cases := []refusal{
		{"a type other than block", strings.Replace(alphaHead, "kind: Block\n", "type: component\n", 1), []string{"type", "component"}},
		{"type: block beside another kind", strings.Replace(alphaHead, "kind: Block\n", "type: block\nkind: Component\n", 1), []string{"type", "kind", "Component"}},
		{"a continuity unit's store and kind that disagree", s(continuityUnit("store: volume\nkind: database\n")),
			[]string{"spec.continuity", "store", "kind", "volume", "database"}},
		{"a non-boolean retainsOnUnsubscribe", s("  lifecycle:\n    retainsOnUnsubscribe: maybe\n"), []string{"spec.lifecycle.retainsOnUnsubscribe"}},
		{"maxLength twenty-seven", s("  naming:\n    appName:\n      maxLength: twenty-seven\n"), []string{contract.AppNameNamingField + ".maxLength"}},
		{"an alias", s("  description: &d hello\n  releaseNote: *d\n"), []string{"spec.releaseNote", "alias"}},
		{"a merge key", s("  lifecycle:\n    <<: {endpoint: https://fixture.example.test/hook}\n"), []string{"spec.lifecycle", "merge key"}},
		{"!!str 5 in a flow expectStatus", s(verifyHead + "        expectStatus: [!!str 5]\n"), []string{"spec.requires[0].verify.expectStatus[0]"}},
		{"!!str 5 in a block expectStatus", s(verifyHead + "        expectStatus:\n          - !!str 5\n"), []string{"spec.requires[0].verify.expectStatus[0]"}},
		{"!!str 5 as the tags list", s("  tags: !!str 5\n"), []string{"spec.tags"}},
		{"!!str 5 as a bool", s("  optional: !!str 5\n"), []string{"spec.optional"}},
		{"!!bool yes", s("  optional: !!bool yes\n"), []string{"spec.optional"}},
		{"!!int abc", s("  description: !!int abc\n"), []string{"spec.description"}},
		{"!!null x", s("  description: !!null x\n"), []string{"spec.description"}},
		{"a flow list ending in a bare tag", s("  tags: [!!str 5, !!str]\n"), []string{"spec.tags"}},
		{"a %YAML 1.2 directive", "%YAML 1.2\n---\n" + alphaHead, []string{"%YAML"}},
		{"!!binary, whose base64 the kit does not decode", s("  description: !!binary aGk=\n"), []string{"spec.description", "!!binary"}},
		{"a second document with a key", alphaHead + "---\nfoo: bar\n", []string{"more than one YAML document"}},
		{"a second copy of the manifest", alphaHead + "---\n" + alphaHead, []string{"more than one YAML document"}},
		{"a second document after a document end", alphaHead + "...\n---\nfoo: bar\n", []string{"more than one YAML document"}},
		{"an empty document before the manifest", "---\n---\n" + alphaHead, []string{"more than one YAML document"}},
		{"a key written twice: kind", strings.Replace(alphaHead, "kind: Block\n", "kind: Block\nkind: Block\n", 1), []string{"kind"}},
		{"a key written twice: metadata.name", strings.Replace(alphaHead, "  owner: fixture-org\n", "  owner: fixture-org\n  name: Other\n", 1),
			[]string{"metadata.name"}},
		{"a key written twice: spec.layer", s("  layer: backend\n  layer: frontend\n"), []string{"spec.layer"}},
		{"a key written twice: spec.requires[0].capability", s("  requires:\n    - capability: database\n      capability: github\n"),
			[]string{"spec.requires[0].capability"}},
		{"metadata.id a mapping", strings.Replace(alphaHead, "  id: fixture-block\n", "  id:\n    org: fixture\n", 1), []string{"metadata.id"}},
	}
	for _, e := range foldEntries {
		cases = append(cases, refusal{e.label + "'s format and kind that disagree", s(e.lines("format: config\nkind: api\n")),
			[]string{e.where, "format", "kind", "config", "api"}})
	}
	for _, value := range []string{"maybe", `"true"`, "1", "[true]"} {
		cases = append(cases, refusal{"a bool field refuses " + value, s("  optional: " + value + "\n"), []string{"spec.optional"}})
	}
	for _, status := range []string{"[ok]", `["201"]`, "[200, created]"} {
		cases = append(cases, refusal{"an integer field refuses expectStatus " + status, s(verifyHead + "        expectStatus: " + status + "\n"),
			[]string{"spec.requires[0].verify.expectStatus"}})
	}
	for _, written := range []string{`"200"`, "true", "{code: 200}"} {
		cases = append(cases, refusal{"an integer field refuses " + written, s(verifyHead + "        expectStatus: [" + written + "]\n"),
			[]string{"spec.requires[0].verify.expectStatus[0]"}})
	}
	// The platform's decoder reads -.inf and -1e30 as the smallest int64, which the kit refuses.
	for _, value := range []string{"-.inf", ".inf", ".nan", "-1e30", "1e30"} {
		cases = append(cases, refusal{"an integer field refuses " + value, s(verifyHead + "        expectStatus: [" + value + "]\n"),
			[]string{"spec.requires[0].verify.expectStatus[0]"}})
	}
	for _, tc := range []struct{ path, lines string }{
		{"spec.layer", "  layer: [a, b]\n"},
		{"spec.layer", "  layer:\n    a: b\n"},
		{"spec.requires[0].capability", "  requires:\n    - capability: [database]\n"},
		{"spec.tags", "  tags: fixture\n"},
		{"spec.tags", "  tags:\n    a: b\n"},
		{"spec.tags[0]", "  tags:\n    - a: b\n"},
		{"spec.requires", "  requires:\n    capability: database\n"},
		{"spec.lifecycle", "  lifecycle: kit/v1\n"},
		{"spec.lifecycle.hooks", "  lifecycle:\n    hooks: beforeEnsure\n"},
		{"spec.lifecycle.verbBudgets", "  lifecycle:\n    verbBudgets: [subscribe]\n"},
		{"spec.lifecycle.verbBudgets.subscribe", "  lifecycle:\n    verbBudgets:\n      subscribe:\n        minutes: 20\n"},
	} {
		cases = append(cases, refusal{"a value of the wrong shape at " + tc.path + " from " + strings.TrimSpace(tc.lines), s(tc.lines), []string{tc.path}})
	}
	for _, tc := range []struct {
		label, value string
		positions    []string
	}{
		{"a plain value holding \": \"", "a: b", []string{"spec.description", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a value starting with %", "%x", []string{"spec.description", "spec.tags[0]", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a ? complex key", "? a", []string{"spec.description", "spec.tags[0]", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
		{"a bare - as a mapping value", "-", []string{"spec.description", "spec.requires[0].verify.headers.h", "spec.params[0].default"}},
	} {
		for _, path := range tc.positions {
			cases = append(cases, refusal{tc.label + " at " + path, s(scalarPositions[path](tc.value)), []string{path}})
		}
	}
	for _, path := range []string{"spec.tags[0]", "spec.requires[0].verify.headers.h", "spec.params[0].default"} {
		cases = append(cases, refusal{"a plain scalar continued at column 4, which YAML refuses, at " + path, s(scalarPositions[path]("multi\n    line")), nil})
	}
	for _, key := range []string{"bogus", "fix", "fixList", "remedies", "actions", "resolution", "hint",
		"description", "name", "kind", "docs", "match", "Fixes", "FIXES", "Signature"} {
		for i := 0; i < 2; i++ {
			rules := "  troubleshooting:\n"
			for j := 0; j <= i; j++ {
				rules += fmt.Sprintf("    - signature: rule-%d\n      fixes: [Retry.]\n", j)
			}
			cases = append(cases, refusal{fmt.Sprintf("troubleshooting key %s at index %d", key, i), s(rules + "      " + key + ": [Retry.]\n"),
				[]string{fmt.Sprintf("spec.troubleshooting[%d].%s", i, key)}})
		}
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			requireRefusedNaming(t, []byte(tc.raw), tc.wants...)
		})
	}
}

func fieldNames(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

func TestV1AlphaTypes_HoldOnlyTheFieldsThatCarryAKey(t *testing.T) {
	cases := []struct {
		of           string
		typ          reflect.Type
		absent, kept string
	}{
		{"the manifest", reflect.TypeOf(contract.V1AlphaManifest{}), "Type", "Kind"},
		{"a requirement", reflect.TypeOf(contract.V1AlphaRequirement{}), "Format", "Kind"},
		{"a token-schema field", reflect.TypeOf(contract.V1AlphaTokenSchemaField{}), "Format", "Kind"},
		{"a provided contract", reflect.TypeOf(contract.V1AlphaProvidedContract{}), "Format", "Kind"},
		{"a continuity unit", reflect.TypeOf(contract.V1AlphaContinuityUnit{}), "Store", "Kind"},
		{"the spec, which drops the retired dependsOn", reflect.TypeOf(contract.V1AlphaSpec{}), "DependsOn", "Layer"},
	}
	for _, tc := range cases {
		t.Run(tc.of, func(t *testing.T) {
			fields := fieldNames(tc.typ)

			if slicesContain(fields, tc.absent) || !slicesContain(fields, tc.kept) {
				t.Errorf("%s (%s) has fields %v, want %s and no %s, since a second spelling is folded, never kept", tc.of, tc.typ, fields, tc.kept, tc.absent)
			}
		})
	}
	t.Run("a troubleshooting rule holds exactly the five fields that carry its keys", func(t *testing.T) {
		want := []string{"Signature", "Title", "Matches", "Fixes", "Doc"}

		got := fieldNames(reflect.TypeOf(contract.V1AlphaTroubleshootingRule{}))

		wantField(t, "the troubleshooting rule's fields", got, want)
	})
}

func slicesContain(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func optionalWarning(capability string) contract.ManifestWarning {
	return contract.ManifestWarning{
		Path: "spec.requires[" + capability + "].optional",
		Message: "the optional flag on spec.requires[" + capability + "] is retired and its value is still honoured; " +
			"a requires entry either names a capability the block needs or leaves it out, " +
			"so drop the entry rather than the flag when the block runs without a provider",
	}
}

// Warnings come in the platform's order: spec.dependsOn first, then each optional requirement by its untrimmed capability.
func TestParseManifest_ReportsEachRetiredKeyAsOneWarning(t *testing.T) {
	dependsOn := contract.ManifestWarning{Path: "spec.dependsOn",
		Message: "spec.dependsOn is retired and was ignored; declare the dependency with spec.requires as a capability the target provides"}
	cases := []struct {
		label, raw string
		want       []contract.ManifestWarning
		version    string
	}{
		{"spec.dependsOn as a block list", alphaHead + "  dependsOn:\n    - weather-app\n    - postgres-db\n  layer: backend\n", []contract.ManifestWarning{dependsOn}, ""},
		{"spec.dependsOn as a flow list", alphaHead + "  dependsOn: [weather-app]\n  layer: backend\n", []contract.ManifestWarning{dependsOn}, ""},
		{"an optional requirement, keyed by its capability", alphaHead + "  requires:\n    - capability: repo\n      reads: [repo]\n" +
			"    - capability: app-host\n      optional: true\n    - capability: database\n      optional: false\n",
			[]contract.ManifestWarning{optionalWarning("app-host")}, ""},
		{"two optional requirements, once each", alphaHead + "  requires:\n    - capability: app-host\n      optional: true\n" +
			"    - capability: database\n      optional: true\n", []contract.ManifestWarning{optionalWarning("app-host"), optionalWarning("database")}, ""},
		{"an optional requirement's raw capability", alphaHead + "  requires:\n    - capability: \" app-host \"\n      optional: true\n",
			[]contract.ManifestWarning{optionalWarning(" app-host ")}, ""},
		{"spec.dependsOn first, then the requirements in order", alphaHead + "  requires:\n    - capability: b-cap\n      optional: true\n" +
			"    - capability: a-cap\n      optional: true\n  dependsOn: [weather-app]\n",
			[]contract.ManifestWarning{dependsOn, optionalWarning("b-cap"), optionalWarning("a-cap")}, ""},
		{"nothing for a mitstack/v1alpha manifest without a retired key", alphaHead + "  layer: backend\n  requires:\n    - capability: repo\n      reads: [repo]\n",
			nil, contract.ManifestAPIVersionV1Alpha},
		{"nothing for a mitstack/v1beta manifest", betaManifest, nil, contract.ManifestAPIVersion},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			m, err := contract.ParseManifest([]byte(tc.raw))

			if err != nil {
				t.Fatalf("ParseManifest: %v\n%s", err, tc.raw)
			}
			if tc.version != "" && m.APIVersion != tc.version {
				t.Fatalf("APIVersion = %q, want %q", m.APIVersion, tc.version)
			}
			if len(m.Warnings) != 0 || len(tc.want) != 0 {
				wantField(t, "Warnings", m.Warnings, tc.want)
			}
		})
	}
}

func TestParseManifest_V1AlphaHonoursARetiredOptionalFlag(t *testing.T) {
	raw := alphaHead + "  requires:\n    - capability: repo\n      reads: [repo]\n" +
		"    - capability: app-host\n      optional: true\n    - capability: database\n      optional: false\n"

	a := parseAlpha(t, raw)

	requireLen(t, "spec.requires", a.Spec.Requires, 3)
	requireLen(t, "Requires", a.Requires, 3)
	wantField(t, "spec.requires[1] capability, optional", []any{a.Spec.Requires[1].Capability, a.Spec.Requires[1].Optional}, []any{"app-host", true})
	wantField(t, "Requires[1].Optional", a.Requires[1].Optional, true)
}
