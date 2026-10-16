// Command board-delivery, part of mitstack's own tooling, derives which board keys a push to main delivered
// and records them once against the board's /v1/delivered endpoint. A push
// to main's CI runs it after test and release both succeed.
package main

import "os"

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout))
}
