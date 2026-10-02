//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"os/exec"
)

type providerChild struct{ command *exec.Cmd }

func startProviderChild(ctx context.Context, command *exec.Cmd, dataDir string, owner *providerOwner) (*providerChild, error) {
	if err := command.Start(); err != nil {
		if receiptErr := finishUnstartedProvider(dataDir, *owner); receiptErr != nil {
			return nil, errors.Join(err, receiptErr)
		}
		return nil, err
	}
	child := &providerChild{command: command}
	err := captureProviderOwner(owner, command.Process.Pid)
	if err == nil {
		err = writeProviderOwner(dataDir, *owner)
	}
	if err != nil {
		_ = child.stop(context.WithoutCancel(ctx))
		_ = command.Wait()
		return nil, err
	}
	return child, nil
}

func (c *providerChild) stop(ctx context.Context) error {
	return killProviderProcess(ctx, c.command)
}

func (*providerChild) close() {}

func (c *providerChild) wait() error { return c.command.Wait() }
