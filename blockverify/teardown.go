package main

import (
	"fmt"
	"strings"
)

const teardownNeedle = "func TestUnsubscribeDeletes"

const teardownDeletesDetail = "TestUnsubscribeDeletes* proves unsubscribe deletes what subscribe created"

// teardownSuite runs TestUnsubscribeDeletes* as well as checking it exists, so a broken teardown test fails the gate.
func teardownSuite(target string) SuiteResult {
	const name = "teardown"

	if detail, ok := requireKitV1Contract(target); !ok {
		return SuiteResult{name, "fail", detail}
	}

	if !strings.Contains(concatGoTestFiles(target), teardownNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf("%s is missing a %s... test", target, teardownNeedle)}
	}

	if detail, ok := refuseKitFake(target, "TestUnsubscribeDeletes"); !ok {
		return SuiteResult{name, "fail", detail}
	}

	ok, output := runGoTest(target, "TestUnsubscribeDeletes")
	if !ok {
		return SuiteResult{name, "fail", detailFromGoTestOutput(output)}
	}

	return SuiteResult{name, "pass", fmt.Sprintf(
		"%s rule: %s; the kit's handler asks the provider again after a delete", kitV1Contract, teardownDeletesDetail)}
}
