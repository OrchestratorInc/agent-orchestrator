//go:build !linux && !darwin && !windows

package persistenthost

import "context"

const providerOwnerProofSupported = false

func captureProviderOwner(owner *providerOwner, _ int) error {
	owner.State = "active"
	return nil
}

func providerGroupStopped(providerOwner) (bool, error) {
	return false, ErrOwnershipInconclusive
}

func stopProviderOwner(context.Context, string, providerOwner) error {
	return ErrOwnershipInconclusive
}

func withProviderOwnerLock(context.Context, string, string, string, func() error) error {
	return ErrOwnershipInconclusive
}
