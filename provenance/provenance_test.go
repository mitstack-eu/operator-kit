package provenance_test

// No private key is committed: the goldens were signed by throwaway keys that were discarded.

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/provenance"
)

const goldenRepo = "mitstack-eu/postgres-db"

func validPayload() provenance.Payload {
	manifest := []byte("apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  id: sample-block\n")
	sum := sha256.Sum256(manifest)
	return provenance.Payload{
		SchemaVersion:  provenance.SchemaVersion,
		BlockID:        "sample-block",
		BlockVersion:   "1.0.0",
		ImageRef:       "123456789012.dkr.ecr.eu-central-1.amazonaws.com/mitstack-sample-block@sha256:" + strings.Repeat("a", 64),
		ImageDigest:    "sha256:" + strings.Repeat("a", 64),
		Manifest:       base64.StdEncoding.EncodeToString(manifest),
		ManifestSha256: "sha256:" + hex.EncodeToString(sum[:]),
		KitVersion:     "v0.1.0",
		Conformance: provenance.Conformance{
			SuiteVersion:  "1.0.0",
			Verdict:       "pass",
			ResultsDigest: "sha256:" + strings.Repeat("b", 64),
			RunURL:        "https://github.com/mitstack-eu/sample-block/actions/runs/1",
		},
		SourceRepository: "mitstack-eu/sample-block",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		IssuedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func goldenRaw(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := provenance.Goldens.ReadFile(provenance.GoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("reading golden %q: %v", name, err)
	}
	return raw
}

func loadGolden(t *testing.T, name string) provenance.Record {
	t.Helper()
	var r provenance.Record
	if err := json.Unmarshal(goldenRaw(t, name), &r); err != nil {
		t.Fatalf("golden %q is not a valid Record: %v", name, err)
	}
	return r
}

// committedResolver serves the committed JWKS, from which invalid-unknown-key.json's signing key is deliberately absent.
func committedResolver(t *testing.T) provenance.KeyResolver {
	t.Helper()
	resolve, err := provenance.ParseJWKS(goldenRaw(t, "record.golden.jwks.json"))
	if err != nil {
		t.Fatalf("ParseJWKS on the committed JWKS: %v", err)
	}
	return resolve
}

func tokenField(t *testing.T, token string, segment int, key string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not three segments", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[segment])
	if err != nil {
		t.Fatalf("decoding token segment %d: %v", segment, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("token segment %d is not JSON: %v", segment, err)
	}
	s, ok := doc[key].(string)
	if !ok {
		t.Fatalf("token segment %d has no string %q: %v", segment, key, doc)
	}
	return s
}

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test RSA key: %v", err)
	}
	return priv
}

func signTestToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestCanonicalAndAudience(t *testing.T) {
	p := validPayload()

	a, errA := provenance.Canonical(p)
	b, errB := provenance.Canonical(p)
	got, err := provenance.Audience(p)

	if errA != nil || errB != nil || !bytes.Equal(a, b) {
		t.Errorf("Canonical twice = %s, %v and %s, %v; want the same bytes both times", a, errA, b, errB)
	}
	sum := sha256.Sum256(a)
	if want := provenance.AudiencePrefix + "sha256:" + hex.EncodeToString(sum[:]); err != nil || got != want {
		t.Errorf("Audience(p) = %q, %v; want %q, the prefix and the sha256 of Canonical(p)", got, err, want)
	}
}

func TestBuild_MintsTokenForExactlyTheComputedAudience(t *testing.T) {
	p := validPayload()
	wantAud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	var gotAud string

	rec, err := provenance.Build(p, func(audience string) (string, error) {
		gotAud = audience
		return "minted-for:" + audience, nil
	})

	if err != nil || gotAud != wantAud || rec.Token != "minted-for:"+wantAud {
		t.Errorf("Build = token %q, %v after minting for %q; want the token minted for %q", rec.Token, err, gotAud, wantAud)
	}
}

func TestBuild_Refuses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*provenance.Payload)
		want   error
	}{
		{"a fail verdict", func(p *provenance.Payload) { p.Conformance.Verdict = "fail" }, provenance.ErrNotConformant},
		{"a partial verdict", func(p *provenance.Payload) { p.Conformance.Verdict = "partial" }, provenance.ErrNotConformant},
		{"an empty verdict", func(p *provenance.Payload) { p.Conformance.Verdict = "" }, provenance.ErrNotConformant},
		{"an upper-case PASS", func(p *provenance.Payload) { p.Conformance.Verdict = "PASS" }, provenance.ErrNotConformant},
		{"a pass with warnings", func(p *provenance.Payload) { p.Conformance.Verdict = "pass-with-warnings" }, provenance.ErrNotConformant},
		{"no schemaVersion", func(p *provenance.Payload) { p.SchemaVersion = "" }, provenance.ErrIncomplete},
		{"no blockId", func(p *provenance.Payload) { p.BlockID = "" }, provenance.ErrIncomplete},
		{"no blockVersion", func(p *provenance.Payload) { p.BlockVersion = "" }, provenance.ErrIncomplete},
		{"no imageRef", func(p *provenance.Payload) { p.ImageRef = "" }, provenance.ErrIncomplete},
		{"no imageDigest", func(p *provenance.Payload) { p.ImageDigest = "" }, provenance.ErrIncomplete},
		{"no manifest", func(p *provenance.Payload) { p.Manifest = "" }, provenance.ErrIncomplete},
		{"no manifestSha256", func(p *provenance.Payload) { p.ManifestSha256 = "" }, provenance.ErrIncomplete},
		{"no kitVersion", func(p *provenance.Payload) { p.KitVersion = "" }, provenance.ErrIncomplete},
		{"no sourceRepository", func(p *provenance.Payload) { p.SourceRepository = "" }, provenance.ErrIncomplete},
		{"no sourceCommit", func(p *provenance.Payload) { p.SourceCommit = "" }, provenance.ErrIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPayload()
			tc.mutate(&p)
			minted := false

			rec, err := provenance.Build(p, func(string) (string, error) { minted = true; return "tok", nil })

			if !errors.Is(err, tc.want) || rec != (provenance.Record{}) {
				t.Errorf("Build = %+v, %v; want a zero Record and %v", rec, err, tc.want)
			}
			if minted && errors.Is(tc.want, provenance.ErrNotConformant) {
				t.Error("Build minted a token for a non-pass verdict; a failed run writes no record")
			}
		})
	}
}

func TestBuild_ReproducesGoldenCanonicalAndAudience(t *testing.T) {
	golden := loadGolden(t, "record.golden.json")
	wantCanonical, err := provenance.Canonical(golden.Payload)
	if err != nil {
		t.Fatalf("Canonical(golden.Payload): %v", err)
	}

	rec, err := provenance.Build(golden.Payload, func(string) (string, error) { return "irrelevant", nil })

	if err != nil {
		t.Fatalf("Build on the golden's own passing payload: %v", err)
	}
	if got, _ := provenance.Canonical(rec.Payload); !bytes.Equal(wantCanonical, got) {
		t.Errorf("Build's canonical encoding = %s, want the golden's %s byte for byte", got, wantCanonical)
	}
	wantAud, _ := provenance.Audience(golden.Payload)
	if aud := tokenField(t, golden.Token, 1, "aud"); aud != wantAud {
		t.Errorf("golden token's aud = %q, want Audience(golden.Payload) = %q", aud, wantAud)
	}
}

func TestVerify_TheGoldens(t *testing.T) {
	cases := []struct {
		name, golden, repo string
		want, notWant      error
	}{
		{"the golden record verifies", "record.golden.json", goldenRepo, nil, nil},
		{"a key in the JWKS claiming another identity is a wrong identity, not an unknown key", "invalid-other-identity.json", goldenRepo, provenance.ErrWrongIdentity, provenance.ErrUnknownKid},
		{"a key absent from the JWKS is unknown", "invalid-unknown-key.json", goldenRepo, provenance.ErrUnknownKid, nil},
		{"a tampered payload no longer matches its audience", "invalid-tampered-payload.json", goldenRepo, provenance.ErrRecordTampered, nil},
		{"a failed conformance run is not conformant", "invalid-failed-conformance.json", goldenRepo, provenance.ErrNotConformant, nil},
		{"an empty expected repository is refused, never read as any repository", "record.golden.json", "", provenance.ErrWrongIdentity, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := loadGolden(t, tc.golden)

			err := provenance.Verify(rec, committedResolver(t), provenance.Expect{Repository: tc.repo})

			if (tc.want == nil && err != nil) || !errors.Is(err, tc.want) || (tc.notWant != nil && errors.Is(err, tc.notWant)) {
				t.Errorf("Verify(%s, repo %q) = %v, want %v and not %v", tc.golden, tc.repo, err, tc.want, tc.notWant)
			}
		})
	}
}

func TestVerify_SignedClaims(t *testing.T) {
	priv, wrongPriv := newTestRSAKey(t), newTestRSAKey(t)
	const kid = "test-kid"
	resolve := func(k string) (*rsa.PublicKey, bool) { return &priv.PublicKey, k == kid }
	now := time.Now().UTC()
	cases := []struct {
		name      string
		mutate    func(*provenance.Payload)
		claims    map[string]any
		signer    *rsa.PrivateKey
		want      error
		wantNamed string
	}{
		{"an expired token still verifies, since an OIDC token lives about ten minutes and the record must stay checkable", nil,
			map[string]any{"iat": now.Add(-2 * time.Hour).Unix(), "exp": now.Add(-90 * time.Minute).Unix()}, priv, nil, ""},
		{"an iat more than five minutes ahead is malformed", nil,
			map[string]any{"iat": now.Add(10 * time.Minute).Unix(), "exp": now.Add(20 * time.Minute).Unix()}, priv, provenance.ErrMalformedToken, ""},
		{"an issuer other than GitHub is untrusted", nil, map[string]any{"iss": "https://not-github.example.com"}, priv, provenance.ErrUntrustedIssuer, ""},
		{"a signed payload missing schemaVersion is incomplete and names it", func(p *provenance.Payload) { p.SchemaVersion = "" }, nil, priv, provenance.ErrIncomplete, "schemaVersion"},
		{"a forged signature is judged before the identity claims the presenter controls", nil,
			map[string]any{"repository": "mitstack-eu/wrong-repo"}, wrongPriv, provenance.ErrBadSignature, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPayload()
			if tc.mutate != nil {
				tc.mutate(&p)
			}
			aud, err := provenance.Audience(p)
			if err != nil {
				t.Fatalf("Audience: %v", err)
			}
			claims := map[string]any{"iss": provenance.Issuer, "aud": aud, "repository": p.SourceRepository, "sha": p.SourceCommit, "iat": now.Unix()}
			for k, v := range tc.claims {
				claims[k] = v
			}
			rec := provenance.Record{Payload: p, Token: signTestToken(t, tc.signer, kid, claims)}

			err = provenance.Verify(rec, resolve, provenance.Expect{Repository: p.SourceRepository})

			if (tc.want == nil && err != nil) || !errors.Is(err, tc.want) {
				t.Errorf("Verify(claims %v) = %v, want %v", tc.claims, err, tc.want)
			}
			if tc.wantNamed != "" && !strings.Contains(fmt.Sprint(err), tc.wantNamed) {
				t.Errorf("Verify error = %v, want it to name %s", err, tc.wantNamed)
			}
		})
	}
}

func TestVerify_RejectsMalformedToken(t *testing.T) {
	p := validPayload()
	resolve := func(string) (*rsa.PublicKey, bool) { return nil, false }
	withHeader := func(header map[string]any) string {
		h, _ := json.Marshal(header)
		pl, _ := json.Marshal(map[string]any{"iss": provenance.Issuer})
		return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(pl) + ".sig"
	}
	for name, token := range map[string]string{
		"empty token":       "",
		"only two segments": "aaaa.bbbb",
		"four segments":     "a.b.c.d",
		"header not base64": "!!!not-base64!!!.eyJhIjoxfQ.sig",
		"alg is not RS256":  withHeader(map[string]any{"alg": "HS256", "kid": "some-kid"}),
		"kid is empty":      withHeader(map[string]any{"alg": "RS256", "kid": ""}),
	} {
		t.Run(name, func(t *testing.T) {
			err := provenance.Verify(provenance.Record{Payload: p, Token: token}, resolve, provenance.Expect{Repository: p.SourceRepository})

			if !errors.Is(err, provenance.ErrMalformedToken) {
				t.Errorf("Verify(token %q) = %v, want ErrMalformedToken", token, err)
			}
		})
	}
}

func TestParseJWKS_ResolvesTheGoldenKidAndNoOther(t *testing.T) {
	kid := tokenField(t, loadGolden(t, "record.golden.json").Token, 0, "kid")
	resolve := committedResolver(t)

	_, known := resolve(kid)
	_, unknown := resolve("not-a-real-kid-at-all")

	if !known || unknown {
		t.Errorf("resolve(%q) = %v, resolve(unknown) = %v; want true and false", kid, known, unknown)
	}
}

func TestSchema_DeclaresDraft202012VersionAndTopLevelVersion(t *testing.T) {
	var doc map[string]any

	err := json.Unmarshal(provenance.Schema, &doc)

	if err != nil || len(provenance.Schema) == 0 {
		t.Fatalf("provenance.Schema is empty or not JSON: %v", err)
	}
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf(`provenance.Schema["$schema"] = %v, want "https://json-schema.org/draft/2020-12/schema"`, doc["$schema"])
	}
	if v, ok := doc["version"]; !ok || v == "" {
		t.Errorf(`provenance.Schema "version" = %v, want a non-empty top-level version`, v)
	}
}

var sha256FieldPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Every golden, valid or invalid, is a Record whose digest fields are shaped like sha256, and neither set is vacuous.
func TestGoldens_AreNonVacuousRecordsWithSha256Digests(t *testing.T) {
	for _, set := range []struct {
		name string
		load func() (map[string][]byte, error)
	}{{"ValidGoldens", provenance.ValidGoldens}, {"InvalidGoldens", provenance.InvalidGoldens}} {
		t.Run(set.name, func(t *testing.T) {
			goldens, err := set.load()

			if err != nil || len(goldens) == 0 {
				t.Fatalf("%s = %d goldens, %v; want a non-empty set, since a vacuous set proves nothing", set.name, len(goldens), err)
			}
			for name, raw := range goldens {
				if strings.HasSuffix(name, ".jwks") {
					t.Errorf("%s surfaces the JWKS companion %q as a record golden", set.name, name)
				}
				var r provenance.Record
				if err := json.Unmarshal(raw, &r); err != nil {
					t.Errorf("golden %q is not valid Record JSON: %v", name, err)
					continue
				}
				for field, value := range map[string]string{
					"imageDigest": r.Payload.ImageDigest, "manifestSha256": r.Payload.ManifestSha256, "conformance.resultsDigest": r.Payload.Conformance.ResultsDigest,
				} {
					if !sha256FieldPattern.MatchString(value) {
						t.Errorf("golden %q: %s = %q, want it to match %s", name, field, value, sha256FieldPattern)
					}
				}
			}
		})
	}
}

const platformPinManifestSha256 = "sha256:1300af076bfb7994658ffa101c9e444d7dd2665accc7f24f6b71b6b86aef9cee"

func TestGoldenRecord_CarriesItsManifestAndEveryAttestedField(t *testing.T) {
	p := loadGolden(t, "record.golden.json").Payload

	raw, err := base64.StdEncoding.DecodeString(p.Manifest)

	if err != nil {
		t.Fatalf("golden manifest is not valid base64: %v", err)
	}
	sum := sha256.Sum256(raw)
	if got := "sha256:" + hex.EncodeToString(sum[:]); p.ManifestSha256 != platformPinManifestSha256 || got != p.ManifestSha256 {
		t.Errorf("golden manifestSha256 = %q, sha256 of its manifest %q; want both the platform/blocks/pins/postgres-db.yaml value %q", p.ManifestSha256, got, platformPinManifestSha256)
	}
	for field, value := range map[string]string{
		"manifest": p.Manifest, "kitVersion": p.KitVersion, "sourceCommit": p.SourceCommit,
		"conformance.verdict": p.Conformance.Verdict, "conformance.suiteVersion": p.Conformance.SuiteVersion, "conformance.resultsDigest": p.Conformance.ResultsDigest,
	} {
		if value == "" {
			t.Errorf("golden %s is empty, want it carried", field)
		}
	}
}

// injectField sets key in the object at path, the record's own top level when path is empty.
func injectField(t *testing.T, raw []byte, path, key string, value any) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding fixture generically: %v", err)
	}
	target := doc
	for _, segment := range strings.FieldsFunc(path, func(r rune) bool { return r == '.' }) {
		next, ok := target[segment].(map[string]any)
		if !ok {
			t.Fatalf("fixture has no object at %q", segment)
		}
		target = next
	}
	target[key] = value
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding mutated fixture: %v", err)
	}
	return out
}

// Decode accepts reordered keys and reformatted whitespace on purpose, because an intermediary may reformat a record and the audience already covers every value.
func TestDecode(t *testing.T) {
	golden := goldenRaw(t, "record.golden.json")
	want := loadGolden(t, "record.golden.json")
	payloadJSON, err := json.Marshal(want.Payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}
	cases := []struct {
		name    string
		raw     []byte
		wantErr error
	}{
		{"the golden record decodes unchanged", golden, nil},
		{"reordered keys and extra whitespace decode to the same record", []byte(fmt.Sprintf("{\n\n  \"token\":   %q  ,\n\n  \"payload\":\n%s\n\n}\n", want.Token, payloadJSON)), nil},
		{"an unknown field at the payload's top level is refused", injectField(t, golden, "payload", "evilExtraField", "attacker-controlled"), provenance.ErrUnknownField},
		{"an unknown field inside conformance is refused", injectField(t, golden, "payload.conformance", "evilExtraField", "attacker-controlled"), provenance.ErrUnknownField},
		{"an unknown field beside payload and token is refused", injectField(t, golden, "", "evilExtraField", "attacker-controlled"), provenance.ErrUnknownField},
		{"the unknown-field golden is refused", goldenRaw(t, "invalid-unknown-field.json"), provenance.ErrUnknownField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := provenance.Decode(tc.raw)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("Decode = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || rec != want {
				t.Errorf("Decode = %+v, %v; want %+v", rec, err, want)
			}
		})
	}
}
