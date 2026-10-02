package persistenthost

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func confirmManagedHostStops(dataDir, sessionID string) error {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "provider-owners"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		identity, proof := strings.CutSuffix(entry.Name(), ".json")
		if !proof {
			continue
		}
		owner, err := readProviderOwner(dataDir, sessionID, identity)
		if err != nil {
			return err
		}
		if owner.HostProtocol != ProtocolManagedRaw {
			continue
		}
		if stopped, err := hostStopped(dataDir, sessionID, identity); err != nil || !stopped {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
	}
	return nil
}

// This receipt joins the host's direct provider only. It is never retirement proof.
func hostStopped(dataDir, sessionID, identity string) (bool, error) {
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return false, err
	}
	path += ".host-stopped"
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return false, errors.Join(ErrOwnershipInconclusive, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false, errors.Join(ErrOwnershipInconclusive, err)
	}
	contents, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil || string(contents) != "host-stopped-v1:"+identity+"\n" {
		return false, errors.Join(ErrOwnershipInconclusive, err)
	}
	return true, nil
}

func recordHostStopped(dataDir, sessionID, identity string) error {
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".host-stopped-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, err = file.WriteString("host-stopped-v1:" + identity + "\n")
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path+".host-stopped"); err != nil {
		return err
	}
	return syncProviderOwnerDirectory(filepath.Dir(path))
}

func finishUnstartedProvider(dataDir string, owner providerOwner) error {
	if owner.HostProtocol != ProtocolManagedRaw {
		return nil
	}
	return recordHostStopped(dataDir, owner.Session, owner.Identity)
}
