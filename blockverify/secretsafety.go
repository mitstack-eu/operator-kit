package main

import (
	"fmt"
	"strings"
)

const secretLeakNeedle = "func TestNoSecretLeak"
const secretLeakLogsNeedle = "func TestNoSecretLeak_LogsEndpoint"

// secretSafetySuite runs the leak tests as well as checking they exist, which the shell script does not.
func secretSafetySuite(target string) SuiteResult {
	const name = "secret-safety"

	content := concatGoTestFiles(target)
	if !strings.Contains(content, secretLeakNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf("%s is missing a %s... test", target, secretLeakNeedle)}
	}

	if anyGoFileContains(target, `"/logs"`) && !strings.Contains(content, secretLeakLogsNeedle) {
		return SuiteResult{name, "fail", fmt.Sprintf("%s serves /logs but is missing a %s... test", target, secretLeakLogsNeedle)}
	}

	if detail, ok := refuseKitFake(target, "TestNoSecretLeak"); !ok {
		return SuiteResult{name, "fail", detail}
	}

	ok, output := runGoTest(target, "TestNoSecretLeak")
	if !ok {
		return SuiteResult{name, "fail", detailFromGoTestOutput(output)}
	}
	return SuiteResult{name, "pass", "TestNoSecretLeak (+ the /logs variant where /logs is served) passes"}
}
