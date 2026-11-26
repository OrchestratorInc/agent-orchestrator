package sessionmanager

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// MaxTransferArchiveBytes caps the one file a move carries, the git bundle and
// the conversation together, under the 100 MB a tunnel takes in one request.
const MaxTransferArchiveBytes int64 = 90 << 20

// ErrTransferTooLarge refuses unpushed work larger than one move carries.
var ErrTransferTooLarge = errors.New("the unpushed work is larger than a move carries")

const (
	transferBundleName       = "git.bundle"
	transferConversationName = "conversation.tgz"
	transferArchiveName      = "transfer.tar"
)

// packTransfer puts the bundle and the conversation that dir holds into one
// tar, leaving the conversation out when both do not fit. It returns the
// archive's description, nil when there is nothing to carry, and whether the
// conversation went in.
func packTransfer(dir string) (*domain.SessionTransferArchive, bool, error) {
	var names []string
	for _, name := range []string{transferBundleName, transferConversationName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, false, nil
	}
	archive := filepath.Join(dir, transferArchiveName)
	for {
		size, err := writeTransferTar(dir, archive, names)
		if err != nil {
			return nil, false, err
		}
		if size <= MaxTransferArchiveBytes {
			break
		}
		if names[len(names)-1] != transferConversationName {
			return nil, false, fmt.Errorf("%w: %d MB", ErrTransferTooLarge, size>>20)
		}
		names = names[:len(names)-1]
		if len(names) == 0 {
			_ = os.Remove(archive)
			return nil, false, nil
		}
	}
	size, sum, err := fileDigest(archive)
	if err != nil {
		return nil, false, err
	}
	for _, name := range []string{transferBundleName, transferConversationName} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return &domain.SessionTransferArchive{Size: size, SHA256: sum}, names[len(names)-1] == transferConversationName, nil
}

func writeTransferTar(dir, archive string, names []string) (int64, error) {
	out, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(out)
	err = func() error {
		for _, name := range names {
			f, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			info, err := f.Stat()
			if err == nil {
				err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), Typeflag: tar.TypeReg})
			}
			if err == nil {
				_, err = io.Copy(tw, f)
			}
			_ = f.Close()
			if err != nil {
				return err
			}
		}
		return tw.Close()
	}()
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// unpackTransfer writes the bundle and the conversation an archive holds into
// dir. Any other entry fails it.
func unpackTransfer(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrTransferArchive, err)
		}
		if (hdr.Name != transferBundleName && hdr.Name != transferConversationName) || seen[hdr.Name] || hdr.Typeflag != tar.TypeReg || hdr.Size > MaxTransferArchiveBytes {
			return fmt.Errorf("%w: unexpected entry %q", ErrTransferArchive, hdr.Name)
		}
		seen[hdr.Name] = true
		out, err := os.OpenFile(filepath.Join(dir, hdr.Name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // hdr.Name is one of the two names checked above
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, hdr.Size))
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
}
