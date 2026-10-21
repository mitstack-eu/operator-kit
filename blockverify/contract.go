package main

import "fmt"

// contractSuite passes a kit/v1 block whose non-test code imports the kit's handler, since the kit owns the webhook-contract goldens that handler serves.
func contractSuite(target string) SuiteResult {
	const name = "contract"

	if detail, ok := requireKitV1Contract(target); !ok {
		return SuiteResult{name, "fail", detail}
	}
	file, ok := checkKitHandlerImport(target)
	if !ok {
		return SuiteResult{name, "fail", file}
	}
	return SuiteResult{name, "pass", fmt.Sprintf(
		"%s rule: the kit owns the webhook-contract goldens; %s imports %s", kitV1Contract, file, kitHandlerPackage)}
}
