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

func TestValidate_PassesTheTemplateBodyAndReturnsNilOnSuccess(t *testing.T) {
	c := &fakeClient{}
	if err := cfnstack.New(c).Validate(context.Background(), templateBody); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	if !reflect.DeepEqual(c.validated, []string{templateBody}) {
		t.Errorf("ValidateTemplate saw %q, want the template body once", c.validated)
	}
}

func TestValidate_ReturnsTheClientError(t *testing.T) {
	wantErr := errors.New("ValidationError: Template format error: unexpected token")
	c := &fakeClient{validateErr: wantErr}
	err := cfnstack.New(c).Validate(context.Background(), "<bad-template>")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Validate = %v, want the client's error %v", err, wantErr)
	}
	if len(c.validated) != 1 {
		t.Errorf("ValidateTemplate called %d times, want once", len(c.validated))
	}
}

func TestEnsureStack_CreatesANewStackWithNamedIAMAndNoUpdate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p, tags map[string]string
	}{
		{"params and tags", params(), tags()},
		{"no params or tags", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{}
			if err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, tc.p, tc.tags); err != nil {
				t.Fatalf("EnsureStack = %v, want nil on a fresh create", err)
			}
			if len(c.created) != 1 {
				t.Fatalf("CreateStack called %d times, want once", len(c.created))
			}
			if want := wantInput(tc.p, tc.tags); !reflect.DeepEqual(c.created[0], want) {
				t.Errorf("CreateStack input = %+v, want %+v", c.created[0], want)
			}
			if len(c.updated) != 0 {
				t.Errorf("UpdateStack called %d times after a successful create, want none", len(c.updated))
			}
		})
	}
}

func TestEnsureStack_AlreadyExistsFallsBackToAnUpdateWithTheSameInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"bare", alreadyExists()},
		{"wrapped once", operationError("CreateStack", alreadyExists())},
		{"wrapped twice", fmt.Errorf("ensure: %w", operationError("CreateStack", alreadyExists()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{createErr: tc.err}
			if err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, params(), tags()); err != nil {
				t.Fatalf("EnsureStack = %v, want nil once the update succeeds", err)
			}
			if len(c.created) != 1 || len(c.updated) != 1 {
				t.Fatalf("CreateStack called %d and UpdateStack %d times, want once each", len(c.created), len(c.updated))
			}
			if want := wantInput(params(), tags()); !reflect.DeepEqual(c.updated[0], want) {
				t.Errorf("UpdateStack input = %+v, want the create's input %+v", c.updated[0], want)
			}
		})
	}
}

func TestEnsureStack_UpdateWithNoUpdatesToPerformReturnsNil(t *testing.T) {
	c := &fakeClient{
		createErr: operationError("CreateStack", alreadyExists()),
		updateErr: errors.New("operation error CloudFormation: UpdateStack, https response error StatusCode: 400, RequestID: x, ValidationError: No updates are to be performed."),
	}
	if err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, params(), tags()); err != nil {
		t.Fatalf("EnsureStack = %v, want nil when the stack is already converged", err)
	}
	if len(c.updated) != 1 {
		t.Errorf("UpdateStack called %d times, want once", len(c.updated))
	}
}

func TestEnsureStack_ReturnsAnyOtherUpdateError(t *testing.T) {
	sentinel := errors.New("ValidationError: Stack is in UPDATE_IN_PROGRESS state and can not be updated")
	c := &fakeClient{
		createErr: operationError("CreateStack", alreadyExists()),
		updateErr: operationError("UpdateStack", sentinel),
	}
	err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, params(), tags())
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnsureStack = %v, want the update's error %v", err, sentinel)
	}
}

func TestEnsureStack_ReturnsAnyOtherCreateErrorAndNeverUpdates(t *testing.T) {
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{createErr: tc.err}
			err := cfnstack.New(c).EnsureStack(context.Background(), stackName, templateBody, params(), tags())
			if !errors.Is(err, tc.err) {
				t.Fatalf("EnsureStack = %v, want the create's error %v", err, tc.err)
			}
			if len(c.updated) != 0 {
				t.Errorf("UpdateStack called %d times after a create error that is not AlreadyExistsException, want none", len(c.updated))
			}
		})
	}
}

func TestDeleteStack_DeletesTheNamedStack(t *testing.T) {
	c := &fakeClient{}
	if err := cfnstack.New(c).DeleteStack(context.Background(), stackName); err != nil {
		t.Fatalf("DeleteStack = %v, want nil", err)
	}
	if !reflect.DeepEqual(c.deleted, []string{stackName}) {
		t.Errorf("DeleteStack saw %q, want %q once", c.deleted, stackName)
	}
}

func TestDeleteStack_StackThatDoesNotExistReturnsNil(t *testing.T) {
	c := &fakeClient{deleteErr: fmt.Errorf("operation error CloudFormation: DeleteStack, ValidationError: Stack with id %s does not exist", stackName)}
	if err := cfnstack.New(c).DeleteStack(context.Background(), stackName); err != nil {
		t.Fatalf("DeleteStack = %v, want nil for a stack that does not exist", err)
	}
	if len(c.deleted) != 1 {
		t.Errorf("DeleteStack called %d times, want once", len(c.deleted))
	}
}

func TestDeleteStack_ReturnsAnyOtherError(t *testing.T) {
	sentinel := errors.New("Throttling: Rate exceeded")
	c := &fakeClient{deleteErr: operationError("DeleteStack", sentinel)}
	err := cfnstack.New(c).DeleteStack(context.Background(), stackName)
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeleteStack = %v, want the client's error %v", err, sentinel)
	}
}

func TestStackStatus_ReturnsTheFirstStacksStatus(t *testing.T) {
	c := &fakeClient{stacks: []cfnstack.Stack{
		{Name: stackName, Status: "CREATE_COMPLETE"},
		{Name: stackName, Status: "DELETE_FAILED"},
	}}
	status, err := cfnstack.New(c).StackStatus(context.Background(), stackName)
	if err != nil {
		t.Fatalf("StackStatus = %v, want nil", err)
	}
	if status != "CREATE_COMPLETE" {
		t.Errorf("StackStatus = %q, want CREATE_COMPLETE", status)
	}
	if !reflect.DeepEqual(c.described, []string{stackName}) {
		t.Errorf("DescribeStacks saw %q, want %q once", c.described, stackName)
	}
}

func TestStackStatus_ClientErrorReachesTheCaller(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"stack does not exist", fmt.Errorf("operation error CloudFormation: DescribeStacks, ValidationError: Stack with id %s does not exist", stackName)},
		{"throttled", operationError("DescribeStacks", errors.New("Throttling: Rate exceeded"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClient{describeErr: tc.err}
			status, err := cfnstack.New(c).StackStatus(context.Background(), stackName)
			if !errors.Is(err, tc.err) {
				t.Fatalf("StackStatus error = %v, want the client's error %v", err, tc.err)
			}
			if status != "" {
				t.Errorf("StackStatus = %q alongside an error, want empty", status)
			}
		})
	}
}

func TestStackStatus_NoStackIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stacks []cfnstack.Stack
	}{
		{"nil", nil},
		{"empty", []cfnstack.Stack{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, err := cfnstack.New(&fakeClient{stacks: tc.stacks}).StackStatus(context.Background(), stackName)
			if err == nil {
				t.Fatal("StackStatus returned no error when the client answered with no stack")
			}
			if status != "" {
				t.Errorf("StackStatus = %q alongside an error, want empty", status)
			}
		})
	}
}
