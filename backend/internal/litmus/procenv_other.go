//go:build !darwin && !linux

package litmus

import "errors"

func processEnviron(int) ([]string, error) {
	return nil, errors.ErrUnsupported
}
