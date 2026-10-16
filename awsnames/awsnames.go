// Package awsnames, an optional helper, is the single source of the account-unique AWS names every app-infra engine creates, so a naming mismatch between engines shows up in one place.
package awsnames

import "strings"

func Prefix(appName string) string { return "mitstack-app-" + strings.TrimSpace(appName) }

// ECRRepository is rebuilt rather than carried on a switch, since deleting it destroys every image pushed to it.
func ECRRepository(appName string) string { return Prefix(appName) }

func InstanceRole(appName string) string { return Prefix(appName) + "-instance" }

// InstanceProfile shares the instance role's name, since an instance profile is a separate account-unique resource.
func InstanceProfile(appName string) string { return InstanceRole(appName) }

// DeployRole is the role managed-cd assumes to deploy into the app's account.
func DeployRole(appName string) string { return Prefix(appName) + "-deploy" }

// Colliding lists every account-unique name for the app, in a stable order, which a stack switch waits on before the target stack creates its own copy.
func Colliding(appName string) []string {
	return []string{
		ECRRepository(appName),
		InstanceRole(appName),
		DeployRole(appName),
	}
}
