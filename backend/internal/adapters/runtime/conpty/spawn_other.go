//go:build !windows && !darwin && !linux

package conpty

import (
	"context"
	"errors"
)

func defaultSpawnHost(_ context.Context, _, _ string, _ []string, _ map[string]string, _ HostOptions) (string, int, error) {
	return "", 0, errors.New("conpty spawn: unsupported on this OS")
}
