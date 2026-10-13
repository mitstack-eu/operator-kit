package contract_test

// A request field added later is optional, so it must leave every golden's DecodeRequest outcome as pinned here.

import (
	"encoding/json"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

type goldenOutcome struct {
	encoded string
	refusal string
}

// wantRequestGoldenOutcomes holds the outcome of every request golden, valid
// and invalid. A golden added later needs a row here, so no golden's outcome
// goes unpinned.
var wantRequestGoldenOutcomes = map[string]goldenOutcome{
	"invalid-missing-nonce": {refusal: `contract: envelope field "nonce" is required`},
	"invalid-unknown-verb":  {refusal: `contract: envelope field "operation" must be one of subscribe, unsubscribe, upgrade, validate, got "reticulate"`},
	"subscribe": {encoded: `{"operation":"subscribe","subscriptionId":"sub_a1b2c3d4e5","idempotencyKey":"sub_a1b2c3d4e5:subscribe:f6a7b8c9","blockId":"blk_aws_app_infra","blockVersion":"1.0.0","orgId":"org_acme","appId":"app_webapp01",` +
		`"params":{"appName":"myapp","domain":"myapp.apps.mitstack.dev","repo":"acme/myapp"},"instances":{"aws":{"name":"Consumer AWS","endpoint":"https://aws.example.com","config":{"region":"eu-west-1"}}},` +
		`"obo":"eyJvcmdfaWQiOiJvcmdfYWNtZSJ9.c2lnbmF0dXJlLXBsYWNlaG9sZGVy","secretInputs":{"dbPassword":"golden-fixture-not-a-secret"},` +
		`"cloudIdentity":{"provider":"aws","roleArn":"arn:aws:iam::123456789012:role/MitstackFederatedRole","webIdentityToken":"wi-token-placeholder","accountRef":"acct-ref-placeholder"},` +
		`"createdAt":"2026-07-15T21:15:06Z","nonce":"n0nce-subscribe-0001"}`},
	"unsubscribe": {encoded: `{"operation":"unsubscribe","subscriptionId":"sub_a1b2c3d4e5","idempotencyKey":"sub_a1b2c3d4e5:unsubscribe:h8c9d0e1","blockId":"blk_aws_app_infra","blockVersion":"1.0.0","orgId":"org_acme","appId":"app_webapp01",` +
		`"params":{"appName":"myapp","domain":"myapp.apps.mitstack.dev","repo":"acme/myapp"},"instances":{"aws":{"name":"Consumer AWS","endpoint":"https://aws.example.com","config":{"region":"eu-west-1"}}},` +
		`"obo":"eyJvcmdfaWQiOiJvcmdfYWNtZSJ9.c2lnbmF0dXJlLXBsYWNlaG9sZGVy",` +
		`"cloudIdentity":{"provider":"aws","roleArn":"arn:aws:iam::123456789012:role/MitstackFederatedRole","webIdentityToken":"wi-token-placeholder"},` +
		`"createdAt":"2026-07-15T21:15:06Z","nonce":"n0nce-unsubscribe-0003","priorOutputs":{"instanceId":"i-0a1b2c3d4e5f67890"}}`},
	"upgrade": {encoded: `{"operation":"upgrade","subscriptionId":"sub_a1b2c3d4e5","idempotencyKey":"sub_a1b2c3d4e5:upgrade:g7b8c9d0","blockId":"blk_aws_app_infra","blockVersion":"1.1.0","orgId":"org_acme","appId":"app_webapp01",` +
		`"params":{"appName":"myapp","domain":"myapp.apps.mitstack.dev","repo":"acme/myapp"},"instances":{"aws":{"name":"Consumer AWS","endpoint":"https://aws.example.com","config":{"region":"eu-west-1"}}},` +
		`"obo":"eyJvcmdfaWQiOiJvcmdfYWNtZSJ9.c2lnbmF0dXJlLXBsYWNlaG9sZGVy","secretInputs":{"dbPassword":"golden-fixture-not-a-secret"},"bindings":{"llm":{"llmEndpoint":"https://llm.example.com"}},` +
		`"cloudIdentity":{"provider":"aws","roleArn":"arn:aws:iam::123456789012:role/MitstackFederatedRole","webIdentityToken":"wi-token-placeholder","accountRef":"acct-ref-placeholder"},` +
		`"createdAt":"2026-07-15T21:15:06Z","nonce":"n0nce-upgrade-0002"}`},
	"validate": {encoded: `{"operation":"validate","subscriptionId":"sub_a1b2c3d4e5","idempotencyKey":"sub_a1b2c3d4e5:validate:i9d0e1f2","blockId":"blk_aws_app_infra","blockVersion":"1.0.0","orgId":"org_acme","appId":"app_webapp01",` +
		`"params":{"appName":"myapp","domain":"myapp.apps.mitstack.dev","repo":"acme/myapp"},"instances":{"aws":{"name":"Consumer AWS","endpoint":"https://aws.example.com","config":{"region":"eu-west-1"}}},` +
		`"obo":"eyJvcmdfaWQiOiJvcmdfYWNtZSJ9.c2lnbmF0dXJlLXBsYWNlaG9sZGVy",` +
		`"cloudIdentity":{"provider":"aws","roleArn":"arn:aws:iam::123456789012:role/MitstackFederatedRole","webIdentityToken":"wi-token-placeholder"},` +
		`"createdAt":"2026-07-15T21:15:06Z","nonce":"n0nce-validate-0004"}`},
}

func TestDecodeRequest_EveryRequestGoldenDecodesOrIsRefusedExactlyAsPinned(t *testing.T) {
	valid, err := contract.ValidGoldens()
	if err != nil {
		t.Fatalf("contract.ValidGoldens(): %v", err)
	}
	invalid, err := contract.InvalidGoldens()
	if err != nil {
		t.Fatalf("contract.InvalidGoldens(): %v", err)
	}
	all := map[string][]byte{}
	for name, raw := range valid {
		all[name] = raw
	}
	for name, raw := range invalid {
		all[name] = raw
	}

	for name := range all {
		if _, ok := wantRequestGoldenOutcomes[name]; !ok {
			t.Errorf("request golden %q has no row in wantRequestGoldenOutcomes, so its outcome is unpinned", name)
		}
	}
	for name, want := range wantRequestGoldenOutcomes {
		raw, ok := all[name]
		if !ok {
			t.Errorf("request golden %q is gone, so its pinned outcome can no longer be compared", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			req, err := contract.DecodeRequest(raw)
			if want.refusal != "" {
				if err == nil {
					t.Fatalf("DecodeRequest(%s) succeeded, want it refused with %q", name, want.refusal)
				}
				if err.Error() != want.refusal {
					t.Errorf("DecodeRequest(%s) refused with %q, want %q", name, err.Error(), want.refusal)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeRequest(%s) refused with %q, want it decoded", name, err.Error())
			}
			got, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("json.Marshal(decoded %s): %v", name, err)
			}
			if string(got) != want.encoded {
				t.Errorf("%s decodes to a Request encoding as\n  %s\nwant\n  %s", name, got, want.encoded)
			}
		})
	}
}
