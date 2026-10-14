package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

const (
	classNotACapability     = "not-a-capability"
	classIssuesNoCredential = "issues-no-credential"
)

// issue serves a capability whose Resource implements CredentialIssuer, and refuses every other block, so a capability that issues nothing needs no stub.
func (h *Handler) issue(ctx context.Context, req contract.Request, secrets []string) (int, contract.Response) {
	if !h.capability {
		return h.errorResponse(Refuse(classNotACapability,
			"only a type %s manifest issues an app a credential, and this block's manifest is not one", contract.ManifestTypeCapability), secrets)
	}
	issuer, ok := h.resource.(CredentialIssuer)
	if !ok {
		return h.errorResponse(Refuse(classIssuesNoCredential,
			"the capability implements no issue handler, so it issues app %q no credential", req.IssueAppID), secrets)
	}
	cred, err := issuer.IssueCredential(ctx, req.Desired(), req.IssueAppID)
	if err != nil {
		return h.errorResponse(err, secrets)
	}
	if err := checkIssued(cred, secrets); err != nil {
		return h.errorResponse(fmt.Errorf("handler: the capability's credential for app %q: %w", req.IssueAppID, err), secrets)
	}
	return http.StatusOK, contract.Response{Credential: &cred}
}

// checkIssued refuses a credential the app could not use, and one repeating a secret the request carried, since the capability's own credential never leaves it.
func checkIssued(cred contract.Credential, secrets []string) error {
	if len(cred.Values) == 0 {
		return errors.New("it holds no value")
	}
	if cred.ExpiresAt.IsZero() {
		return errors.New("it has no expiry")
	}
	for key, value := range cred.Values {
		if slices.Contains(secrets, value) {
			return fmt.Errorf("its value %q repeats a secret the request carried", key)
		}
	}
	return nil
}
