package ownership_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe/ownership"
)

func TestOwnedErrorSurvivesMultipleWraps(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", ownership.Own(errors.New("boom"), ownership.OwnerHTTP)))
	if got := ownership.OwnerOf(err); got != ownership.OwnerHTTP {
		t.Fatalf("OwnerOf() = %q, want %q", got, ownership.OwnerHTTP)
	}
}

func TestOwnedErrorSurvivesErrorsJoin(t *testing.T) {
	err := errors.Join(errors.New("secondary"), ownership.Own(errors.New("boom"), ownership.OwnerHTTP))
	if got := ownership.OwnerOf(err); got != ownership.OwnerHTTP {
		t.Fatalf("OwnerOf() = %q, want %q", got, ownership.OwnerHTTP)
	}
}

func TestOwnDoesNotReplaceExistingOwner(t *testing.T) {
	err := ownership.Own(errors.New("boom"), ownership.OwnerHTTP)
	err = ownership.Own(err, ownership.OwnerHTTP)
	if got := ownership.OwnerOf(err); got != ownership.OwnerHTTP {
		t.Fatalf("OwnerOf() = %q, want original owner %q", got, ownership.OwnerHTTP)
	}
}

func TestOwnRejectsNilAndUnknownOwners(t *testing.T) {
	if got := ownership.Own(nil, ownership.OwnerHTTP); got != nil {
		t.Fatalf("Own(nil) = %v, want nil", got)
	}
	err := errors.New("boom")
	if got := ownership.Own(err, ownership.Owner("untrusted")); !errors.Is(got, err) {
		t.Fatalf("Own() with an unknown owner replaced the original error: %v", got)
	}
	if got := ownership.OwnerOf(err); got != "" {
		t.Fatalf("OwnerOf(unowned) = %q, want empty", got)
	}
}

func TestInvalidOuterOwnerDoesNotHideValidInnerOwner(t *testing.T) {
	err := invalidOwnedError{
		err: ownership.Own(errors.New("boom"), ownership.OwnerHTTP),
	}
	if got := ownership.OwnerOf(err); got != ownership.OwnerHTTP {
		t.Fatalf("OwnerOf() = %q, want inner owner %q", got, ownership.OwnerHTTP)
	}
}

func TestPreserveMovesOnlyOwnerToMappedError(t *testing.T) {
	originalCause := errors.New("private cause")
	original := ownership.Own(originalCause, ownership.OwnerHTTP)
	mappedCause := errors.New("public error")
	mapped := ownership.Preserve(original, mappedCause)

	if got := ownership.OwnerOf(mapped); got != ownership.OwnerHTTP {
		t.Fatalf("OwnerOf(mapped) = %q, want %q", got, ownership.OwnerHTTP)
	}
	if !errors.Is(mapped, mappedCause) {
		t.Fatal("mapped error is no longer discoverable through errors.Is")
	}
	if errors.Is(mapped, originalCause) {
		t.Fatal("Preserve retained the original private error chain")
	}
}

type invalidOwnedError struct{ err error }

func (e invalidOwnedError) Error() string { return e.err.Error() }
func (e invalidOwnedError) Unwrap() error { return e.err }
func (invalidOwnedError) ObservabilityOwner() ownership.Owner {
	return ownership.Owner("untrusted")
}
