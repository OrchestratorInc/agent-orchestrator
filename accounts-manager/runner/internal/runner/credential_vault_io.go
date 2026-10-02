package runner

import (
	"encoding/json"
	"errors"
	"io"
	"os"
)

func (v *credentialVault) openPrivateFile(name string, create bool) (*os.File, error) {
	info, err := v.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		file, err := v.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if err := protectVaultFile(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errCredentialStorage
	}
	file, err := v.root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) || validateVaultFile(file, false) != nil {
		_ = file.Close()
		return nil, errCredentialStorage
	}
	return file, nil
}

func (v *credentialVault) readFile(name string, limit int64) ([]byte, error) {
	file, err := v.openPrivateFile(name, false)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || int64(len(raw)) > limit {
		clear(raw)
		return nil, errCredentialStorage
	}
	return raw, nil
}

func (v *credentialVault) writeNewFile(name string, raw []byte) error {
	file, err := v.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errCredentialStorage
	}
	// Successful writes check Close before syncing the directory; failures still release the descriptor.
	defer func() { _ = file.Close() }()
	if err := protectVaultFile(file); err != nil {
		return errCredentialStorage
	}
	if _, err := file.Write(raw); err != nil {
		return errCredentialStorage
	}
	if err := file.Sync(); err != nil {
		return errCredentialStorage
	}
	if err := file.Close(); err != nil {
		return errCredentialStorage
	}
	if err := syncVaultDirectory(v.root); err != nil {
		return errCredentialStorage
	}
	return nil
}

func (v *credentialVault) persistLocked(next vaultState) (resultErr error) {
	plain, err := json.Marshal(next)
	if err != nil || len(plain) > vaultMaxBytes-64 {
		return errCredentialStorage
	}
	defer clear(plain)
	sealed, err := v.seal(plain, []byte("ao-credential-vault-v1"))
	if err != nil {
		return err
	}
	id, err := randomVaultID()
	if err != nil {
		return err
	}
	tempName := "credentials-" + id + ".pending"
	defer func() {
		if err := v.root.Remove(tempName); err != nil && !errors.Is(err, os.ErrNotExist) {
			v.failed = true
			resultErr = errCredentialStorage
		}
	}()
	if err := v.writeNewFile(tempName, sealed); err != nil {
		return err
	}
	if existing, err := v.openPrivateFile(vaultFileName, false); err == nil {
		_ = existing.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return errCredentialStorage
	}
	if err := v.root.Rename(tempName, vaultFileName); err != nil {
		return errCredentialStorage
	}
	if err := syncVaultDirectory(v.root); err != nil {
		// The rename may have committed. Do not admit work on an older memory snapshot.
		v.failed = true
		return errCredentialStorage
	}
	v.state = next
	return nil
}
