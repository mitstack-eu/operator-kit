package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// suiteVersion must change with the report shape, suite set, run order or digest algorithms.
const suiteVersion = "1.3.0"

// Report is the standalone-mode conformance report blockverify emits.
type Report struct {
	SuiteVersion   string        `json:"suiteVersion"`
	KitVersion     string        `json:"kitVersion"`
	Mode           string        `json:"mode"`
	ManifestDigest string        `json:"manifestDigest"`
	Verdict        string        `json:"verdict"`
	ResultsDigest  string        `json:"resultsDigest"`
	Suites         []SuiteResult `json:"suites"`
}

// SuiteResult is one entry of Report.Suites, in run order.
type SuiteResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pass" | "fail" | "skipped"
	Detail string `json:"detail"`
}

const usageMessage = `blockverify: standalone conformance runner for a mitstack block living in an
external repository.

Usage:
  blockverify [--json] --standalone [--base-manifest <path>] <dir>

--base-manifest names the base branch's manifest.yaml. When the file exists,
a spec.version that differs from the base's needs spec.releaseClass and
spec.releaseNote. A path that does not exist is a block new on this branch,
and no version is compared.

This command supports ONLY standalone mode: one block's directory, scanned
in isolation. It refuses to run without --standalone and a target directory.
From the block's own directory, run:

  blockverify --standalone .
`

// Run executes the blockverify CLI and returns its exit code, writing only to stdout.
func Run(args []string, stdout io.Writer) int {
	a, ok := parseArgs(args)
	if !ok || !a.standalone || a.target == "" {
		fmt.Fprint(stdout, usageMessage)
		return 2
	}

	report := runStandalone(a.target, a.baseManifest)

	if a.jsonOut {
		raw, err := json.Marshal(report)
		if err != nil {
			fmt.Fprintf(stdout, "blockverify: internal error marshaling report: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(raw))
	} else {
		printHuman(stdout, report)
	}

	if report.Verdict != "pass" {
		return 1
	}
	return 0
}

type cliArgs struct {
	jsonOut, standalone bool
	target              string
	baseManifest        string
}

// parseArgs lets the last non-flag argument win as the target, and reports ok false when --base-manifest has no path.
func parseArgs(args []string) (a cliArgs, ok bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			a.jsonOut = true
		case "--standalone":
			a.standalone = true
		case "--base-manifest":
			if i+1 == len(args) {
				return a, false
			}
			i++
			a.baseManifest = args[i]
		default:
			a.target = args[i]
		}
	}
	return a, true
}

// runStandalone runs the suites in the fixed contract order.
func runStandalone(target, baseManifest string) Report {
	suites := []SuiteResult{
		trustPlumbingSuite(target),
		contractSuite(target),
		secretSafetySuite(target),
		teardownSuite(target),
		resourcesSuite(target),
		deliverableSuite(target),
		manifestSuite(target, baseManifest),
		namingSuite(target),
	}

	verdict := "pass"
	for _, s := range suites {
		if s.Status == "fail" {
			verdict = "fail"
			break
		}
	}

	return Report{
		SuiteVersion:   suiteVersion,
		KitVersion:     kitVersionFloor,
		Mode:           "standalone",
		ManifestDigest: manifestDigest(target),
		Verdict:        verdict,
		ResultsDigest:  resultsDigest(suites),
		Suites:         suites,
	}
}

func manifestDigest(target string) string {
	raw, err := os.ReadFile(filepath.Join(target, "manifest.yaml"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// suiteDigestEntry omits detail and keeps name before status, so the digest equals `jq -c '[.suites[] | {name, status}]' | sha256sum`.
type suiteDigestEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func resultsDigest(suites []SuiteResult) string {
	entries := make([]suiteDigestEntry, 0, len(suites))
	for _, s := range suites {
		entries = append(entries, suiteDigestEntry{Name: s.Name, Status: s.Status})
	}
	raw, _ := json.Marshal(entries)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func printHuman(w io.Writer, r Report) {
	fmt.Fprintf(w, "blockverify %s (%s mode)\n", r.SuiteVersion, r.Mode)
	for _, s := range r.Suites {
		mark := "✗" // ✗
		switch s.Status {
		case "pass":
			mark = "✓" // ✓
		case "skipped":
			mark = "○" // ○
		}
		fmt.Fprintf(w, "  %s %-22s %s\n", mark, s.Name, s.Detail)
	}
	fmt.Fprintln(w)
	if r.Verdict == "pass" {
		fmt.Fprintf(w, "VERDICT: PASS — %d suite(s), kit %s, %s mode\n", len(r.Suites), r.KitVersion, r.Mode)
	} else {
		fmt.Fprintln(w, "VERDICT: FAIL — a partial pass is a FAIL. There is no 'passed with exceptions'.")
	}
}
