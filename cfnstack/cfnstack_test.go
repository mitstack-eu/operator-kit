package cfnstack_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/cfnstack"
)

var _ cfnstack.Provisioner = (*cfnstack.Stacks)(nil)

const (
	stackName    = "mitstack-sub-demo"
	templateBody = "AWSTemplateFormatVersion: '2010-09-09'\nResources: {}\n"
)

type fakeClient struct {
	validateErr error
	createErr   error
	updateErr   error
	deleteErr   error
	describeErr error
	stacks      []cfnstack.Stack

	validated []string
	created   []cfnstack.StackInput
	updated   []cfnstack.StackInput
	deleted   []string
	described []string
}

func (f *fakeClient) ValidateTemplate(_ context.Context, body string) error {
	f.validated = append(f.validated, body)
	return f.validateErr
}

func (f *fakeClient) CreateStack(_ context.Context, in cfnstack.StackInput) error {
	f.created = append(f.created, in)
	return f.createErr
}

func (f *fakeClient) UpdateStack(_ context.Context, in cfnstack.StackInput) error {
	f.updated = append(f.updated, in)
	return f.updateErr
}

func (f *fakeClient) DeleteStack(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return f.deleteErr
}

func (f *fakeClient) DescribeStacks(_ context.Context, name string) ([]cfnstack.Stack, error) {
	f.described = append(f.described, name)
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return f.stacks, nil
}

// codedError stands in for the SDK's typed API errors, such as types.AlreadyExistsException.
type codedError struct{ code, msg string }

func (e *codedError) Error() string     { return e.code + ": " + e.msg }
func (e *codedError) ErrorCode() string { return e.code }

func alreadyExists() *codedError {
	return &codedError{code: "AlreadyExistsException", msg: "Stack [" + stackName + "] already exists"}
}

func operationError(op string, err error) error {
	return fmt.Errorf("operation error CloudFormation: %s, https response error StatusCode: 400, RequestID: x, %w", op, err)
}

func params() map[string]string {
	return map[string]string{"AppName": "demo", "InstanceType": "t3.small"}
}

func tags() map[string]string {
	return map[string]string{"mitstack:subscription": "sub_demo"}
}

func wantInput(p, tg map[string]string) cfnstack.StackInput {
	return cfnstack.StackInput{
		Name:         stackName,
		TemplateBody: templateBody,
		Parameters:   p,
		Tags:         tg,
		Capabilities: []string{cfnstack.CapabilityNamedIAM},
	}
}

func TestCapabilityNamedIAM_IsCloudFormationsName(t *testing.T) {
	if cfnstack.CapabilityNamedIAM != "CAPABILITY_NAMED_IAM" {
		t.Fatalf("CapabilityNamedIAM = %q, want CAPABILITY_NAMED_IAM", cfnstack.CapabilityNamedIAM)
	}
}

func TestValidate(t *testing.T) {
	invalid := errors.New("ValidationError: Template format error: unexpected token")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"it passes the template body and returns nil on success", nil},
		{"it returns the client's error", invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{validateErr: tc.err}

			err := cfnstack.New(c).Validate(context.Background(), templateBody)

			if !errors.Is(err, tc.err) || !reflect.DeepEqual(c.validated, []string{templateBody}) {
				t.Errorf("Validate = %v after ValidateTemplate saw %q; want %v after the template body once", err, c.validated, tc.err)
			}
		})
	}
}

func TestEnsureStack(t *testing.T) {
	stuck := errors.New("ValidationError: Stack is in UPDATE_IN_PROGRESS state and can not be updated")
	noUpdates := errors.New("operation error CloudFormation: UpdateStack, https response error StatusCode: 400, RequestID: x, ValidationError: No updates are to be performed.")
	type ensureCase struct {
		name                 string
		p, tags              map[string]string
		createErr, updateErr error
		wantErr              error
		wantUpdate           bool
	}
	cases := []ensureCase{
		{"a new stack is created with named IAM and never updated", params(), tags(), nil, nil, nil, false},
		{"a new stack with no params or tags is created", nil, nil, nil, nil, nil, false},
		{"a bare AlreadyExistsException falls back to an update with the same input", params(), tags(), alreadyExists(), nil, nil, true},
		{"an AlreadyExistsException wrapped once falls back to an update", params(), tags(), operationError("CreateStack", alreadyExists()), nil, nil, true},
		{"an AlreadyExistsException wrapped twice falls back to an update", params(), tags(), fmt.Errorf("ensure: %w", operationError("CreateStack", alreadyExists())), nil, nil, true},
		{"an update with no updates to perform is an already converged stack", params(), tags(), operationError("CreateStack", alreadyExists()), noUpdates, nil, true},
		{"any other update error is returned", params(), tags(), operationError("CreateStack", alreadyExists()), operationError("UpdateStack", stuck), stuck, true},
	}
	for _, err := range []struct {
		name string
		err  error
	}{
		{"no error code", errors.New("InsufficientCapabilitiesException: insufficient")},
		{"no error code naming AlreadyExistsException in its text", errors.New("AlreadyExistsException: Stack already exists")},
		{"another code", operationError("CreateStack", &codedError{code: "InsufficientCapabilitiesException", msg: "requires CAPABILITY_NAMED_IAM"})},
		{"a code containing AlreadyExists", operationError("CreateStack", &codedError{code: "TokenAlreadyExistsException", msg: "token in use"})},
		{"an empty code", operationError("CreateStack", &codedError{code: "", msg: "unknown"})},
		{"no updates text on create", errors.New("ValidationError: No updates are to be performed.")},
		{"does not exist text on create", errors.New("ValidationError: Stack with id " + stackName + " does not exist")},
	} {
		cases = append(cases, ensureCase{"a create error with " + err.name + " is returned and never updates", params(), tags(), err.err, nil, err.err, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{createErr: tc.createErr, updateErr: tc.updateErr}
			want := wantInput(tc.p, tc.tags)

			err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, tc.p, tc.tags)

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("EnsureStack = %v, want %v", err, tc.wantErr)
			}
			if len(c.created) != 1 || !reflect.DeepEqual(c.created[0], want) {
				t.Errorf("CreateStack inputs = %+v, want once %+v", c.created, want)
			}
			if wantUpdates := map[bool]int{true: 1}[tc.wantUpdate]; len(c.updated) != wantUpdates || (wantUpdates == 1 && !reflect.DeepEqual(c.updated[0], want)) {
				t.Errorf("UpdateStack inputs = %+v, want %d update(s) with the create's input", c.updated, wantUpdates)
			}
		})
	}
}

func TestDeleteStack(t *testing.T) {
	throttled := errors.New("Throttling: Rate exceeded")
	for _, tc := range []struct {
		name               string
		deleteErr, wantErr error
	}{
		{"it deletes the named stack", nil, nil},
		{"a stack that does not exist succeeds", fmt.Errorf("operation error CloudFormation: DeleteStack, ValidationError: Stack with id %s does not exist", stackName), nil},
		{"any other error is returned", operationError("DeleteStack", throttled), throttled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{deleteErr: tc.deleteErr}

			err := cfnstack.New(c).DeleteStack(context.Background(), stackName)

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) || !reflect.DeepEqual(c.deleted, []string{stackName}) {
				t.Errorf("DeleteStack = %v after deleting %q; want %v after %q once", err, c.deleted, tc.wantErr, stackName)
			}
		})
	}
}

func TestStackStatus(t *testing.T) {
	missing := fmt.Errorf("operation error CloudFormation: DescribeStacks, ValidationError: Stack with id %s does not exist", stackName)
	throttled := operationError("DescribeStacks", errors.New("Throttling: Rate exceeded"))
	errNoStack := errors.New("no stack")
	for _, tc := range []struct {
		name                 string
		stacks               []cfnstack.Stack
		describeErr, wantErr error
		want                 string
	}{
		{"it returns the first stack's status", []cfnstack.Stack{{Name: stackName, Status: "CREATE_COMPLETE"}, {Name: stackName, Status: "DELETE_FAILED"}}, nil, nil, "CREATE_COMPLETE"},
		{"a stack that does not exist reaches the caller", nil, missing, missing, ""},
		{"a throttled describe reaches the caller", nil, throttled, throttled, ""},
		{"a nil answer is an error", nil, nil, errNoStack, ""},
		{"an empty answer is an error", []cfnstack.Stack{}, nil, errNoStack, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{stacks: tc.stacks, describeErr: tc.describeErr}

			status, err := cfnstack.New(c).StackStatus(context.Background(), stackName)

			switch {
			case tc.wantErr == errNoStack && err == nil, tc.wantErr != errNoStack && !errors.Is(err, tc.wantErr), tc.wantErr == nil && err != nil:
				t.Errorf("StackStatus error = %v, want %v", err, tc.wantErr)
			case status != tc.want || !reflect.DeepEqual(c.described, []string{stackName}):
				t.Errorf("StackStatus = %q after describing %q; want %q after %q once", status, c.described, tc.want, stackName)
			}
		})
	}
}
