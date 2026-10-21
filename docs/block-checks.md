# Running the block checks on your block

This page is for a block author who wants the conformance verdict before
pushing. It covers what `blockverify` judges, how you run it from your block's
own repository, and where CI runs it for you. Every check the platform holds a
block to lives in this kit, so the verdict you get locally is the verdict the
platform reads.

House terms are defined in
[the workspace glossary](https://github.com/mitstack-eu/mitstack-workspace/blob/main/docs/glossary.md).
The ones this page leans on are these. A block is a managed service defined by
its `manifest.yaml`, and its operator is the Go program in the block's
repository that serves the lifecycle calls. The kit is this module,
`github.com/mitstack-eu/operator-kit/v3`, which every operator imports for
the webhook contract and the checks below. An attestation is the platform's
signed record that a block passed every suite, which the catalog shows as a
verified badge.

## What blockverify judges

`blockverify` runs seven suites over one block's directory, in this order. A
suite that fails turns the verdict to `fail`, and a partial pass is never
signed. A suite that does not apply to your block reports `skipped` with the
reason.

| Suite | What it checks |
|---|---|
| `trust-plumbing` | `manifest.yaml` is present and declares `contract: kit/v1`, your `go.mod` requires the kit at or above the floor, a non-test file imports the kit's `handler` package, and no kit-owned function is redeclared. A block that reads the GitHub API must resolve a request-supplied base through an allowlist it owns, either `ghapi.Base` or `ghapi.BaseFromEnv`, or a `urlguard.go` with `allowedGitHubHosts()`. It must also never concatenate a raw `apiBase` into a URL |
| `contract` | The block passes once a non-test file imports the kit's `handler` package, which serves the webhook contract whose goldens the kit owns |
| `secret-safety` | `TestNoSecretLeak` exists and passes, plus its `/logs` variant when the block serves `/logs`. A test file that declares one must not build `kittest.FakeResource`, because a test over the kit's fake judges the fake rather than your block |
| `teardown` | `TestUnsubscribeDeletes*` exists and passes, and its file builds no `kittest.FakeResource`. The kit's handler observes absence after every delete, so the block need not ask its provider again itself |
| `resources` | Each resource the manifest declares is driven through the kit and proven absent afterwards |
| `deliverable` | The block has its own `Dockerfile`, and it copies nothing from the retired `examples/operator-kit` path |
| `manifest` | The manifest parses strictly as `mitstack/v1beta`, its `metadata.id` is a block id, and a changed `spec.version` carries a release class and note |

Every suite that reads the lifecycle contract requires `contract: kit/v1`,
the one contract this kit judges, under which the kit's `handler` package
serves the block's lifecycle route. A manifest in the retired
`mitstack/v1alpha` version fails every one of them, because the parser
refuses it.

The floor is the oldest kit release your `go.mod` may require, and today it
is `v0.1.0`. It moves only by a deliberate decision, so a new kit release
never fails a block that has not picked it up yet.

## Run it locally

The kit is a public module, so Go fetches it with no credential. Run the
suites from your block's directory, naming the kit release to run.
`--standalone` tells `blockverify` to judge that one directory on its own,
and it is the only mode the command has:

```sh
go run github.com/mitstack-eu/operator-kit/v3/blockverify@v3.0.0 --standalone .
```

The version is required because `go run` with `@` builds the command outside
your block's module, so your own `go.mod` cannot choose it. `v3.0.0` is the
first release under the `/v3` module path, and a later tag works the same
way. A block still on kit v2 runs `github.com/mitstack-eu/operator-kit/v2/blockverify`
at its own v2 release. The run takes about a minute, mostly spent compiling and running your
tests. A passing block ends with a line like this, where `kit v0.1.0` is the
floor the suites held your `go.mod` to, not the release you ran:

```text
VERDICT: PASS — 7 suite(s), kit v0.1.0, standalone mode
```

A failing suite prints its reason on its own line, such as the missing test or
the manifest key the parser refused. Add `--json` for the
machine-readable report the attestation binds to.

## Where CI runs it

The kit's reusable workflow, `.github/workflows/publish-block.yml`, runs
`blockverify --standalone` in its `conformance` job on every event your block's
CI calls it for. A red verdict there stops the `publish` job, so no image ships
without a pass.

A block the platform scaffolds also carries `.github/workflows/block-verify.yml`.
That workflow runs the same command on each push to `main` and submits the
report to the platform, which signs it as the block's attestation.
