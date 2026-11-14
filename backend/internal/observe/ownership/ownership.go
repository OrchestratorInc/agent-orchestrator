package ownership

type Owner string

const (
	OwnerHTTP Owner = "http"
)

type OwnedError struct {
	Err   error
	Owner Owner
}

func (e *OwnedError) Error() string { return e.Err.Error() }

func (e *OwnedError) Unwrap() error { return e.Err }

func (e *OwnedError) ObservabilityOwner() Owner { return e.Owner }

type ownerCarrier interface {
	ObservabilityOwner() Owner
}

func Own(err error, owner Owner) error {
	if err == nil || !owner.Valid() || OwnerOf(err).Valid() {
		return err
	}
	return &OwnedError{Err: err, Owner: owner}
}

func OwnerOf(err error) Owner {
	if err == nil {
		return ""
	}
	if carrier, ok := err.(ownerCarrier); ok {
		owner := carrier.ObservabilityOwner()
		if owner.Valid() {
			return owner
		}
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if owner := OwnerOf(child); owner.Valid() {
				return owner
			}
		}
		return ""
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return OwnerOf(wrapped.Unwrap())
	}
	return ""
}

func Preserve(original, mapped error) error {
	if mapped == nil {
		return nil
	}
	return Own(mapped, OwnerOf(original))
}

func (owner Owner) Valid() bool {
	return owner == OwnerHTTP
}
