package main

// The manifest and naming suites and the contract rule read a manifest through contract.ParseManifest and judge what it declares.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const manifestHead = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: reads-consumer
  name: Reads Consumer
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: A block for the manifest suite.
`

func withVersion(t *testing.T, v string) string {
	return replaced(t, manifestHead, "version: 1.0.0", "version: "+v)
}

func withID(t *testing.T, idLine string) string {
	return replaced(t, manifestHead, "  id: reads-consumer\n", "  id: "+idLine+"\n")
}

func plainV1Beta(t *testing.T) string {
	t.Helper()
	raw, err := contract.ManifestGoldens.ReadFile(contract.ManifestGoldensDir + "/plain-v1beta.yaml")
	if err != nil {
		t.Fatalf("reading plain-v1beta.yaml from contract.ManifestGoldens: %v", err)
	}
	return string(raw)
}

func missingBase(t *testing.T) string { return filepath.Join(t.TempDir(), "base-manifest.yaml") }

func dirBase(t *testing.T) string {
	base := missingBase(t)
	mkdir(filepath.Base(base))(t, filepath.Dir(base))
	return base
}

func passes(name, content string) suiteCase {
	return suiteCase{name: name, dir: manifestOnly(content), status: "pass"}
}

func fails(name, content string, detail ...string) suiteCase {
	return suiteCase{name: name, dir: manifestOnly(content), status: "fail", detail: detail}
}

func TestManifestSuite(t *testing.T) {
	head, v1beta := manifestHead, plainV1Beta(t)
	from100 := baseManifest(withVersion(t, "1.0.0"))
	noID := replaced(t, head, "  id: reads-consumer\n", "")
	noMetadata := replaced(t, head, "metadata:\n  id: reads-consumer\n  name: Reads Consumer\n  owner: blockverify-tests\n", "")
	cases := []suiteCase{
		{name: "a directory with no manifest is skipped", dir: emptyDir, status: "skipped"},
		passes("the golden fixture's requires: [] declares no reads and passes", legacyManifest(t)),
		passes("a manifest with no requires passes", head),
		passes("requirements with no reads pass", head+"  requires:\n    - capability: database\n      hint: a postgres database\n    - capability: no-registry-entry-lists-this\n"),
		passes("reads in a flow list that their capability gives pass", head+"  requires:\n    - capability: database\n      reads: [appName, dbName]\n"),
		passes("a read in a block list naming an alias passes", head+"  requires:\n    - capability: app-host\n      reads:\n        - region\n"),
		passes("reads written before the capability pass", head+"  requires:\n    - reads: [host, port]\n      capability: database\n"),
		passes("quoted read names and comments pass", head+"  requires:\n    # the database this block connects to\n    - capability: database # postgres\n      reads: [\"dbName\", 'username'] # both from the database\n"),
		passes("a read two capabilities give passes", head+"  requires:\n    - capability: aws-topology\n      reads:\n        - appHostname\n"),
		passes("a requirement with reads beside ones without passes", head+"  requires:\n    - capability: github\n    - capability: llm\n      reads: [llmEndpoint, model]\n    - capability: database\n"),
		passes("a consumer reading the four kube-cluster contract fields passes", head+"  requires:\n    - capability: kube-cluster\n      reads: [privateEndpoint, issuerUrl, issuerBucket, clusterRepoUrl]\n"),
		passes("a requirement reading region from app-host passes", head+"  requires:\n    - capability: app-host\n      reads: [region]\n"),
		fails("a read the registry does not hold fails, naming the nearest name", head+"  requires:\n    - capability: database\n      reads: [appName, dbNme]\n",
			`spec.requires[database].reads: "dbNme" is not in the resource registry`, `"dbName"`),
		fails("database reading appHostname fails", head+"  requires:\n    - capability: database\n      reads: [appHostname]\n",
			`spec.requires[database].reads: "appHostname" is not an output of capability "database"`),
		fails("database reading an alias of a host entry fails", head+"  requires:\n    - capability: database\n      reads:\n        - instanceType\n",
			`spec.requires[database].reads: "instanceType" is not an output of capability "database"`),
		fails("an offending requirement after a valid one fails, naming it", head+"  requires:\n    - capability: app-host\n      reads: [appHostname]\n    - capability: llm\n      reads: [dbName]\n",
			`spec.requires[llm].reads: "dbName" is not an output of capability "llm"`),
		fails("an empty read name fails", head+"  requires:\n    - capability: database\n      reads: [appName, \"\"]\n",
			`spec.requires[database].reads: a name must not be empty`),
		passes("outputs in a block list with dotted names and an alias pass", head+"  outputs:\n    - dbName\n    - host.type\n    - region\n"),
		passes("outputs in a flow list pass", head+"  outputs: [host, port, username]\n"),
		passes("an empty outputs flow list passes", head+"  outputs: []\n"),
		passes("the kube-cluster block's eight outputs pass", head+"  outputs:\n    - privateEndpoint\n    - issuerUrl\n    - issuerBucket\n    - clusterRepoUrl\n    - serverId\n    - cloud.region\n    - host.type\n    - enrolmentTokenSha256\n"),
		{name: "an output outside the registry names the nearest name, a tie broken alphabetically in a block list", dir: manifestOnly(head + "  outputs:\n    - url\n    - hostnme\n"),
			status: "fail", detail: []string{"spec.outputs", `"hostnme"`, `"host"`}, not: []string{`"hostId"`}},
		{name: "an output outside the registry names the nearest name, a tie broken alphabetically in a flow list", dir: manifestOnly(head + "  outputs: [runsOnP]\n"),
			status: "fail", detail: []string{"spec.outputs", `"runsOnP"`, `"runsOn"`}, not: []string{`"runsOnPr"`}},
		{name: "an output outside the registry whose nearest name is an alias names the alias", dir: manifestOnly(head + "  outputs:\n    - regon\n"),
			status: "fail", detail: []string{"spec.outputs", `"regon"`, `"region"`}, not: []string{`"repo"`}},
		{name: "an unchanged version passes without a release class or note", dir: manifestOnly(withVersion(t, "1.0.0")), base: from100, status: "pass"},
		{name: "a changed version with a class and a plain note passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n  releaseNote: Adds the reads declaration.\n")},
		{name: "a changed version with a class and a folded note passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: correctness-fix\n  releaseNote: >\n    Fixes the retry that dropped the second\n    attempt's outputs.\n  requires: []\n")},
		{name: "a changed version with a literal note before its class passes", base: from100, status: "pass",
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseNote: |\n    Renames the output.\n  releaseClass: breaking\n")},
		{name: "a changed version with no release class fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseClass"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseNote: Adds the reads declaration.\n")},
		{name: "a changed version with no release note fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseNote"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n")},
		{name: "a changed version with a blank release note fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseNote"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n  releaseNote: \"   \"\n")},
		{name: "a changed version with an empty folded release note fails, naming it", base: from100, status: "fail", detail: []string{"spec.releaseNote"},
			dir: manifestOnly(withVersion(t, "1.1.0") + "  releaseClass: feature\n  releaseNote: >\n  requires: []\n")},
		{name: "a changed version with neither class nor note names both", dir: manifestOnly(withVersion(t, "2.0.0")), base: from100,
			status: "fail", detail: []string{"spec.releaseClass", "spec.releaseNote"}},
		{name: "a base manifest that does not exist marks the block new and passes", dir: manifestOnly(withVersion(t, "1.1.0")), base: missingBase, status: "pass"},
		passes("no --base-manifest compares no version", withVersion(t, "1.1.0")),
		{name: "an unreadable base manifest fails, naming it", dir: manifestOnly(withVersion(t, "1.1.0")), base: dirBase, status: "fail", detail: []string{"$BASE"}},
		passes("a worked v1beta manifest passes", v1beta),
		{name: "a worked v1beta manifest passes against an older v1alpha base", dir: manifestOnly(v1beta), base: baseManifest(withVersion(t, "2.4.0")), status: "pass"},
		passes("a bare slug metadata.id passes", withID(t, "billing-db")),
		passes("a double-quoted private metadata.id passes", withID(t, `"@acme/billing-db"`)),
		passes("a single-quoted private metadata.id passes", withID(t, `'@acme/billing-db'`)),
		passes("a private metadata.id with a comment passes", withID(t, `"@org-a/api" # the org's own api block`)),
		passes("a forty-character bare slug passes", withID(t, "a"+strings.Repeat("b", 38)+"c")),
		passes("metadata without an id passes with a valid spec", noID),
		passes("no metadata at all passes with a valid spec", noMetadata),
		fails("a bad metadata.id is reported beside a spec problem", withID(t, `"@Acme/x"`)+"  outputs: [hostnme]\n", "metadata.id", "@<org>/", `spec.outputs: "hostnme"`),
		{name: "metadata without an id reports only the spec problem", dir: manifestOnly(noID + "  outputs: [hostnme]\n"),
			status: "fail", detail: []string{`spec.outputs: "hostnme"`}, not: []string{"metadata.id"}},
		{name: "no metadata at all reports only the spec problem", dir: manifestOnly(noMetadata + "  outputs: [hostnme]\n"),
			status: "fail", detail: []string{`spec.outputs: "hostnme"`}, not: []string{"metadata.id"}},
		fails("metadata as a flow mapping is a failure", "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata: {id: \"@Acme/x\", owner: blockverify-tests}\nspec:\n  layer: backend\n  version: 1.0.0\n", "manifest.yaml", "metadata"),
		fails("metadata.id as an alias is a failure", "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  name: &bad \"@Acme/x\"\n  id: *bad\nspec:\n  layer: backend\n  version: 1.0.0\n", "manifest.yaml", "metadata"),
		fails("metadata.id as a mapping is a failure", "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  id:\n    org: Acme\nspec:\n  layer: backend\n  version: 1.0.0\n", "manifest.yaml", "metadata"),
		passes("the type, format and store spelling passes", source(t, "type-block-spelling-manifest.yaml")),
		passes("the kind-in-all-three spelling passes", source(t, "kind-spelling-manifest.yaml")),
		passes("an old-form manifest with dependsOn and provided slugs passes", head+"  dependsOn:\n    - weather-app\n  provides:\n    - runner\n  requires:\n    - capability: repo\n      reads: [repo]\n    - capability: app-host\n  outputs:\n    - repo\n"),
		{name: "a manifest with dependsOn passes and prints its warning", dir: manifestOnly(head + "  dependsOn:\n    - weather-app\n  requires:\n    - capability: repo\n      reads: [repo]\n"),
			status: "pass", detail: []string{"spec.dependsOn"}},
		fails("spec as a flow mapping is a failure", "apiVersion: mitstack/v1alpha\nkind: Block\nspec: {version: 1.0.0, outputs: [nope]}\n", "manifest.yaml"),
		fails("spec.outputs as a flow mapping is a failure", head+"  outputs: {first: nope}\n", "manifest.yaml"),
		fails("spec.outputs as an alias is a failure", head+"  tags: &outs [nope]\n  outputs: *outs\n", "manifest.yaml"),
		fails("spec.requires as a flow mapping is a failure", head+"  requires: {capability: database, reads: [nope]}\n", "manifest.yaml"),
		fails("spec.requires as an alias is a failure", head+"  resources: &reqs\n    - name: database\n  requires: *reqs\n", "manifest.yaml"),
		{name: "a manifest.yaml that is a directory is a failure", dir: then(emptyDir, mkdir(manifestFile)), status: "fail", detail: []string{"manifest.yaml"}},
		fails("a v1beta manifest carrying spec.outputs fails as the parser refuses it", v1beta+"  outputs: [host]\n", "manifest.yaml", "spec.outputs"),
		fails("an unknown apiVersion fails, naming both known versions", replaced(t, head, "apiVersion: mitstack/v1alpha", "apiVersion: mitstack/v2"),
			"manifest.yaml", "mitstack/v2", contract.ManifestAPIVersionV1Alpha, contract.ManifestAPIVersion),
		fails("no apiVersion fails, naming both known versions", replaced(t, head, "apiVersion: mitstack/v1alpha\n", ""),
			"manifest.yaml", contract.ManifestAPIVersionV1Alpha, contract.ManifestAPIVersion),
	}
	for _, idLine := range []string{`"@Acme/x"`, `"@acme/"`, "acme/x", `"@acme/x/y"`, "Billing-DB", "billing-", "a" + strings.Repeat("b", 39) + "c"} {
		cases = append(cases, fails("metadata.id "+idLine+" outside both forms fails, naming it", withID(t, idLine), "manifest.yaml", "metadata.id", "@<org>/"))
	}
	for _, idLine := range []string{"@acme/billing-db", "@acme/billing-db # the billing database", "`billing-db`"} {
		cases = append(cases, fails("unquoted metadata.id "+idLine+" fails, asking for quotes", withID(t, idLine), "manifest.yaml", "metadata.id", "quote"))
	}
	for path, spec := range map[string]string{
		"spec.frobnicate":             "  frobnicate: true\n",
		"spec.futureSection":          "  futureSection:\n    outputs: not-the-outputs\n    reads: [not, the, reads]\n",
		"spec.requires[0].frobnicate": "  requires:\n    - capability: database\n      reads: [host]\n      frobnicate: true\n",
		"spec.lifecycle.frobnicate":   "  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n",
		"spec.requires[0].frobnicate after a requires item app-host gives": "  requires:\n    - capability: app-host\n      reads: [region]\n      frobnicate: true\n",
	} {
		field, _, _ := strings.Cut(path, " ")
		cases = append(cases, fails("an unknown field "+path+" fails with the parser's message", head+spec, "manifest.yaml", field, refusal(t, head+spec, field)))
	}
	for label, content := range map[string]string{
		"a base under mitstack/v2":           replaced(t, withVersion(t, "1.0.0"), "apiVersion: mitstack/v1alpha", "apiVersion: mitstack/v2"),
		"a base with an unquoted private id": withID(t, "@acme/billing-db"),
	} {
		_, parseErr := contract.ParseManifest([]byte(content))
		if parseErr == nil {
			t.Fatalf("the %s fixture parses, so it proves nothing:\n%s", label, content)
		}
		cases = append(cases,
			suiteCase{name: label + " leaves its version unknown, so a released change passes", base: baseManifest(content), status: "pass",
				dir: manifestOnly(withVersion(t, "1.0.0") + "  releaseClass: feature\n  releaseNote: Fixes the base manifest.\n")},
			suiteCase{name: label + " leaves its version unknown, so an unreleased change names the base, its error and both fields", base: baseManifest(content),
				dir: manifestOnly(withVersion(t, "1.0.0")), status: "fail", detail: []string{"$BASE", parseErr.Error(), "spec.releaseClass", "spec.releaseNote"}})
	}

	runSuites(t, []string{"manifest"}, cases)
}

// ecrPushTemplate is aws-app-infra's ECR push role name: 37 fixed characters inside IAM's limit of 64, so the app name may run to 27.
const ecrPushTemplate = "mitstack-app-{appName}-github-actions-ecr-push"

func namingSpec(maxLength string, items ...string) string {
	s := "  naming:\n    appName:\n      maxLength: " + maxLength + "\n"
	if len(items) > 0 {
		s += "      derivedNames:\n" + strings.Join(items, "")
	}
	return s
}

func derivedName(template, limit string) string {
	return "        - template: \"" + template + "\"\n          limit: " + limit + "\n"
}

var awsAppInfraNamingSpec = namingSpec("27", derivedName(ecrPushTemplate, "64"))

func TestNamingSuite(t *testing.T) {
	// The roomy template comes first, so neither the first template nor the largest limit can pass for the smallest.
	smallestLast := []string{derivedName("{appName}-db", "63"), derivedName(ecrPushTemplate, "64")}
	cases := []suiteCase{
		passes("aws-app-infra's declaration passes", manifestHead+awsAppInfraNamingSpec),
		fails("a maxLength that differs from the computed limit fails, naming the template and the limit", manifestHead+namingSpec("40", derivedName(ecrPushTemplate, "64")), ecrPushTemplate, "27"),
		passes("a maxLength equal to the smallest room over the templates passes", manifestHead+namingSpec("27", smallestLast...)),
		fails("a maxLength equal to a larger template's room fails, naming the smallest", manifestHead+namingSpec("60", smallestLast...), ecrPushTemplate, "27"),
		fails("a maxLength with no derivedNames fails", manifestHead+namingSpec("27"), "derivedNames"),
		fails("a template without the placeholder fails, naming it", manifestHead+namingSpec("27", derivedName("mitstack-app-github-actions-ecr-push", "64")),
			"mitstack-app-github-actions-ecr-push", "{appName}"),
		// 63 is what the one-placeholder formula gives, so only the refusal of a repeated placeholder can fail it.
		fails("a template using the placeholder more than once fails", manifestHead+namingSpec("63", derivedName("{appName}-{appName}", "64")), "{appName}-{appName}", "more than once"),
		fails("a template whose fixed text equals its limit fails", manifestHead+namingSpec("0", derivedName(ecrPushTemplate, "37")), ecrPushTemplate, "37"),
		fails("a template whose fixed text exceeds its limit fails", manifestHead+namingSpec("0", derivedName(ecrPushTemplate, "30")), ecrPushTemplate, "30"),
		{name: "a manifest with no spec.naming is skipped", dir: manifestOnly(manifestHead), status: "skipped"},
		{name: "a directory with no manifest is skipped", dir: emptyDir, status: "skipped"},
		fails("an unreadable declaration fails", manifestHead+namingSpec("twenty-seven", derivedName(ecrPushTemplate, "64")), "spec.naming.appName"),
	}
	for label, content := range map[string]string{
		"an unknown spec field beside the declaration": manifestHead + "  frobnicate: true\n" + awsAppInfraNamingSpec,
		"an unknown spec field and no declaration":     manifestHead + "  frobnicate: true\n",
	} {
		cases = append(cases, fails(label+" fails with the parser's message", content, "manifest.yaml", refusal(t, content, "spec.frobnicate")))
	}
	other := manifestHead + "  naming:\n    frobnicate:\n      maxLength: 10\n"
	cases = append(cases, fails("a key under spec.naming other than appName fails with the parser's message", other, "manifest.yaml", refusal(t, other, "spec.naming.frobnicate")))

	runSuites(t, []string{"naming"}, cases)
}

func TestContractRule(t *testing.T) {
	legacy := legacyManifest(t)
	cases := []struct {
		name, content, wantRule, wantRefusal string
	}{
		{name: "a v1alpha block mapping declares kit/v1", content: legacy + "  lifecycle:\n    contract: kit/v1\n", wantRule: kitV1Contract},
		{name: "a v1alpha flow mapping declares kit/v1", content: legacy + "  lifecycle: {contract: kit/v1}\n", wantRule: kitV1Contract},
		{name: "the worked v1beta block declares kit/v1", content: plainV1Beta(t), wantRule: kitV1Contract},
	}
	for path, content := range map[string]string{
		"spec.frobnicate":            legacy + "  frobnicate: true\n",
		"spec.lifecycle.frobnicate":  legacy + "  lifecycle:\n    contract: kit/v1\n    frobnicate: true\n",
		"metadata.frobnicate":        replaced(t, legacy, "  owner: blockverify-tests\n", "  owner: blockverify-tests\n  frobnicate: true\n"),
		contract.LifecycleHooksField: replaced(t, plainV1Beta(t), "    contract: kit/v1", "    contract: kit/v1\n    hooks: [beforeEnsure]"),
	} {
		cases = append(cases, struct{ name, content, wantRule, wantRefusal string }{
			name: "a manifest the parser refuses at " + path + " fails with its message", content: content, wantRefusal: refusal(t, content, path)})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := manifestOnly(c.content)(t)

			rule, detail, ok := contractRule(dir)

			if c.wantRefusal == "" && (!ok || rule != c.wantRule) {
				t.Errorf("contractRule(%s) = %q, %v (detail %q); want %q, true", dir, rule, ok, detail, c.wantRule)
			}
			if c.wantRefusal != "" && (ok || !strings.Contains(detail, "manifest.yaml") || !strings.Contains(detail, c.wantRefusal)) {
				t.Errorf("contractRule(%s) = %q, %v, detail %q; want a failure naming manifest.yaml and carrying %q", dir, rule, ok, detail, c.wantRefusal)
			}
		})
	}
}
