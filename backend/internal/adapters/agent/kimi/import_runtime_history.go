package kimi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Older pre-release receipts lack prefix metadata. Recover it only from the
// unchanged selected source, reproducing both the original selection and the
// exact remapped copy fingerprint. Never bless the current live journal.
func recoverKimiRuntimeProof(ctx context.Context, home string, proof kimiImportProof) (kimiImportProof, error) {
	original, err := ReadImportedSession(ctx, proof.Source, proof.ID)
	if err != nil {
		return proof, err
	}
	if original.Fingerprint != proof.Selection {
		return proof, errors.New("kimi: cannot recover imported history proof from changed source")
	}
	mainDir := filepath.Join(home, "sessions", kimiWorkdirKey(proof.CWD), proof.ID, "agents", "main")
	state, err := remapKimiState(original.State, proof.CWD, mainDir)
	if err != nil {
		return proof, err
	}
	wire, err := remapKimiWire(original.Wire, proof.CWD)
	if err != nil {
		return proof, err
	}
	if kimiSnapshotFingerprint(state, wire, original.Blobs) != proof.Copied {
		return proof, errors.New("kimi: recovered native copy does not match original proof")
	}
	proof.WireBytes = int64(len(wire))
	proof.WireHash = fmt.Sprintf("%x", sha256.Sum256(wire))
	proof.Blobs = make(map[string]int64, len(original.Blobs))
	for key, data := range original.Blobs {
		proof.Blobs[key] = int64(len(data))
	}
	return proof, nil
}

// Reopen verifies the immutable imported prefix, not a new bounded snapshot of
// the entire live conversation. Native turns may grow the journal and media
// indefinitely; memory is bounded by one journal record and a copy buffer.
func verifyKimiRuntimeHistory(ctx context.Context, root *os.Root, proof kimiImportProof) (string, error) {
	fail := errors.New("kimi: imported native history or binding changed")
	if !kimiNativeSessionID.MatchString(proof.ID) || !filepath.IsAbs(proof.CWD) || proof.WireBytes <= 0 || proof.WireBytes > kimiImportBudget || !kimiRuntimeHash(proof.WireHash) || len(proof.Blobs) > kimiBlobCountBudget {
		return "", fail
	}
	base := filepath.Join("sessions", kimiWorkdirKey(proof.CWD), proof.ID)
	data, err := readKimiFile(ctx, root, filepath.Join(base, "state.json"), 1<<20)
	if err != nil {
		return "", err
	}
	// Kimi records every subagent the conversation starts in state.json, so a
	// continued import may hold more agents than it was copied with. Only the
	// main agent carries the imported history this proof covers.
	var state kimiImportState
	if json.Unmarshal(data, &state) != nil || state.ID != proof.ID || state.Version != 2 || state.Archived || state.CWD != proof.CWD {
		return "", fail
	}
	mainAgent, ok := state.Agents["main"]
	if !ok || mainAgent.Type != "main" {
		return "", fail
	}
	agentDir := filepath.Join(base, "agents", "main")
	expectedHome, err := filepath.EvalSymlinks(filepath.Join(root.Name(), agentDir))
	if err != nil {
		return "", err
	}
	recordedHome, err := filepath.EvalSymlinks(mainAgent.Home)
	if err != nil || recordedHome != expectedHome {
		return "", fail
	}
	wire, err := openKimiRegularFile(ctx, root, filepath.Join(agentDir, "wire.jsonl"))
	if err != nil {
		return "", err
	}
	defer func() { _ = wire.Close() }()
	st, err := wire.Stat()
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	if _, err := io.CopyN(sum, kimiRuntimeReader{ctx, wire}, proof.WireBytes); err != nil {
		return "", fmt.Errorf("kimi: missing imported journal prefix: %w", err)
	}
	if hex.EncodeToString(sum.Sum(nil)) != proof.WireHash {
		return "", fail
	}
	if _, err := wire.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	// Fix the read boundary at open so a concurrently appending native writer
	// cannot make verification chase an unbounded stream. No total size cap.
	model, err := validateKimiWireReader(ctx, io.LimitReader(wire, st.Size()), proof.CWD, proof.ID)
	if err != nil {
		return "", err
	}
	var importedBytes int64
	for key, size := range proof.Blobs {
		if !kimiRuntimeHash(key) || size < 0 || size > kimiBlobFileBudget {
			return "", fail
		}
		importedBytes += size
		if importedBytes > kimiBlobTotalBudget {
			return "", fail
		}
		if err := verifyKimiRuntimeBlob(ctx, root, filepath.Join(agentDir, "blobs", key), key, size); err != nil {
			return "", err
		}
	}
	return model, nil
}

func verifyKimiRuntimeBlob(ctx context.Context, root *os.Root, path, hash string, size int64) error {
	f, err := openKimiRegularFile(ctx, root, path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	encoder := base64.NewEncoder(base64.StdEncoding, sum)
	n, err := io.Copy(encoder, io.LimitReader(kimiRuntimeReader{ctx, f}, size+1))
	if err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(sum.Sum(nil)) != hash {
		return errors.New("kimi: imported media blob changed")
	}
	return nil
}

func kimiRuntimeHash(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == 64 && value == strings.ToLower(value)
}

type kimiRuntimeReader struct {
	ctx context.Context
	io.Reader
}

func (r kimiRuntimeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
