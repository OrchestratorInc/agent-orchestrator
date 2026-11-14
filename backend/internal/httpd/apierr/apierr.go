package apierr

type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindForbidden
	KindTooManyRequests
	KindNotImplemented
	KindUnavailable
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func New(kind Kind, code, message string, details map[string]any) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Details: details}
}

func Invalid(code, message string, details map[string]any) *Error {
	return New(KindInvalid, code, message, details)
}

func NotFound(code, message string) *Error {
	return New(KindNotFound, code, message, nil)
}

func Conflict(code, message string, details map[string]any) *Error {
	return New(KindConflict, code, message, details)
}

func Forbidden(code, message string) *Error {
	return New(KindForbidden, code, message, nil)
}

func TooManyRequests(code, message string) *Error {
	return New(KindTooManyRequests, code, message, nil)
}

func NotImplemented(code, message string) *Error {
	return New(KindNotImplemented, code, message, nil)
}

func Unavailable(code, message string) *Error {
	return New(KindUnavailable, code, message, nil)
}

func Internal(code, message string) *Error {
	return New(KindInternal, code, message, nil)
}

func InternalCause(code, message string, cause error) *Error {
	e := Internal(code, message)
	e.Cause = cause
	return e
}
