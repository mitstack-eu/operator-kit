# CLAUDE.md — operator-kit

Module path: `github.com/mitstack-eu/operator-kit/v3`.

The kit is stdlib-only by design. No third-party dependency is ever added,
however convenient one looks for a single call site.

Test with `go vet ./... && go test ./...`.

Every push to main that changes the tree cuts the next patch tag; a
`kit-release: minor` or `kit-release: major` line in a commit message raises
that level instead (`scripts/release-operator-kit.sh`).

Commit subjects carry the `MIT-###:` key of the ticket they deliver.

A change under `contract/` is a wire-contract change: the platform must pick
it up by bumping its pin on this module, not by copying the file.
