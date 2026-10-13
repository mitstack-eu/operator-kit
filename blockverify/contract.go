package main

import (
	"fmt"
	"strings"
)

// contractRequiredTests are the webhook-contract tests a legacy block must ship and pass.
var contractRequiredTests = []string{
	"TestContract_DecodesEveryValidGolden",
	"TestContract_RejectsMalformedGoldens",
	"TestContract_EmitsConformingResponse",
}

// contractSuite checks presence before running, because go test -run with no match exits 0; on kit/v1 the handler-import check replaces both.
func contractSuite(target string) SuiteResult {
	const name = "contract"

	contract, detail, ok := contractRule(target)
	if !ok {
		return SuiteResult{name, "fail", detail}
	}
	if contract == kitV1Contract {
		file, ok := checkKitHandlerImport(target)
		if !ok {
			return SuiteResult{name, "fail", file}
		}
		return SuiteResult{name, "pass", fmt.Sprintf(
			"%s rule: the kit owns the webhook-contract goldens, so %s are waived; %s imports %s",
			kitV1Contract, strings.Join(contractRequiredTests, ", "), file, kitHandlerPackage)}
	}

	missing := missingTestFuncs(target, contractRequiredTests)
	if len(missing) > 0 {
		return SuiteResult{name, "fail", fmt.Sprintf("%s is missing %s", target, strings.Join(missing, ", "))}
	}

	ok, output := runGoTest(target, "TestContract_")
	if !ok {
		return SuiteResult{name, "fail", detailFromGoTestOutput(output)}
	}
	return SuiteResult{name, "pass", "TestContract_* accepts every valid golden and rejects the malformed ones"}
}

func missingTestFuncs(target string, want []string) []string {
	content := concatGoTestFiles(target)
	var missing []string
	for _, fn := range want {
		if !strings.Contains(content, "func "+fn+"(") {
			missing = append(missing, fn)
		}
	}
	return missing
}
