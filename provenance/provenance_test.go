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

func loadGolden(t *testing.T, name string) provenance.Record {
	t.Helper()
	raw, err := provenance.Goldens.ReadFile(provenance.GoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("reading golden %q: %v", name, err)
	}
	var r provenance.Record
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("golden %q is not a valid Record: %v", name, err)
	}
	return r
}

// invalid-unknown-key.json is signed by a key deliberately absent from the committed JWKS.
func committedResolver(t *testing.T) provenance.KeyResolver {
	t.Helper()
	raw, err := provenance.Goldens.ReadFile(provenance.GoldensDir + "/record.golden.jwks.json")
	if err != nil {
		t.Fatalf("reading committed JWKS: %v", err)
	}
	resolve, err := provenance.ParseJWKS(raw)
	if err != nil {
		t.Fatalf("ParseJWKS on the committed JWKS: %v", err)
	}
	return resolve
}

func decodeJWTSegment(t *testing.T, token string, index int) map[string]interface{} {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q is not three segments", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[index])
	if err != nil {
		t.Fatalf("decoding token segment %d: %v", index, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("token segment %d is not JSON: %v", index, err)
	}
	return doc
}

func stringClaim(t *testing.T, doc map[string]interface{}, key string) string {
	t.Helper()
	v, ok := doc[key]
	if !ok {
		t.Fatalf("no claim %q", key)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("claim %q is not a string: %v", key, v)
	}
	return s
}

func newTestRSAKey(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test RSA key: %v", err)
	}
	return priv, &priv.PublicKey
}

func resolverFor(kid string, pub *rsa.PublicKey) provenance.KeyResolver {
	return func(k string) (*rsa.PublicKey, bool) {
		if k == kid {
			return pub, true
		}
		return nil, false
	}
}

func signTestToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]interface{}{"alg": "RS256", "kid": kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshalling header: %v", err)
	}
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

func TestCanonical_DeterministicAcrossCalls(t *testing.T) {
	p := validPayload()
	a, err := provenance.Canonical(p)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	b, err := provenance.Canonical(p)
	if err != nil {
		t.Fatalf("Canonical (second call): %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("Canonical must be deterministic: two calls on the same payload produced different bytes")
	}
}

func TestAudience_EqualsPrefixPlusSha256OfCanonical(t *testing.T) {
	p := validPayload()
	canon, err := provenance.Canonical(p)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	sum := sha256.Sum256(canon)
	want := provenance.AudiencePrefix + "sha256:" + hex.EncodeToString(sum[:])

	got, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	if got != want {
		t.Errorf("Audience(p) = %q, want %q (AudiencePrefix + sha256 of Canonical(p))", got, want)
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
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if gotAud != wantAud {
		t.Errorf("mintToken was asked for audience %q, want %q", gotAud, wantAud)
	}
	if rec.Token != "minted-for:"+wantAud {
		t.Errorf("Record.Token = %q, want the exact value mintToken returned", rec.Token)
	}
}

func TestBuild_RefusesNonPassVerdict(t *testing.T) {
	for _, verdict := range []string{"fail", "partial", "", "PASS", "pass-with-warnings"} {
		verdict := verdict
		t.Run(fmt.Sprintf("verdict=%q", verdict), func(t *testing.T) {
			p := validPayload()
			p.Conformance.Verdict = verdict

			rec, err := provenance.Build(p, func(string) (string, error) {
				t.Fatal("Build must not mint a token for a non-pass verdict — a failed run writes no record")
				return "", nil
			})
			if !errors.Is(err, provenance.ErrNotConformant) {
				t.Errorf("err = %v, want ErrNotConformant", err)
			}
			if rec != (provenance.Record{}) {
				t.Errorf("Build must return a zero Record when refusing, got %+v", rec)
			}
		})
	}
}

func TestBuild_RefusesIncompletePayload(t *testing.T) {
	cases := map[string]func(*provenance.Payload){
		"schemaVersion":    func(p *provenance.Payload) { p.SchemaVersion = "" },
		"blockId":          func(p *provenance.Payload) { p.BlockID = "" },
		"blockVersion":     func(p *provenance.Payload) { p.BlockVersion = "" },
		"imageRef":         func(p *provenance.Payload) { p.ImageRef = "" },
		"imageDigest":      func(p *provenance.Payload) { p.ImageDigest = "" },
		"manifest":         func(p *provenance.Payload) { p.Manifest = "" },
		"manifestSha256":   func(p *provenance.Payload) { p.ManifestSha256 = "" },
		"kitVersion":       func(p *provenance.Payload) { p.KitVersion = "" },
		"sourceRepository": func(p *provenance.Payload) { p.SourceRepository = "" },
		"sourceCommit":     func(p *provenance.Payload) { p.SourceCommit = "" },
	}
	for name, mutate := range cases {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			p := validPayload()
			mutate(&p)
			if _, err := provenance.Build(p, func(string) (string, error) { return "tok", nil }); !errors.Is(err, provenance.ErrIncomplete) {
				t.Errorf("missing %s: err = %v, want ErrIncomplete", name, err)
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
		t.Fatalf("Build on the golden's own payload must succeed (it is already a pass): %v", err)
	}
	gotCanonical, err := provenance.Canonical(rec.Payload)
	if err != nil {
		t.Fatalf("Canonical(rec.Payload): %v", err)
	}
	if !bytes.Equal(wantCanonical, gotCanonical) {
		t.Error("Build must reproduce the golden's canonical encoding byte for byte from the same inputs")
	}

	wantAud, err := provenance.Audience(golden.Payload)
	if err != nil {
		t.Fatalf("Audience(golden.Payload): %v", err)
	}
	tokenAud := stringClaim(t, decodeJWTSegment(t, golden.Token, 1), "aud")
	if tokenAud != wantAud {
		t.Errorf("golden token's aud claim = %q, want Audience(golden.Payload) = %q", tokenAud, wantAud)
	}
}

func TestVerify_AcceptsTheGoldenRecord(t *testing.T) {
	rec := loadGolden(t, "record.golden.json")
	resolve := committedResolver(t)

	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: "mitstack-eu/postgres-db"}); err != nil {
		t.Fatalf("Verify(golden) = %v, want nil", err)
	}
}

func TestVerify_RejectsOtherIdentity(t *testing.T) {
	rec := loadGolden(t, "invalid-other-identity.json")
	resolve := committedResolver(t)

	err := provenance.Verify(rec, resolve, provenance.Expect{Repository: "mitstack-eu/postgres-db"})
	if !errors.Is(err, provenance.ErrWrongIdentity) {
		t.Errorf("err = %v, want ErrWrongIdentity", err)
	}
	if errors.Is(err, provenance.ErrUnknownKid) {
		t.Error("must not be reported as ErrUnknownKid — the signing key IS in the committed JWKS, only the claimed identity is wrong")
	}
}

func TestVerify_RejectsUnknownKey(t *testing.T) {
	rec := loadGolden(t, "invalid-unknown-key.json")
	resolve := committedResolver(t)

	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: "mitstack-eu/postgres-db"}); !errors.Is(err, provenance.ErrUnknownKid) {
		t.Errorf("err = %v, want ErrUnknownKid", err)
	}
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	rec := loadGolden(t, "invalid-tampered-payload.json")
	resolve := committedResolver(t)

	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: "mitstack-eu/postgres-db"}); !errors.Is(err, provenance.ErrRecordTampered) {
		t.Errorf("err = %v, want ErrRecordTampered", err)
	}
}

func TestVerify_RejectsFailedConformance(t *testing.T) {
	rec := loadGolden(t, "invalid-failed-conformance.json")
	resolve := committedResolver(t)

	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: "mitstack-eu/postgres-db"}); !errors.Is(err, provenance.ErrNotConformant) {
		t.Errorf("err = %v, want ErrNotConformant", err)
	}
}

func TestVerify_EmptyExpectedRepositoryIsRefused(t *testing.T) {
	rec := loadGolden(t, "record.golden.json")
	resolve := committedResolver(t)

	err := provenance.Verify(rec, resolve, provenance.Expect{})
	if err == nil {
		t.Fatal(`Verify must refuse an empty Expect rather than treat it as "any repository"`)
	}
	if !errors.Is(err, provenance.ErrWrongIdentity) {
		t.Errorf("err = %v, want ErrWrongIdentity for an unset exp.Repository", err)
	}
}

func TestVerify_DoesNotRejectAnExpiredToken(t *testing.T) {
	// exp is deliberately not checked: an OIDC token lives about ten minutes, so checking it would make every record unverifiable within the hour.
	priv, pub := newTestRSAKey(t)
	const kid = "test-kid-expired"
	p := validPayload()
	aud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	now := time.Now().UTC()
	claims := map[string]interface{}{
		"iss":        provenance.Issuer,
		"aud":        aud,
		"repository": p.SourceRepository,
		"sha":        p.SourceCommit,
		"iat":        now.Add(-2 * time.Hour).Unix(),
		"exp":        now.Add(-90 * time.Minute).Unix(), // long expired
	}
	rec := provenance.Record{Payload: p, Token: signTestToken(t, priv, kid, claims)}

	if err := provenance.Verify(rec, resolverFor(kid, pub), provenance.Expect{Repository: p.SourceRepository}); err != nil {
		t.Errorf("Verify must accept a record whose token has expired, got %v", err)
	}
}

func TestVerify_RejectsIatMoreThanFiveMinutesInTheFuture(t *testing.T) {
	priv, pub := newTestRSAKey(t)
	const kid = "test-kid-future-iat"
	p := validPayload()
	aud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	now := time.Now().UTC()
	claims := map[string]interface{}{
		"iss":        provenance.Issuer,
		"aud":        aud,
		"repository": p.SourceRepository,
		"sha":        p.SourceCommit,
		"iat":        now.Add(10 * time.Minute).Unix(), // over the 5-minute allowance
		"exp":        now.Add(20 * time.Minute).Unix(),
	}
	rec := provenance.Record{Payload: p, Token: signTestToken(t, priv, kid, claims)}

	err = provenance.Verify(rec, resolverFor(kid, pub), provenance.Expect{Repository: p.SourceRepository})
	if !errors.Is(err, provenance.ErrMalformedToken) {
		t.Errorf("err = %v, want ErrMalformedToken for an iat more than five minutes in the future", err)
	}
}

func TestVerify_RejectsWrongIssuer(t *testing.T) {
	priv, pub := newTestRSAKey(t)
	const kid = "test-kid-issuer"
	p := validPayload()
	aud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	claims := map[string]interface{}{
		"iss":        "https://not-github.example.com",
		"aud":        aud,
		"repository": p.SourceRepository,
		"sha":        p.SourceCommit,
		"iat":        time.Now().UTC().Unix(),
	}
	rec := provenance.Record{Payload: p, Token: signTestToken(t, priv, kid, claims)}

	if err := provenance.Verify(rec, resolverFor(kid, pub), provenance.Expect{Repository: p.SourceRepository}); !errors.Is(err, provenance.ErrUntrustedIssuer) {
		t.Errorf("err = %v, want ErrUntrustedIssuer", err)
	}
}

func TestVerify_RejectsIncompletePayloadMissingSchemaVersion(t *testing.T) {
	priv, pub := newTestRSAKey(t)
	const kid = "test-kid-missing-schema-version"
	p := validPayload()
	p.SchemaVersion = ""
	aud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	claims := map[string]interface{}{
		"iss":        provenance.Issuer,
		"aud":        aud,
		"repository": p.SourceRepository,
		"sha":        p.SourceCommit,
		"iat":        time.Now().UTC().Unix(),
	}
	rec := provenance.Record{Payload: p, Token: signTestToken(t, priv, kid, claims)}

	err = provenance.Verify(rec, resolverFor(kid, pub), provenance.Expect{Repository: p.SourceRepository})
	if !errors.Is(err, provenance.ErrIncomplete) {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
	if !strings.Contains(err.Error(), "schemaVersion") {
		t.Errorf("err = %v, want it to name schemaVersion as the missing field", err)
	}
}

func TestVerify_SignatureCheckedBeforeIdentityMismatch(t *testing.T) {
	// Presenter-controlled claims are judged after the signature, so a forged token cannot leak whether they were well-formed.
	_, pub := newTestRSAKey(t)
	wrongPriv, _ := newTestRSAKey(t)
	const kid = "test-kid-order"
	p := validPayload()
	aud, err := provenance.Audience(p)
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	claims := map[string]interface{}{
		"iss":        provenance.Issuer,
		"aud":        aud,
		"repository": "mitstack-eu/wrong-repo", // also wrong, to prove signature is judged first
		"sha":        p.SourceCommit,
		"iat":        time.Now().UTC().Unix(),
	}
	rec := provenance.Record{Payload: p, Token: signTestToken(t, wrongPriv, kid, claims)}

	err = provenance.Verify(rec, resolverFor(kid, pub), provenance.Expect{Repository: p.SourceRepository})
	if !errors.Is(err, provenance.ErrBadSignature) {
		t.Errorf("err = %v, want ErrBadSignature — the signature must be judged before any claim the presenter controls", err)
	}
}

func TestVerify_RejectsMalformedToken(t *testing.T) {
	p := validPayload()
	resolve := func(string) (*rsa.PublicKey, bool) { return nil, false }

	headerAndPayload := func(header map[string]interface{}) string {
		h, _ := json.Marshal(header)
		pl, _ := json.Marshal(map[string]interface{}{"iss": provenance.Issuer})
		return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(pl) + ".sig"
	}

	cases := map[string]string{
		"empty token":       "",
		"only two segments": "aaaa.bbbb",
		"four segments":     "a.b.c.d",
		"header not base64": "!!!not-base64!!!.eyJhIjoxfQ.sig",
		"alg is not RS256":  headerAndPayload(map[string]interface{}{"alg": "HS256", "kid": "some-kid"}),
		"kid is empty":      headerAndPayload(map[string]interface{}{"alg": "RS256", "kid": ""}),
	}
	for name, token := range cases {
		name, token := name, token
		t.Run(name, func(t *testing.T) {
			rec := provenance.Record{Payload: p, Token: token}
			if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: p.SourceRepository}); !errors.Is(err, provenance.ErrMalformedToken) {
				t.Errorf("err = %v, want ErrMalformedToken", err)
			}
		})
	}
}

func TestParseJWKS_RoundTripsAndUnknownKidReturnsFalse(t *testing.T) {
	golden := loadGolden(t, "record.golden.json")
	kid := stringClaim(t, decodeJWTSegment(t, golden.Token, 0), "kid")

	resolve := committedResolver(t)
	if _, ok := resolve(kid); !ok {
		t.Fatalf("ParseJWKS must resolve the golden token's own kid %q", kid)
	}
	if _, ok := resolve("not-a-real-kid-at-all"); ok {
		t.Error("ParseJWKS's resolver must return false for an unknown kid, not silently accept it")
	}
}

func TestSchema_DeclaresDraft202012VersionAndTopLevelVersion(t *testing.T) {
	if len(provenance.Schema) == 0 {
		t.Fatal("provenance.Schema must not be empty")
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(provenance.Schema, &doc); err != nil {
		t.Fatalf("provenance.Schema must be valid JSON: %v", err)
	}
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf(`provenance.Schema["$schema"] = %v, want "https://json-schema.org/draft/2020-12/schema"`, doc["$schema"])
	}
	version, ok := doc["version"]
	if !ok || version == "" {
		t.Error(`provenance.Schema must declare a non-empty top-level "version" field`)
	}
}

func TestValidGoldens_ExcludesAnyJWKSCompanionBySuffix(t *testing.T) {
	valid, err := provenance.ValidGoldens()
	if err != nil {
		t.Fatalf("ValidGoldens: %v", err)
	}
	for name := range valid {
		if strings.HasSuffix(name, ".jwks") {
			t.Errorf("ValidGoldens must not surface a *.jwks.json file (%q) as if it were a record golden — the accessor must skip the .jwks.json suffix generally", name)
		}
	}
}

func TestValidGoldens_NonVacuousAndEveryEntryIsARecord(t *testing.T) {
	valid, err := provenance.ValidGoldens()
	if err != nil {
		t.Fatalf("ValidGoldens: %v", err)
	}
	if len(valid) == 0 {
		t.Fatal("ValidGoldens must not be empty — a vacuous acceptance set proves nothing")
	}
	for name, raw := range valid {
		var r provenance.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Errorf("valid golden %q is not valid Record JSON: %v", name, err)
		}
	}
}

func TestInvalidGoldens_NonVacuous(t *testing.T) {
	invalid, err := provenance.InvalidGoldens()
	if err != nil {
		t.Fatalf("InvalidGoldens: %v", err)
	}
	if len(invalid) == 0 {
		t.Fatal("InvalidGoldens must not be empty — a vacuous rejection set proves nothing")
	}
	for name, raw := range invalid {
		var r provenance.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Errorf("invalid golden %q is not valid Record JSON: %v", name, err)
		}
	}
}

const platformPinManifestSha256 = "sha256:1300af076bfb7994658ffa101c9e444d7dd2665accc7f24f6b71b6b86aef9cee"

func TestGoldenManifestSha256_MatchesThePlatformPinLiteralAndItsOwnBytes(t *testing.T) {
	rec := loadGolden(t, "record.golden.json")

	if rec.Payload.ManifestSha256 != platformPinManifestSha256 {
		t.Errorf("golden manifestSha256 = %q, want the literal platform/blocks/pins/postgres-db.yaml value %q", rec.Payload.ManifestSha256, platformPinManifestSha256)
	}

	raw, err := base64.StdEncoding.DecodeString(rec.Payload.Manifest)
	if err != nil {
		t.Fatalf("golden manifest is not valid base64: %v", err)
	}
	sum := sha256.Sum256(raw)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != rec.Payload.ManifestSha256 {
		t.Errorf("sha256 of the decoded manifest = %q, want it to equal manifestSha256 %q", got, rec.Payload.ManifestSha256)
	}
}

var sha256FieldPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func TestGoldenDigestFields_AreShapedLikeSha256(t *testing.T) {
	valid, err := provenance.ValidGoldens()
	if err != nil {
		t.Fatalf("ValidGoldens: %v", err)
	}
	invalid, err := provenance.InvalidGoldens()
	if err != nil {
		t.Fatalf("InvalidGoldens: %v", err)
	}

	all := map[string][]byte{}
	for name, raw := range valid {
		all[name] = raw
	}
	for name, raw := range invalid {
		all[name] = raw
	}
	if len(all) == 0 {
		t.Fatal("no goldens found to check")
	}

	for name, raw := range all {
		var r provenance.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("golden %q is not valid Record JSON: %v", name, err)
		}
		fields := map[string]string{
			"imageDigest":               r.Payload.ImageDigest,
			"manifestSha256":            r.Payload.ManifestSha256,
			"conformance.resultsDigest": r.Payload.Conformance.ResultsDigest,
		}
		for field, value := range fields {
			if !sha256FieldPattern.MatchString(value) {
				t.Errorf("golden %q: %s = %q, want it to match ^sha256:[0-9a-f]{64}$", name, field, value)
			}
		}
	}
}

func TestGoldenRecord_CarriesEveryAcceptanceCriterionField(t *testing.T) {
	rec := loadGolden(t, "record.golden.json")

	if rec.Payload.Manifest == "" {
		t.Error("golden must carry the manifest bytes")
	}
	if rec.Payload.ManifestSha256 == "" {
		t.Error("golden must carry the manifest's sha256")
	}
	if rec.Payload.KitVersion == "" {
		t.Error("golden must carry the kit version")
	}
	if rec.Payload.Conformance.Verdict == "" || rec.Payload.Conformance.SuiteVersion == "" || rec.Payload.Conformance.ResultsDigest == "" {
		t.Error("golden must carry the conformance result")
	}
	if rec.Payload.SourceCommit == "" {
		t.Error("golden must carry the source commit")
	}
}

// Decode accepts reordered keys and reformatted whitespace on purpose, because an intermediary may reformat a record and the audience already covers every value.

// loadGoldenRaw returns raw bytes because json.Unmarshal into Record would drop the very field under test.
func loadGoldenRaw(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := provenance.Goldens.ReadFile(provenance.GoldensDir + "/" + name)
	if err != nil {
		t.Fatalf("reading golden %q: %v", name, err)
	}
	return raw
}

// injectField treats an empty path as the record's own top level.
func injectField(t *testing.T, raw []byte, path, key string, value interface{}) []byte {
	t.Helper()
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding fixture generically: %v", err)
	}
	target := doc
	if path != "" {
		for _, segment := range strings.Split(path, ".") {
			next, ok := target[segment].(map[string]interface{})
			if !ok {
				t.Fatalf("fixture has no object at %q", segment)
			}
			target = next
		}
	}
	target[key] = value
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding mutated fixture: %v", err)
	}
	return out
}

func TestDecode_AcceptsTheGoldenRecordUnchanged(t *testing.T) {
	want := loadGolden(t, "record.golden.json")
	rec, err := provenance.Decode(loadGoldenRaw(t, "record.golden.json"))
	if err != nil {
		t.Fatalf("Decode(golden) = %v, want nil — strictness must not break the artefact both halves share", err)
	}
	if rec != want {
		t.Errorf("Decode(golden) = %+v, want %+v", rec, want)
	}
}

func TestDecode_RejectsUnknownFieldAtPayloadTopLevel(t *testing.T) {
	raw := injectField(t, loadGoldenRaw(t, "record.golden.json"), "payload", "evilExtraField", "attacker-controlled")
	if _, err := provenance.Decode(raw); !errors.Is(err, provenance.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField for an unknown key at payload's top level", err)
	}
}

func TestDecode_RejectsUnknownFieldInsideConformance(t *testing.T) {
	raw := injectField(t, loadGoldenRaw(t, "record.golden.json"), "payload.conformance", "evilExtraField", "attacker-controlled")
	if _, err := provenance.Decode(raw); !errors.Is(err, provenance.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField for an unknown key inside conformance", err)
	}
}

func TestDecode_RejectsUnknownFieldAtRecordTopLevel(t *testing.T) {
	raw := injectField(t, loadGoldenRaw(t, "record.golden.json"), "", "evilExtraField", "attacker-controlled")
	if _, err := provenance.Decode(raw); !errors.Is(err, provenance.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField for an unknown key beside payload and token", err)
	}
}

func TestDecode_RejectsTheUnknownFieldGolden(t *testing.T) {
	raw := loadGoldenRaw(t, "invalid-unknown-field.json")
	if _, err := provenance.Decode(raw); !errors.Is(err, provenance.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField", err)
	}
}

func TestDecode_AcceptsReorderedKeysAndExtraWhitespace(t *testing.T) {
	want := loadGolden(t, "record.golden.json")
	payloadJSON, err := json.Marshal(want.Payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}
	reordered := []byte(fmt.Sprintf("{\n\n  \"token\":   %q  ,\n\n  \"payload\":\n%s\n\n}\n", want.Token, payloadJSON))

	rec, err := provenance.Decode(reordered)
	if err != nil {
		t.Fatalf("Decode must accept reordered keys and extra whitespace when every field is one the schema declares: %v", err)
	}
	if rec != want {
		t.Errorf("Decode(reordered) = %+v, want %+v", rec, want)
	}
}
