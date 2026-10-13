package ghapi_test

// revision_sha_test.go pins that every call taking a revision accepts only a
// full commit SHA, 40 lowercase hex digits for SHA-1 or 64 for SHA-256, and
// refuses anything else before any request. url.PathEscape leaves "." and
// ".." as they are, so without the check a revision of ".." reaches
// /repos/acme/gitops/commits/.., which a path-normalising server answers as
// the repository itself. An empty parent stays valid for CommitChanges,
// where it means "create main".

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

// Full commit SHAs the revision tests address commits by.
const (
	shaRev1    = "ef663767a3d60390831d0518763e341592dcbcb3"
	shaRev2    = "ec31b1e77e397c40f195af031457e9f5ac78ddb4"
	shaParent  = "edfab415e1ed0dd187f07d1ba43f677bdbbbc67e"
	shaUnknown = "4a88c839bf17a3e423b10408b6c056b2dd61d950"
	sha256Rev  = "f807e527f163f8bc44c4b002454a7f0236e520c97386e8c31a2924c81cc26d22"
)

// invalidRevisions are revisions that are not a full lowercase hex SHA.
var invalidRevisions = map[string]string{
	"dot-dot":                         "..",
	"dot":                             ".",
	"empty":                           "",
	"a branch name":                   "main",
	"a short hex prefix":              "abc",
	"39 hex digits":                   shaRev1[:39],
	"41 hex digits":                   shaRev1 + "a",
	"an uppercase SHA":                strings.ToUpper(shaRev1),
	"40 digits with a non-hex letter": "g" + shaRev1[1:],
	"a query injection":               "rev-1&ref=evil",
	"a path climb":                    shaRev1 + "/../../orgs",
}

// revisionCalls are the ghapi calls that take a revision, each calling with
// rev as that revision.
var revisionCalls = map[string]func(c *ghapi.Client, rev string) error{
	"ReadFileAt": func(c *ghapi.Client, rev string) error {
		_, _, err := c.ReadFileAt(context.Background(), revRepo, rev, "a.yaml")
		return err
	},
	"CommitTree": func(c *ghapi.Client, rev string) error {
		_, err := c.CommitTree(context.Background(), revRepo, rev)
		return err
	},
	"CommitParentAndPaths": func(c *ghapi.Client, rev string) error {
		_, _, err := c.CommitParentAndPaths(context.Background(), revRepo, rev)
		return err
	},
	"CommitChanges": func(c *ghapi.Client, rev string) error {
		_, err := c.CommitChanges(context.Background(), revRepo, rev, "m", map[string]*string{"a.yaml": strp("a\n")})
		return err
	},
}

// anythingServer answers every request 200 with a body every revision call
// decodes, so only a refusal before the request can make a call fail, and
// counts the requests it receives.
func anythingServer(t *testing.T) (*ghapi.Client, func() int) {
	t.Helper()
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"` + shaRev2 + `","tree":{"sha":"` + shaRev2 + `"},"object":{"sha":"` + shaRev2 + `"},` +
			`"parents":[],"files":[],"type":"file","encoding":"base64","content":""}`))
	}))
	t.Cleanup(srv.Close)
	return newTestClient(srv, revToken), func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
}

func TestRevisionCalls_RefuseARevisionThatIsNotAFullLowercaseHexSHABeforeAnyRequest(t *testing.T) {
	for name, call := range revisionCalls {
		for label, rev := range invalidRevisions {
			if name == "CommitChanges" && rev == "" {
				continue
			}
			t.Run(name+"/"+label, func(t *testing.T) {
				c, requests := anythingServer(t)

				if err := call(c, rev); err == nil {
					t.Errorf("%s with revision %q = nil error, want the revision refused", name, rev)
				}
				if n := requests(); n != 0 {
					t.Errorf("%s with revision %q made %d request(s), want 0", name, rev, n)
				}
			})
		}
	}
}

func TestRevisionCalls_AcceptAFortyOrSixtyFourDigitLowercaseHexSHA(t *testing.T) {
	for name, call := range revisionCalls {
		for _, rev := range []string{shaRev1, sha256Rev} {
			t.Run(name+"/"+rev, func(t *testing.T) {
				c, requests := anythingServer(t)

				_ = call(c, rev)
				if requests() == 0 {
					t.Errorf("%s with revision %q made no request, want a full SHA accepted", name, rev)
				}
			})
		}
	}
}
