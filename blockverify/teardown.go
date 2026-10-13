package main

import (
	"fmt"
	"strings"
)

const teardownNeedle = "func TestUnsubscribeDeletes"

const teardownDeletesDetail = "TestUnsubscribeDeletes* proves unsubscribe deletes what subscribe created"

// teardownSuite runs TestUnsubscribeDeletes* as well as checking it exists, so a broken teardown test fails the gate; a legacy block must also verify its teardown, which the kit's handler does for a kit/v1 one.
func teardownSuite(target string) SuiteResult {
	const name = "teardown"

	contract, detail, ok := contractRule(target)
	if !ok {
		return SuiteResult{name, "fail", detail}
	}

	if !strings.Contains(concatGoTestFiles(target), teardownNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf("%s is missing a %s... test", target, teardownNeedle)}
	}

	ok, output := runGoTest(target, "TestUnsubscribeDeletes")
	if !ok {
		return SuiteResult{name, "fail", detailFromGoTestOutput(output)}
	}

	if contract == kitV1Contract {
		return SuiteResult{name, "pass", fmt.Sprintf(
			"%s rule: %s; the kit's handler asks the provider again after a delete, so the TestUnsubscribeVerifies rule is waived",
			kitV1Contract, teardownDeletesDetail)}
	}

	verified, ok := checkVerifiedTeardown(target)
	if !ok {
		return SuiteResult{name, "fail", verified}
	}
	return SuiteResult{name, "pass", teardownDeletesDetail + "; " + verified}
}
