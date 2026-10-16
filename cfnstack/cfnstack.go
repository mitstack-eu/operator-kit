// Package cfnstack, an optional helper, drives CloudFormation stacks over a Client of stdlib types,
// which an operator adapts its own AWS SDK client to. A create error whose
// ErrorCode is AlreadyExistsException, as the SDK's typed exception reports,
// turns into an update.
package cfnstack

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const CapabilityNamedIAM = "CAPABILITY_NAMED_IAM"

const alreadyExistsCode = "AlreadyExistsException"

type Provisioner interface {
	Validate(ctx context.Context, templateBody string) error
	EnsureStack(ctx context.Context, name, templateBody string, params, tags map[string]string) error
	DeleteStack(ctx context.Context, name string) error
}

type StackInput struct {
	Name         string
	TemplateBody string
	Parameters   map[string]string
	Tags         map[string]string
	Capabilities []string
}

type Stack struct {
	Name   string
	Status string
}

// Client is the CloudFormation API an operator adapts its SDK client to.
// An adapter returns the SDK's errors as they are or wrapped with %w, since a
// wrapper with its own ErrorCode hides AlreadyExistsException from EnsureStack.
type Client interface {
	ValidateTemplate(ctx context.Context, templateBody string) error
	CreateStack(ctx context.Context, in StackInput) error
	UpdateStack(ctx context.Context, in StackInput) error
	DeleteStack(ctx context.Context, name string) error
	DescribeStacks(ctx context.Context, name string) ([]Stack, error)
}

type Stacks struct {
	client Client
}

func New(c Client) *Stacks {
	return &Stacks{client: c}
}

func (s *Stacks) Validate(ctx context.Context, templateBody string) error {
	return s.client.ValidateTemplate(ctx, templateBody)
}

// EnsureStack creates the stack, or updates it when it already exists; an
// update with nothing to change succeeds.
func (s *Stacks) EnsureStack(ctx context.Context, name, templateBody string, params, tags map[string]string) error {
	in := StackInput{
		Name:         name,
		TemplateBody: templateBody,
		Parameters:   params,
		Tags:         tags,
		Capabilities: []string{CapabilityNamedIAM},
	}
	err := s.client.CreateStack(ctx, in)
	if err == nil || !isAlreadyExists(err) {
		return err
	}
	err = s.client.UpdateStack(ctx, in)
	if err != nil && strings.Contains(err.Error(), "No updates are to be performed") {
		return nil
	}
	return err
}

// DeleteStack removes the stack; a stack that does not exist succeeds.
func (s *Stacks) DeleteStack(ctx context.Context, name string) error {
	err := s.client.DeleteStack(ctx, name)
	if err != nil && strings.Contains(err.Error(), "does not exist") {
		return nil
	}
	return err
}

func (s *Stacks) StackStatus(ctx context.Context, name string) (string, error) {
	stacks, err := s.client.DescribeStacks(ctx, name)
	if err != nil {
		return "", err
	}
	if len(stacks) == 0 {
		return "", fmt.Errorf("cfnstack: DescribeStacks returned no stack for %s", name)
	}
	return stacks[0].Status, nil
}

func isAlreadyExists(err error) bool {
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == alreadyExistsCode
}
