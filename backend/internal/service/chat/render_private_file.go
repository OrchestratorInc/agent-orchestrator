//go:build !windows

package chat

import (
	"context"
	"os"
)

// createRenderArtifact 通过 os.Root 独占创建文件，并在首次创建时应用 POSIX 0600。
func createRenderArtifact(ctx context.Context, root *os.Root, name string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
