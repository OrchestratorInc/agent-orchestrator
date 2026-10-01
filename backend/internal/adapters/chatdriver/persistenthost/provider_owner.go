package persistenthost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
)

var errProviderContainmentRequired = errors.New("provider retirement requires a contained execution boundary")

type providerProcessIdentity struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start"`
}

type providerOwner struct {
	Version      int                       `json:"version"`
	Session      string                    `json:"session"`
	Identity     string                    `json:"identity"`
	State        string                    `json:"state"`
	Boot         string                    `json:"boot,omitempty"`
	Group        int                       `json:"group,omitempty"`
	SessionID    int                       `json:"processSession,omitempty"`
	Members      []providerProcessIdentity `json:"members,omitempty"`
	Proof        string                    `json:"proof,omitempty"`
	HostProtocol Protocol                  `json:"hostProtocol,omitempty"`
}

func providerOwnerPath(dataDir, sessionID, identity string) (string, error) {
	decoded, err := hex.DecodeString(identity)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != identity {
		return "", ErrOwnershipInconclusive
	}
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "provider-owners", identity+".json"), nil
}

func beginProviderOwner(dataDir, sessionID, identity string, protocol ...Protocol) (providerOwner, error) {
	owner := providerOwner{Version: 1, Session: sessionID, Identity: identity, State: "starting"}
	if len(protocol) != 0 && protocol[0] == ProtocolManagedRaw {
		owner.HostProtocol = ProtocolManagedRaw
	}
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return owner, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return owner, err
	}
	if err := syncProviderOwnerDirectory(filepath.Dir(filepath.Dir(path))); err != nil {
		return owner, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return owner, err
	}
	err = json.NewEncoder(file).Encode(owner)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err == nil {
		err = syncProviderOwnerDirectory(filepath.Dir(path))
	}
	return owner, err
}

func readProviderOwner(dataDir, sessionID, identity string) (providerOwner, error) {
	path, err := providerOwnerPath(dataDir, sessionID, identity)
	if err != nil {
		return providerOwner{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return providerOwner{}, errors.Join(ErrOwnershipInconclusive, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return providerOwner{}, errors.Join(ErrOwnershipInconclusive, err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return providerOwner{}, errors.Join(ErrOwnershipInconclusive, err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return providerOwner{}, errors.Join(ErrOwnershipInconclusive, err)
	}
	if err := validateProviderOwnerJSON(raw); err != nil {
		return providerOwner{}, err
	}
	var owner providerOwner
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return owner, errors.Join(ErrOwnershipInconclusive, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return owner, ErrOwnershipInconclusive
	}
	if owner.Version != 1 || owner.Session != sessionID || owner.Identity != identity || !validProviderOwnerProof(owner) {
		return owner, ErrOwnershipInconclusive
	}
	return owner, nil
}

func validProviderOwnerProof(owner providerOwner) bool {
	if owner.HostProtocol != "" && owner.HostProtocol != ProtocolManagedRaw {
		return false
	}
	if owner.State == "starting" {
		return owner.Boot == "" && owner.Group == 0 && owner.SessionID == 0 && len(owner.Members) == 0 && owner.Proof == ""
	}
	if owner.State != "active" && owner.State != "stopped" {
		return false
	}
	if owner.Group <= 1 || len(owner.Members) == 0 || owner.Members[0].PID != owner.Group {
		return false
	}
	if owner.Proof == "windows-job-v1" {
		if owner.Boot != "" || owner.SessionID != 0 || len(owner.Members) != 1 || int64(owner.Group) > math.MaxUint32 {
			return false
		}
	} else {
		boot, err := uuid.Parse(owner.Boot)
		if owner.Proof != "" || err != nil || boot == uuid.Nil || owner.SessionID <= 0 {
			return false
		}
	}
	seen := make(map[int]bool, len(owner.Members))
	for _, member := range owner.Members {
		if member.PID <= 1 || member.Start == 0 || seen[member.PID] {
			return false
		}
		seen[member.PID] = true
	}
	return true
}

func writeProviderOwner(dataDir string, owner providerOwner) error {
	path, err := providerOwnerPath(dataDir, owner.Session, owner.Identity)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".provider-owner-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	err = json.NewEncoder(file).Encode(owner)
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncProviderOwnerDirectory(filepath.Dir(path))
}

func syncProviderOwnerDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func confirmProviderOwnersStopped(dataDir, sessionID string) error {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "provider-owners"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		identity := entry.Name()[:len(entry.Name())-len(".json")]
		owner, err := readProviderOwner(dataDir, sessionID, identity)
		if err != nil || owner.State != "stopped" {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		if err := confirmProviderOwnerStopped(owner); err != nil {
			return err
		}
	}
	return nil
}

func confirmProviderOwnerStopped(owner providerOwner) error {
	stopped, err := providerGroupStopped(owner)
	if err != nil || !stopped {
		return errors.Join(ErrOwnershipInconclusive, err)
	}
	return nil
}

func finishProviderOwner(ctx context.Context, dataDir string, owner providerOwner) error {
	if !providerOwnerProofSupported {
		return nil
	}
	return withProviderOwnerLock(ctx, dataDir, owner.Session, owner.Identity, func() error {
		current, err := readProviderOwner(dataDir, owner.Session, owner.Identity)
		if err != nil {
			return err
		}
		if current.State == "stopped" {
			return confirmProviderOwnerStopped(current)
		}
		for {
			stopped, err := providerGroupStopped(current)
			if err != nil {
				return errors.Join(ErrOwnershipInconclusive, err)
			}
			if stopped {
				current.State = "stopped"
				return writeProviderOwner(dataDir, current)
			}
			select {
			case <-ctx.Done():
				return errors.Join(ErrOwnershipInconclusive, ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	})
}
