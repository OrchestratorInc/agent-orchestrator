//go:build linux

package persistenthost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func namespacePermit(identity string) (string, error) {
	decoded, err := hex.DecodeString(identity)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != identity {
		return "", ErrOwnershipInconclusive
	}
	return "AO-NS-START/1 " + identity + "\n", nil
}

func awaitNamespacePermit(ctx context.Context, identity string, pipe *os.File) error {
	expected, err := namespacePermit(identity)
	if err != nil || pipe == nil {
		return ErrOwnershipInconclusive
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = pipe.Close()
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
		}
	}()
	message, err := io.ReadAll(io.LimitReader(pipe, int64(len(expected)+1)))
	if err != nil || string(message) != expected || ctx.Err() != nil {
		return errors.Join(ErrOwnershipInconclusive, err, ctx.Err())
	}
	return nil
}
