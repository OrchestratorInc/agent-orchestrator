//go:build !darwin

package skillbridge

import "errors"

func cloneFile(string, string) error { return errors.ErrUnsupported }
