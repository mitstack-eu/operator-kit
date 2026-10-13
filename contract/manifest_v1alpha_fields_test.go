package contract_test

// contract.ParseManifest reads every field the platform's manifest decoder reads under mitstack/v1alpha, and the kit's own spec.naming.appName.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// alphaHead is a complete minimal v1alpha manifest ending inside spec. A
// test appends the spec lines it is about, indented two spaces.
const alphaHead = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: fixture-block
  name: Fixture Block
  owner: fixture-org
spec:
  version: 1.0.0
`

// parseAlpha parses raw and returns its V1Alpha, failing the test when raw
// is refused or is not read as a v1alpha manifest.
func parseAlpha(t *testing.T, raw string) *contract.V1AlphaManifest {
	t.Helper()
	m, err := contract.ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, raw)
	}
	if m.V1Alpha == nil {
		t.Fatalf("ParseManifest gave no V1Alpha for a %s manifest: %+v", contract.ManifestAPIVersionV1Alpha, m)
	}
	return m.V1Alpha
}

func alphaSpec(t *testing.T, specLines string) *contract.V1AlphaManifest {
	t.Helper()
	return parseAlpha(t, alphaHead+specLines)
}

func wantField(t *testing.T, field string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", field, got, want)
	}
}

// requirePresent stops the test when v, a section the manifest declares, was
// read as its zero value, so no later read dereferences a section that is
// not there.
func requirePresent(t *testing.T, field string, v any) {
	t.Helper()
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.IsZero() {
		t.Fatalf("%s was read as empty, but the manifest declares it", field)
	}
}

// requireLen stops the test unless a list read from the manifest has n
// items, so no later index falls outside it.
func requireLen(t *testing.T, field string, v any, n int) {
	t.Helper()
	if got := reflect.ValueOf(v).Len(); got != n {
		t.Fatalf("%s has %d items, want %d", field, got, n)
	}
}

func TestParseManifest_V1AlphaReadsKindAndMetadata(t *testing.T) {
	a := parseAlpha(t, alphaHead)
	wantField(t, "kind", a.Kind, "Block")
	wantField(t, "metadata.id", a.Metadata.ID, "fixture-block")
	wantField(t, "metadata.name", a.Metadata.Name, "Fixture Block")
	wantField(t, "metadata.owner", a.Metadata.Owner, "fixture-org")
	wantField(t, "V1AlphaManifest.ID beside metadata.id", a.ID, a.Metadata.ID)
}

func TestParseManifest_V1AlphaReadsTheSpecScalarsAndLists(t *testing.T) {
	a := alphaSpec(t, `  layer: backend
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
`)
	s := a.Spec
	wantField(t, "spec.layer", s.Layer, "backend")
	wantField(t, "spec.version", s.Version, "1.0.0")
	wantField(t, "spec.description", s.Description, "A fixture block for the parser.")
	wantField(t, "spec.optional", s.Optional, true)
	wantField(t, "spec.tags", s.Tags, []string{"fixture", "parser"})
	wantField(t, "spec.provides", s.Provides, []string{"fixture-capability", "fixture-extra"})
	wantField(t, "spec.outputs", s.Outputs, []string{"host", "port"})
	wantField(t, "spec.upgradeStrategy", s.UpgradeStrategy, "in-place")
	wantField(t, "spec.releaseClass", s.ReleaseClass, "correctness-fix")
	wantField(t, "spec.releaseNote", s.ReleaseNote, "Fixes the fixture.")
	wantField(t, "spec.receivesAppSecrets", s.ReceivesAppSecrets, true)
}

func TestParseManifest_V1AlphaReadsEachResourcesNameDescriptionAndProduces(t *testing.T) {
	a := alphaSpec(t, `  resources:
    - name: Database
      description: The app's Postgres database.
      produces: [DATABASE_URL, DATABASE_HOST]
    - name: Cache
      produces:
        - CACHE_URL
`)
	requireLen(t, "spec.resources", a.Spec.Resources, 2)
	db, cache := a.Spec.Resources[0], a.Spec.Resources[1]
	wantField(t, "spec.resources[0].name", db.Name, "Database")
	wantField(t, "spec.resources[0].description", db.Description, "The app's Postgres database.")
	wantField(t, "spec.resources[0].produces", db.Produces, []string{"DATABASE_URL", "DATABASE_HOST"})
	wantField(t, "spec.resources[1].name", cache.Name, "Cache")
	wantField(t, "spec.resources[1].description", cache.Description, "")
	wantField(t, "spec.resources[1].produces", cache.Produces, []string{"CACHE_URL"})
}

func TestParseManifest_V1AlphaReadsProvidedContracts(t *testing.T) {
	a := alphaSpec(t, `  provides: [app-host]
  providedContracts:
    - capability: app-host
      kind: config
      schema: '{"type":"object"}'
`)
	requireLen(t, "spec.providedContracts", a.Spec.ProvidedContracts, 1)
	pc := a.Spec.ProvidedContracts[0]
	wantField(t, "spec.providedContracts[0].capability", pc.Capability, "app-host")
	wantField(t, "spec.providedContracts[0].kind", pc.Kind, "config")
	wantField(t, "spec.providedContracts[0].schema", pc.Schema, `{"type":"object"}`)
}

func TestParseManifest_V1AlphaReadsEveryRequirementField(t *testing.T) {
	a := alphaSpec(t, `  requires:
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
`)
	requireLen(t, "spec.requires", a.Spec.Requires, 2)
	r := a.Spec.Requires[0]
	wantField(t, "spec.requires[0].capability", r.Capability, "github")
	wantField(t, "spec.requires[0].hint", r.Hint, "Connect a GitHub organisation")
	wantField(t, "spec.requires[0].kind", r.Kind, "config")
	wantField(t, "spec.requires[0].schema", r.Schema, `{"type":"object"}`)
	wantField(t, "spec.requires[0].requiresOrgAccount", r.RequiresOrgAccount, true)
	wantField(t, "spec.requires[0].optional", r.Optional, true)
	wantField(t, "spec.requires[0].scope", r.Scope, "own-app")
	wantField(t, "spec.requires[0].reads", r.Reads, []string{"repo", "repoUrl"})

	requireLen(t, "spec.requires[0].tokenSchema", r.TokenSchema, 2)
	f := r.TokenSchema[0]
	wantField(t, "spec.requires[0].tokenSchema[0].name", f.Name, "api_token")
	wantField(t, "spec.requires[0].tokenSchema[0].kind", f.Kind, "string")
	wantField(t, "spec.requires[0].tokenSchema[0].secret", f.Secret, true)
	wantField(t, "spec.requires[0].tokenSchema[0].placeholder", f.Placeholder, "ghp_example")
	wantField(t, "spec.requires[0].tokenSchema[1].name", r.TokenSchema[1].Name, "org")
	wantField(t, "spec.requires[0].tokenSchema[1].secret", r.TokenSchema[1].Secret, false)

	requirePresent(t, "spec.requires[0].verify", r.Verify)
	v := r.Verify
	wantField(t, "spec.requires[0].verify.method", v.Method, "GET")
	wantField(t, "spec.requires[0].verify.urlTemplate", v.URLTemplate, "https://api.example.test/user?token={api_token}")
	wantField(t, "spec.requires[0].verify.headers", v.Headers,
		map[string]string{"Authorization": "Bearer {api_token}", "Accept": "application/json"})
	wantField(t, "spec.requires[0].verify.basicAuthUser", v.BasicAuthUser, "{api_token}")
	wantField(t, "spec.requires[0].verify.basicAuthPass", v.BasicAuthPass, "x-oauth")
	wantField(t, "spec.requires[0].verify.expectStatus", v.ExpectStatus, []int{200, 204})

	e := a.Spec.Requires[1]
	wantField(t, "spec.requires[1].extension", e.Extension, "retired-fan-in")
	wantField(t, "spec.requires[1].capability", e.Capability, "")
	wantField(t, "spec.requires[1].optional", e.Optional, false)
}

func TestParseManifest_V1AlphaReadsEveryLifecycleKey(t *testing.T) {
	a := alphaSpec(t, `  lifecycle:
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
`)
	requirePresent(t, "spec.lifecycle", a.Spec.Lifecycle)
	l := a.Spec.Lifecycle
	wantField(t, "spec.lifecycle.endpoint", l.Endpoint, "https://fixture.example.test/hook")
	wantField(t, "spec.lifecycle.operations", l.Operations, []string{"subscribe", "unsubscribe", "upgrade", "validate", "rotate"})
	wantField(t, "spec.lifecycle.needsCredentialRefresh", l.NeedsCredentialRefresh, true)
	wantField(t, "spec.lifecycle.absenceProbe", l.AbsenceProbe, true)
	wantField(t, "spec.lifecycle.exclusiveNames", l.ExclusiveNames, true)
	wantField(t, "spec.lifecycle.verbBudgets", l.VerbBudgets, map[string]string{"subscribe": "20m", "unsubscribe": "15m"})
	wantField(t, "spec.lifecycle.contract", l.Contract, "kit/v1")
	wantField(t, "spec.lifecycle.retainsOnUnsubscribe", l.RetainsOnUnsubscribe, true)
	wantField(t, contract.LifecycleHooksField, l.Hooks, []string{"beforeEnsure", "afterDelete"})
}

// A lifecycle that declares neither hooks nor retainsOnUnsubscribe reads
// them as none and false, and a lifecycle: key with no value reads as no
// lifecycle at all.
func TestParseManifest_V1AlphaReadsAnUndeclaredLifecycleAsEmpty(t *testing.T) {
	a := alphaSpec(t, "  lifecycle:\n    endpoint: https://fixture.example.test/hook\n")
	requirePresent(t, "spec.lifecycle", a.Spec.Lifecycle)
	wantField(t, "spec.lifecycle.hooks", len(a.Spec.Lifecycle.Hooks), 0)
	wantField(t, "spec.lifecycle.retainsOnUnsubscribe", a.Spec.Lifecycle.RetainsOnUnsubscribe, false)

	for label, lines := range map[string]string{
		"no lifecycle key":              "",
		"a lifecycle key with no value": "  lifecycle:\n",
		"a lifecycle of comments only":  "  lifecycle:\n    # hooks:\n    #   - beforeEnsure\n",
	} {
		t.Run(label, func(t *testing.T) {
			got := alphaSpec(t, lines).Spec.Lifecycle
			if !reflect.ValueOf(got).IsZero() {
				t.Errorf("spec.lifecycle = %#v, want it empty", got)
			}
		})
	}
}

// Hook names are read as YAML reads each scalar form: quotes and comments
// never reach the name, a sequence may sit at its key's own indent, and a
// # inside quotes is part of the name.
func TestParseManifest_V1AlphaReadsHooksInEveryYAMLForm(t *testing.T) {
	cases := []struct {
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
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			l := alphaSpec(t, "  lifecycle:\n"+tc.lines).Spec.Lifecycle
			requirePresent(t, "spec.lifecycle", l)
			if len(l.Hooks) == 0 && len(tc.want) == 0 {
				return
			}
			wantField(t, contract.LifecycleHooksField, l.Hooks, tc.want)
		})
	}
	t.Run("a comment on the spec line", func(t *testing.T) {
		raw := strings.Replace(alphaHead, "spec:\n", "spec: # note\n", 1) + "  lifecycle:\n    hooks:\n      - beforeEnsure\n"
		l := parseAlpha(t, raw).Spec.Lifecycle
		requirePresent(t, "spec.lifecycle", l)
		wantField(t, contract.LifecycleHooksField, l.Hooks, []string{"beforeEnsure"})
	})
}

func TestParseManifest_V1AlphaReadsParams(t *testing.T) {
	a := alphaSpec(t, `  params:
    - name: image
      default: ghcr.io/example/app:1.0
      label: Container image
      description: The image the block runs.
      required: true
      secret: false
    - name: apiKey
      secret: true
`)
	requireLen(t, "spec.params", a.Spec.Params, 2)
	p := a.Spec.Params[0]
	wantField(t, "spec.params[0].name", p.Name, "image")
	wantField(t, "spec.params[0].default", p.Default, "ghcr.io/example/app:1.0")
	wantField(t, "spec.params[0].label", p.Label, "Container image")
	wantField(t, "spec.params[0].description", p.Description, "The image the block runs.")
	wantField(t, "spec.params[0].required", p.Required, true)
	wantField(t, "spec.params[0].secret", p.Secret, false)
	wantField(t, "spec.params[1].name", a.Spec.Params[1].Name, "apiKey")
	wantField(t, "spec.params[1].secret", a.Spec.Params[1].Secret, true)
	wantField(t, "spec.params[1].required", a.Spec.Params[1].Required, false)
}

func TestParseManifest_V1AlphaReadsObservabilityResourcesMetricsAndCost(t *testing.T) {
	a := alphaSpec(t, `  observability:
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
`)
	requirePresent(t, "spec.observability", a.Spec.Observability)
	o := a.Spec.Observability

	requireLen(t, "spec.observability.resources", o.Resources, 2)
	r := o.Resources[0]
	wantField(t, "spec.observability.resources[0].id", r.ID, "app-instance")
	wantField(t, "spec.observability.resources[0].type", r.Type, "aws.ec2-instance")
	wantField(t, "spec.observability.resources[0].from", r.From, "outputs.instanceId")
	requirePresent(t, "spec.observability.resources[0].priced", r.Priced)
	wantField(t, "spec.observability.resources[0].priced.service", r.Priced.Service, "AmazonEC2")
	wantField(t, "spec.observability.resources[0].priced.attributes", r.Priced.Attributes,
		map[string]string{"instanceType": "t3.small", "location": "EU (Frankfurt)"})
	wantField(t, "spec.observability.resources[0].priced.usageBased", r.Priced.UsageBased, true)
	if p := o.Resources[1].Priced; !reflect.ValueOf(p).IsZero() {
		t.Errorf("spec.observability.resources[1].priced = %#v, want it empty, since the manifest declares none", p)
	}

	requireLen(t, "spec.observability.metrics", o.Metrics, 1)
	g := o.Metrics[0]
	wantField(t, "spec.observability.metrics[0].resource", g.Resource, "app-instance")
	wantField(t, "spec.observability.metrics[0].namespace", g.Namespace, "AWS/EC2")
	wantField(t, "spec.observability.metrics[0].region", g.Region, "eu-central-1")
	wantField(t, "spec.observability.metrics[0].dimensions", g.Dimensions, map[string]string{"InstanceId": "outputs.instanceId"})
	requireLen(t, "spec.observability.metrics[0].series", g.Series, 2)
	s := g.Series[0]
	wantField(t, "spec.observability.metrics[0].series[0].name", s.Name, "cpu")
	wantField(t, "spec.observability.metrics[0].series[0].metric", s.Metric, "CPUUtilization")
	wantField(t, "spec.observability.metrics[0].series[0].stat", s.Stat, "Average")
	wantField(t, "spec.observability.metrics[0].series[0].unit", s.Unit, "Percent")
	wantField(t, "spec.observability.metrics[0].series[1].unit", g.Series[1].Unit, "")

	requirePresent(t, "spec.observability.cost", o.Cost)
	wantField(t, "spec.observability.cost.groupBy", o.Cost.GroupBy, []string{"app", "environment"})
}

func TestParseManifest_V1AlphaReadsDocs(t *testing.T) {
	a := alphaSpec(t, `  docs:
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
`)
	requirePresent(t, "spec.docs", a.Spec.Docs)
	d := a.Spec.Docs
	requirePresent(t, "spec.docs.source", d.Source)
	wantField(t, "spec.docs.source.git", d.Source.Git, "https://example.test/fixture.git")
	wantField(t, "spec.docs.source.ref", d.Source.Ref, "v1.0.0")
	wantField(t, "spec.docs.source.path", d.Source.Path, "docs")
	wantField(t, "spec.docs.index", d.Index, "README.md")

	requireLen(t, "spec.docs.pages", d.Pages, 2)
	wantField(t, "spec.docs.pages[0].title", d.Pages[0].Title, "Getting started")
	wantField(t, "spec.docs.pages[0].path", d.Pages[0].Path, "start.md")
	wantField(t, "spec.docs.pages[0].anchors", d.Pages[0].Anchors, map[string]string{"image": "container-image", "apiKey": "api-key"})
	wantField(t, "spec.docs.pages[1].title", d.Pages[1].Title, "Reference")
	wantField(t, "spec.docs.pages[1].anchors", len(d.Pages[1].Anchors), 0)

	requireLen(t, "spec.docs.links", d.Links, 2)
	wantField(t, "spec.docs.links[0].rel", d.Links[0].Rel, "source")
	wantField(t, "spec.docs.links[0].title", d.Links[0].Title, "Source code")
	wantField(t, "spec.docs.links[0].url", d.Links[0].URL, "https://example.test/fixture")
	wantField(t, "spec.docs.links[1].rel", d.Links[1].Rel, "issues")
	wantField(t, "spec.docs.links[1].title", d.Links[1].Title, "")
}

func TestParseManifest_V1AlphaReadsContinuityUnits(t *testing.T) {
	a := alphaSpec(t, `  continuity:
    - name: app-data
      kind: volume
      stateful: true
      handover: snapshot
      handoverOutput: dataSnapshotId
    - name: scratch
      kind: ephemeral
      stateful: false
`)
	requireLen(t, "spec.continuity", a.Spec.Continuity, 2)
	u := a.Spec.Continuity[0]
	wantField(t, "spec.continuity[0].name", u.Name, "app-data")
	wantField(t, "spec.continuity[0].kind", u.Kind, "volume")
	wantField(t, "spec.continuity[0].stateful", u.Stateful, true)
	wantField(t, "spec.continuity[0].handover", u.Handover, "snapshot")
	wantField(t, "spec.continuity[0].handoverOutput", u.HandoverOutput, "dataSnapshotId")
	wantField(t, "spec.continuity[1].name", a.Spec.Continuity[1].Name, "scratch")
	wantField(t, "spec.continuity[1].stateful", a.Spec.Continuity[1].Stateful, false)
	wantField(t, "spec.continuity[1].handover", a.Spec.Continuity[1].Handover, "")
}

// spec.naming.appName is the kit's own field, which the platform's decoder
// does not have: the longest app name a block accepts and the names it
// derives from it.
func TestParseManifest_V1AlphaReadsSpecNamingAppName(t *testing.T) {
	a := alphaSpec(t, `  naming:
    appName:
      maxLength: 27
      derivedNames:
        - template: "mitstack-app-{appName}-github-actions-ecr-push"
          limit: 64
        - template: "{appName}-db"
          limit: 63
`)
	requirePresent(t, "spec.naming", a.Spec.Naming)
	requirePresent(t, contract.AppNameNamingField, a.Spec.Naming.AppName)
	n := a.Spec.Naming.AppName
	wantField(t, contract.AppNameNamingField+".maxLength", n.MaxLength, 27)
	requireLen(t, contract.AppNameNamingField+".derivedNames", n.DerivedNames, 2)
	wantField(t, contract.AppNameNamingField+".derivedNames[0].template", n.DerivedNames[0].Template, "mitstack-app-{appName}-github-actions-ecr-push")
	wantField(t, contract.AppNameNamingField+".derivedNames[0].limit", n.DerivedNames[0].Limit, 64)
	wantField(t, contract.AppNameNamingField+".derivedNames[1].template", n.DerivedNames[1].Template, "{appName}-db")
	wantField(t, contract.AppNameNamingField+".derivedNames[1].limit", n.DerivedNames[1].Limit, 63)

	if got := alphaSpec(t, "").Spec.Naming; !reflect.ValueOf(got).IsZero() {
		t.Errorf("spec.naming = %#v for a manifest that declares none, want it empty", got)
	}
}

// A top-level type: block names the same document as kind: Block, and is
// read into Kind. A type other than block, or a kind beside it other than
// Block, is refused naming both keys.
func TestParseManifest_V1AlphaFoldsTypeIntoKind(t *testing.T) {
	typeOnly := strings.Replace(alphaHead, "kind: Block\n", "type: block\n", 1)
	both := strings.Replace(alphaHead, "kind: Block\n", "type: block\nkind: Block\n", 1)
	for label, raw := range map[string]string{"type: block alone": typeOnly, "type: block beside kind: Block": both} {
		t.Run(label, func(t *testing.T) {
			wantField(t, "kind", parseAlpha(t, raw).Kind, "Block")
		})
	}
	t.Run("a type other than block", func(t *testing.T) {
		requireRefusedNaming(t, []byte(strings.Replace(alphaHead, "kind: Block\n", "type: component\n", 1)), "type", "component")
	})
	t.Run("type: block beside another kind", func(t *testing.T) {
		requireRefusedNaming(t, []byte(strings.Replace(alphaHead, "kind: Block\n", "type: block\nkind: Component\n", 1)),
			"type", "kind", "Component")
	})
}

// A requirement's, a token-schema field's and a provided contract's format
// is read into its kind. Written beside kind with the same value it is
// accepted; with a different value it is refused naming both keys and both
// values.
func TestParseManifest_V1AlphaFoldsFormatIntoKind(t *testing.T) {
	type entry struct {
		label    string
		lines    func(keys string) string
		kindOf   func(t *testing.T, a *contract.V1AlphaManifest) string
		wherePfx string
	}
	entries := []entry{
		{
			label: "a requirement",
			lines: func(keys string) string { return "  requires:\n    - capability: database\n" + indent(keys, 6) },
			kindOf: func(t *testing.T, a *contract.V1AlphaManifest) string {
				requireLen(t, "spec.requires", a.Spec.Requires, 1)
				return a.Spec.Requires[0].Kind
			},
			wherePfx: "spec.requires",
		},
		{
			label: "a token-schema field",
			lines: func(keys string) string {
				return "  requires:\n    - capability: github\n      tokenSchema:\n        - name: api_token\n" + indent(keys, 10)
			},
			kindOf: func(t *testing.T, a *contract.V1AlphaManifest) string {
				requireLen(t, "spec.requires", a.Spec.Requires, 1)
				requireLen(t, "spec.requires[0].tokenSchema", a.Spec.Requires[0].TokenSchema, 1)
				return a.Spec.Requires[0].TokenSchema[0].Kind
			},
			wherePfx: "spec.requires",
		},
		{
			label: "a provided contract",
			lines: func(keys string) string {
				return "  provides: [app-host]\n  providedContracts:\n    - capability: app-host\n" + indent(keys, 6)
			},
			kindOf: func(t *testing.T, a *contract.V1AlphaManifest) string {
				requireLen(t, "spec.providedContracts", a.Spec.ProvidedContracts, 1)
				return a.Spec.ProvidedContracts[0].Kind
			},
			wherePfx: "spec.providedContracts",
		},
	}
	for _, e := range entries {
		t.Run(e.label, func(t *testing.T) {
			for label, keys := range map[string]string{
				"format alone":               "format: config\n",
				"kind alone":                 "kind: config\n",
				"format and kind that agree": "format: config\nkind: config\n",
			} {
				t.Run(label, func(t *testing.T) {
					wantField(t, e.label+" kind", e.kindOf(t, alphaSpec(t, e.lines(keys))), "config")
				})
			}
			t.Run("format and kind that disagree", func(t *testing.T) {
				requireRefusedNaming(t, []byte(alphaHead+e.lines("format: config\nkind: api\n")),
					e.wherePfx, "format", "kind", "config", "api")
			})
		})
	}
}

// A continuity unit's store is read into its kind, under the same rule.
func TestParseManifest_V1AlphaFoldsStoreIntoKind(t *testing.T) {
	unit := func(keys string) string {
		return "  continuity:\n    - name: app-data\n" + indent(keys, 6) + "      stateful: false\n"
	}
	for label, keys := range map[string]string{
		"store alone":               "store: volume\n",
		"kind alone":                "kind: volume\n",
		"store and kind that agree": "store: volume\nkind: volume\n",
	} {
		t.Run(label, func(t *testing.T) {
			a := alphaSpec(t, unit(keys))
			requireLen(t, "spec.continuity", a.Spec.Continuity, 1)
			wantField(t, "spec.continuity[0].kind", a.Spec.Continuity[0].Kind, "volume")
		})
	}
	t.Run("store and kind that disagree", func(t *testing.T) {
		requireRefusedNaming(t, []byte(alphaHead+unit("store: volume\nkind: database\n")),
			"spec.continuity", "store", "kind", "volume", "database")
	})
}

// The second spellings are read into kind and never kept beside it, so the
// types hold no field for them.
func TestV1AlphaTypes_ExposeOnlyTheFoldedKind(t *testing.T) {
	a := alphaSpec(t, `  provides: [app-host]
  providedContracts:
    - capability: app-host
      format: config
  requires:
    - capability: github
      format: config
      tokenSchema:
        - name: api_token
          format: string
  continuity:
    - name: app-data
      store: volume
      stateful: false
`)
	requireLen(t, "spec.requires", a.Spec.Requires, 1)
	requireLen(t, "spec.requires[0].tokenSchema", a.Spec.Requires[0].TokenSchema, 1)
	requireLen(t, "spec.providedContracts", a.Spec.ProvidedContracts, 1)
	requireLen(t, "spec.continuity", a.Spec.Continuity, 1)
	cases := []struct {
		of     string
		t      reflect.Type
		absent string
	}{
		{"the manifest", reflect.TypeOf(*a), "Type"},
		{"a requirement", reflect.TypeOf(a.Spec.Requires[0]), "Format"},
		{"a token-schema field", reflect.TypeOf(a.Spec.Requires[0].TokenSchema[0]), "Format"},
		{"a provided contract", reflect.TypeOf(a.Spec.ProvidedContracts[0]), "Format"},
		{"a continuity unit", reflect.TypeOf(a.Spec.Continuity[0]), "Store"},
	}
	for _, c := range cases {
		if _, ok := c.t.FieldByName(c.absent); ok {
			t.Errorf("%s (%s) has a field %s; only the folded Kind may carry its value", c.of, c.t, c.absent)
		}
		if _, ok := c.t.FieldByName("Kind"); !ok {
			t.Errorf("%s (%s) has no field Kind to carry the folded value", c.of, c.t)
		}
	}
}

// spec.dependsOn is retired: a manifest carrying it is accepted whatever
// its list, and nothing of it is kept.
func TestParseManifest_V1AlphaAcceptsAndDropsDependsOn(t *testing.T) {
	for label, lines := range map[string]string{
		"a block list":    "  dependsOn:\n    - weather-app\n    - postgres-db\n",
		"a flow list":     "  dependsOn: [weather-app]\n",
		"an empty list":   "  dependsOn: []\n",
		"no value at all": "  dependsOn:\n",
	} {
		t.Run(label, func(t *testing.T) {
			a := alphaSpec(t, lines+"  layer: backend\n")
			wantField(t, "spec.layer beside spec.dependsOn", a.Spec.Layer, "backend")
		})
	}
	spec := reflect.TypeOf(alphaSpec(t, "").Spec)
	for i := 0; i < spec.NumField(); i++ {
		if f := spec.Field(i); f.IsExported() && f.Name == "DependsOn" {
			t.Errorf("the spec type %s exports a DependsOn field; a retired key is dropped, never carried", spec)
		}
	}
}

// A flow mapping or sequence is read as its block form is.
func TestParseManifest_V1AlphaReadsFlowAndBlockCollectionsAlike(t *testing.T) {
	block := alphaHead + "  requires:\n    - capability: app-host\n      reads:\n        - region\n"
	flow := strings.Replace(alphaHead,
		"metadata:\n  id: fixture-block\n  name: Fixture Block\n  owner: fixture-org\n",
		"metadata: {id: fixture-block, name: Fixture Block, owner: fixture-org}\n", 1) +
		"  requires:\n    - {capability: app-host, reads: [region]}\n"
	if flow == alphaHead {
		t.Fatal("the flow fixture edit changed nothing")
	}
	b, f := parseAlpha(t, block), parseAlpha(t, flow)
	wantField(t, "metadata written as a flow mapping", f.Metadata, b.Metadata)
	requireLen(t, "spec.requires (flow)", f.Spec.Requires, 1)
	wantField(t, "spec.requires[0].capability (flow)", f.Spec.Requires[0].Capability, "app-host")
	wantField(t, "spec.requires[0].reads (flow)", f.Spec.Requires[0].Reads, []string{"region"})
}

// A string field takes any scalar as its text, and a null as the empty
// string.
func TestParseManifest_V1AlphaReadsAStringFieldFromAnyScalar(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"2048", "2048"},
		{"1.5", "1.5"},
		{"true", "true"},
		{`"quoted text"`, "quoted text"},
		{"'single quoted'", "single quoted"},
		{"", ""},
		{"~", ""},
		{"null", ""},
	} {
		t.Run("layer: "+tc.value, func(t *testing.T) {
			a := alphaSpec(t, "  layer: "+tc.value+"\n")
			wantField(t, "spec.layer", a.Spec.Layer, tc.want)
		})
	}
}

// A bool field takes true or false only.
func TestParseManifest_V1AlphaBoolTakesTrueOrFalseOnly(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "false": false} {
		t.Run("accepts "+value, func(t *testing.T) {
			wantField(t, "spec.optional", alphaSpec(t, "  optional: "+value+"\n").Spec.Optional, want)
		})
	}
	for _, value := range []string{"maybe", `"true"`, "1", "[true]"} {
		t.Run("refuses "+value, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+"  optional: "+value+"\n"), "spec.optional")
		})
	}
	t.Run("refuses a non-boolean retainsOnUnsubscribe", func(t *testing.T) {
		requireRefusedNaming(t, []byte(alphaHead+"  lifecycle:\n    retainsOnUnsubscribe: maybe\n"),
			"spec.lifecycle.retainsOnUnsubscribe")
	})
}

// A bool field also takes the YAML 1.1 words for true and false, plain or
// quoted, as the platform's decoder does for a typed bool, so a manifest the
// platform accepts is never refused here.
func TestParseManifest_V1AlphaBoolTakesTheYAML11Words(t *testing.T) {
	words := map[string]bool{
		"y": true, "Y": true, "yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
		"n": false, "N": false, "no": false, "No": false, "NO": false, "off": false, "Off": false, "OFF": false,
	}
	for word, want := range words {
		for _, written := range []string{word, `"` + word + `"`} {
			t.Run(written, func(t *testing.T) {
				a := alphaSpec(t, "  params:\n    - name: image\n      secret: "+written+"\n"+
					"  lifecycle:\n    absenceProbe: "+written+"\n")
				requireLen(t, "spec.params", a.Spec.Params, 1)
				wantField(t, "spec.params[0].secret", a.Spec.Params[0].Secret, want)
				requirePresent(t, "spec.lifecycle", a.Spec.Lifecycle)
				wantField(t, "spec.lifecycle.absenceProbe", a.Spec.Lifecycle.AbsenceProbe, want)
			})
		}
	}
}

// An integer field takes integers only.
func TestParseManifest_V1AlphaIntegerFieldsTakeIntegersOnly(t *testing.T) {
	verify := func(status string) string {
		return "  requires:\n    - capability: github\n      verify:\n        urlTemplate: https://api.example.test/user\n" +
			"        expectStatus: " + status + "\n"
	}
	for _, status := range []string{"[ok]", `["201"]`, "[200, created]"} {
		t.Run("expectStatus "+status, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+verify(status)), "spec.requires[0].verify.expectStatus")
		})
	}
	t.Run("maxLength twenty-seven", func(t *testing.T) {
		requireRefusedNaming(t, []byte(alphaHead+"  naming:\n    appName:\n      maxLength: twenty-seven\n"),
			contract.AppNameNamingField+".maxLength")
	})
}

// An integer field reads a plain number as the platform's decoder does for a
// typed int: a float is truncated toward zero, and an integer may be written
// in hexadecimal, in octal, with underscores or with a plus sign. A quoted
// number, a boolean and a mapping are refused, naming the path.
func TestParseManifest_V1AlphaIntegerFieldsReadNumbersAsTheirInteger(t *testing.T) {
	verify := func(status string) string {
		return "  requires:\n    - capability: github\n      verify:\n        urlTemplate: https://api.example.test/user\n" +
			"        expectStatus: [" + status + "]\n"
	}
	for _, tc := range []struct {
		written string
		want    int
	}{
		{"200.0", 200},
		{"200.9", 200},
		{"0x10", 16},
		{"0o17", 15},
		{"1_000", 1000},
		{"+200", 200},
	} {
		t.Run("reads "+tc.written, func(t *testing.T) {
			a := alphaSpec(t, verify(tc.written))
			requireLen(t, "spec.requires", a.Spec.Requires, 1)
			requirePresent(t, "spec.requires[0].verify", a.Spec.Requires[0].Verify)
			wantField(t, "spec.requires[0].verify.expectStatus", a.Spec.Requires[0].Verify.ExpectStatus, []int{tc.want})
		})
	}
	for _, written := range []string{`"200"`, "true", "{code: 200}"} {
		t.Run("refuses "+written, func(t *testing.T) {
			requireRefusedNaming(t, []byte(alphaHead+verify(written)), "spec.requires[0].verify.expectStatus[0]")
		})
	}
}

// A mapping or a list where a scalar is declared, a scalar or a mapping
// where a list is declared, and a list where a mapping is declared are each
// refused naming the path.
func TestParseManifest_V1AlphaRefusesAValueOfTheWrongShapeNamingItsPath(t *testing.T) {
	cases := []struct{ path, lines string }{
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
		{"metadata.id", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path+" from "+strings.TrimSpace(tc.lines), func(t *testing.T) {
			raw := alphaHead + tc.lines
			if tc.path == "metadata.id" {
				raw = strings.Replace(alphaHead, "  id: fixture-block\n", "  id:\n    org: fixture\n", 1)
			}
			requireRefusedNaming(t, []byte(raw), tc.path)
		})
	}
}

// A key written twice in one mapping is refused, naming it.
func TestParseManifest_V1AlphaRefusesADuplicateKeyNamingIt(t *testing.T) {
	cases := map[string]string{
		"kind":                        strings.Replace(alphaHead, "kind: Block\n", "kind: Block\nkind: Block\n", 1),
		"metadata.name":               strings.Replace(alphaHead, "  owner: fixture-org\n", "  owner: fixture-org\n  name: Other\n", 1),
		"spec.layer":                  alphaHead + "  layer: backend\n  layer: frontend\n",
		"spec.requires[0].capability": alphaHead + "  requires:\n    - capability: database\n      capability: github\n",
	}
	for path, raw := range cases {
		t.Run(path, func(t *testing.T) {
			requireRefusedNaming(t, []byte(raw), path)
		})
	}
}

// indent prefixes every line of lines with n spaces.
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
