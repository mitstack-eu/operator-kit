// Command pr-keys, part of mitstack's own tooling, gates a pull request on whether its commit range and its
// own title both deliver a board key. A pull_request CI job runs it against
// the pull request's base and head sha and title.
package main

import "os"

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout))
}
