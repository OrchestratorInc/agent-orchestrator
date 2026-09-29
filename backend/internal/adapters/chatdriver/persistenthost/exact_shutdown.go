package persistenthost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"
)

// HostIdentity can be persisted as ownership evidence, but cannot authorize a connection.
func (t *Transport) HostIdentity() string { return t.identity }

func descriptorIdentity(d Descriptor) string {
	sum := sha256.Sum256([]byte(d.Token))
	return hex.EncodeToString(sum[:])
}

// ShutdownExact requires launch-specific provider death proof. Reusable host
// files cannot prove that a retired host's descendants stopped.
func ShutdownExact(ctx context.Context, dataDir, sessionID, identity string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if identity == "" {
		if _, err := readDescriptor(dataDir, sessionID); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(ErrOwnershipInconclusive, err)
		}
		if err := confirmHostAbsent(dataDir, sessionID); err != nil {
			return err
		}
		return confirmProviderOwnersStopped(dataDir, sessionID)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return withProviderOwnerLock(ctx, dataDir, sessionID, identity, func() error {
		owner, err := readProviderOwner(dataDir, sessionID, identity)
		if err != nil {
			return err
		}
		if owner.State == "stopped" {
			return confirmProviderOwnerStopped(owner)
		}
		if owner.State != "active" {
			return ErrOwnershipInconclusive
		}
		if requestExactHostShutdown(ctx, dataDir, sessionID, identity) {
			grace := time.NewTimer(3500 * time.Millisecond)
			defer grace.Stop()
			for {
				stopped, err := providerGroupStopped(owner)
				if err != nil {
					return err
				}
				if stopped {
					owner.State = "stopped"
					return writeProviderOwner(dataDir, owner)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-grace.C:
					return stopProviderOwner(ctx, dataDir, owner)
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		return stopProviderOwner(ctx, dataDir, owner)
	})
}

func requestExactHostShutdown(ctx context.Context, dataDir, sessionID, identity string) bool {
	d, err := readDescriptor(dataDir, sessionID)
	if err != nil || d.Version != ProtocolVersion || descriptorIdentity(d) != identity {
		return false
	}
	host, _, err := net.SplitHostPort(d.Address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.Address)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	finish := bindConnToContext(ctx, conn)
	defer func() { _ = finish(nil) }()
	if err := json.NewEncoder(conn).Encode(hello{Version: ProtocolVersion, Token: d.Token, Action: "shutdown"}); err != nil {
		return false
	}
	var response helloResponse
	return json.NewDecoder(conn).Decode(&response) == nil && response.OK
}

func confirmHostAbsent(dataDir, sessionID string) error {
	path, err := lockPath(dataDir, sessionID)
	if err != nil {
		return err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return ErrOwnershipInconclusive
}
