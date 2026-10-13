package kittest_test

// A key the contract does not name stays accepted at every level, and each problem names the path it concerns.

import (
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// wellFormedResources reports an ensure with an identity and published
// outputs, an observe, a delete, and keys the contract does not name.
const wellFormedResources = `{
  "outputs": {"groupId": "sg-0abc"},
  "resources": {
    "firewall": {
      "ensure": {"identity": {"groupId": "sg-0abc"}, "outputs": {"groupArn": "arn:sg"}, "futureField": 1},
      "observe": {"state": "present", "outputs": {"groupId": "sg-0abc"}, "statusDetail": "rules applied"},
      "futureCall": {"anything": true}
    },
    "network": {
      "delete": {},
      "observe": {"state": "absent"}
    },
    "instance": {
      "ensure": {"error": "refused:quota: no capacity", "statusDetail": "refused:quota: no capacity"}
    }
  }
}`

func TestCheckResponse_AcceptsAWellFormedResourcesReport(t *testing.T) {
	if problems := kittest.CheckResponse([]byte(wellFormedResources)); len(problems) != 0 {
		t.Errorf("CheckResponse rejected a well-formed resources report with %q", problems)
	}
}

func TestCheckResponse_RejectsMalformedResourcesNamingThePath(t *testing.T) {
	cases := []struct {
		name string
		body string
		path string
	}{
		{"resources an array", `{"resources": ["network"]}`, `"resources"`},
		{"resources a string", `{"resources": "network"}`, `"resources"`},
		{"entry a string", `{"resources": {"network": "ensured"}}`, `"resources"."network"`},
		{"entry an array", `{"resources": {"network": ["ensure"]}}`, `"resources"."network"`},
		{"ensure a boolean", `{"resources": {"network": {"ensure": true}}}`, `"resources"."network"."ensure"`},
		{"observe a string", `{"resources": {"network": {"observe": "present"}}}`, `"resources"."network"."observe"`},
		{"delete a number", `{"resources": {"network": {"delete": 1}}}`, `"resources"."network"."delete"`},
		{"ensure outputs an array", `{"resources": {"network": {"ensure": {"outputs": ["vpc-0abc"]}}}}`, `"resources"."network"."ensure"."outputs"`},
		{"observe outputs not strings", `{"resources": {"network": {"observe": {"outputs": {"port": 8080}}}}}`, `"resources"."network"."observe"."outputs"`},
		{"ensure identity a string", `{"resources": {"network": {"ensure": {"identity": "vpc-0abc"}}}}`, `"resources"."network"."ensure"."identity"`},
		{"ensure identity not strings", `{"resources": {"network": {"ensure": {"identity": {"vpcId": 7}}}}}`, `"resources"."network"."ensure"."identity"`},
		{"delete error a number", `{"resources": {"network": {"delete": {"error": 7}}}}`, `"resources"."network"."delete"."error"`},
		{"observe state a boolean", `{"resources": {"network": {"observe": {"state": true}}}}`, `"resources"."network"."observe"."state"`},
		{"observe statusDetail an array", `{"resources": {"network": {"observe": {"statusDetail": ["waiting"]}}}}`, `"resources"."network"."observe"."statusDetail"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := kittest.CheckResponse([]byte(c.body))
			if len(problems) == 0 {
				t.Fatalf("CheckResponse accepted %s, want a problem naming %s", c.body, c.path)
			}
			joined := strings.Join(problems, "; ")
			if !strings.Contains(joined, c.path) {
				t.Errorf("CheckResponse(%s) problems %q do not name the path %s", c.body, problems, c.path)
			}
		})
	}
}

func TestCheckResponse_ReportsEachMalformedResourceSeparately(t *testing.T) {
	body := `{"resources": {"network": {"ensure": true}, "instance": {"observe": {"state": 3}}}}`
	joined := strings.Join(kittest.CheckResponse([]byte(body)), "; ")
	for _, path := range []string{`"resources"."network"."ensure"`, `"resources"."instance"."observe"."state"`} {
		if !strings.Contains(joined, path) {
			t.Errorf("problems %q do not name %s: every malformed entry is reported, not only the first", joined, path)
		}
	}
}
