package primeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestPrimePreparationCleansOwnedOutputOnFailureAndRetry(t *testing.T) {
	for _, failure := range []string{"runner", "cancellation", "source changed", "ambiguous", "proof publication"} {
		t.Run(failure, func(t *testing.T) {
			dataDir, sourceRoot, cwd, workspace := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			source := writePrimeNativeFixture(t, sourceRoot, primeSourceID, cwd, "", primeNativeFixtureRows()...)
			root, err := sessionDir(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			preexisting := writePrimeNativeFixture(t, root, "51381da5-ea93-465a-9506-97868d30cc43", workspace, source, primeForkFixtureRows()...)
			preexistingData, err := os.ReadFile(preexisting)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := primeTestPlugin()
			var attempt, foreign, marker string
			var sourceAfterFailure []byte
			calls := 0
			p.runCommand = func(_ context.Context, _, resolvedCWD string, env map[string]string, _ ...string) ([]byte, error) {
				calls++
				attempt = env["PRIME_AGENT_SESSION_DIR"]
				writePrimeNativeFixture(t, attempt, primeForkID, resolvedCWD, source, primeForkFixtureRows()...)
				if runtime.GOOS != "windows" {
					if err := os.Symlink(sourceRoot, filepath.Join(attempt, "source-link")); err != nil {
						t.Fatal(err)
					}
				}
				if calls > 1 {
					return nil, nil
				}
				// A concurrent writer's matching fork in the shared root must
				// neither be selected nor be removed by this preparation.
				foreign = writePrimeNativeFixture(t, root, "420df575-a967-4ce3-82c8-f81412f6d52f", resolvedCWD, source, primeForkFixtureRows()...)
				switch failure {
				case "runner":
					return []byte(strings.Repeat("discarded-prefix", 1000) + "useful-stderr-tail"), errors.New("synthetic fork failed after writing")
				case "cancellation":
					cancel()
					return []byte("cancelled-fork-tail"), ctx.Err()
				case "source changed":
					writePrimeNativeFixture(t, sourceRoot, primeSourceID, cwd, "", primeNativeFixtureRows()[0])
				case "ambiguous":
					writePrimeNativeFixture(t, attempt, "6a6dd55c-3c42-401a-b0bd-3720b709b78d", resolvedCWD, source, primeForkFixtureRows()...)
				case "proof publication":
					key := sha256.Sum256([]byte(source + "\x00" + primeSourceID + "\x00" + resolvedCWD))
					marker = filepath.Join(root, fmt.Sprintf(".ao-import-%x.json", key))
					// A foreign marker appearing during the fork prevents proof
					// publication; it must survive cleanup too.
					if err := os.Mkdir(marker, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				sourceAfterFailure, err = os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				return nil, nil
			}
			request := ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: sourceRoot, TranscriptPath: source, SourceCWD: cwd, LaunchCWD: workspace, DataDir: dataDir}
			ref, err := p.PrepareImportedSession(ctx, request)
			if err == nil || ref.NativeSessionID != "" {
				t.Fatalf("failed preparation returned identity: %#v, %v", ref, err)
			}
			if failure == "runner" && (!strings.HasSuffix(err.Error(), "useful-stderr-tail") || strings.Contains(err.Error(), strings.Repeat("discarded-prefix", 300)) || len(err.Error()) > primeForkStderrLimit+200) {
				t.Fatalf("stderr diagnostic is missing or unbounded: %d bytes", len(err.Error()))
			}
			if failure == "cancellation" && (!errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "cancelled-fork-tail")) {
				t.Fatalf("cancellation diagnostic lost: %v", err)
			}
			if _, err := os.Stat(attempt); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unadopted attempt survived: %v", err)
			}
			if got, err := os.ReadFile(preexisting); err != nil || !bytes.Equal(got, preexistingData) {
				t.Fatalf("preexisting history changed: %v", err)
			}
			if _, err := os.Stat(foreign); err != nil {
				t.Fatalf("concurrent foreign history removed: %v", err)
			}
			if sourceAfterFailure != nil {
				if got, err := os.ReadFile(source); err != nil || !bytes.Equal(got, sourceAfterFailure) {
					t.Fatalf("cleanup modified source: %v", err)
				}
			}
			if marker != "" {
				if err := os.Remove(marker); err != nil {
					t.Fatalf("foreign marker disappeared: %v", err)
				}
			}
			writePrimeNativeFixture(t, sourceRoot, primeSourceID, cwd, "", primeNativeFixtureRows()...)
			ref, err = p.PrepareImportedSession(context.Background(), request)
			if err != nil || ref.NativeSessionID != primeForkID || calls != 2 {
				t.Fatalf("retry failed: %#v, %v, calls=%d", ref, err, calls)
			}
			retry, err := p.PrepareImportedSession(context.Background(), request)
			if err != nil || retry != ref || calls != 2 {
				t.Fatalf("adopted fork was lost or reforked: %#v, %v, calls=%d", retry, err, calls)
			}
			files, err := primeSessionFiles(context.Background(), root)
			if err != nil || len(files) != 3 || !files[preexisting] || !files[foreign] || !files[ref.TranscriptPath] {
				t.Fatalf("retry accumulated or removed histories: %v, %v", files, err)
			}
		})
	}
}

func TestPrimeConcurrentPreparationsOwnTheirForks(t *testing.T) {
	for _, scenario := range []string{"same import", "different workspace", "failed peer"} {
		t.Run(scenario, func(t *testing.T) {
			dataDir, sourceRoot, cwd, workspace := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			source := writePrimeNativeFixture(t, sourceRoot, primeSourceID, cwd, "", primeNativeFixtureRows()...)
			sourceData, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			request := ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: sourceRoot, TranscriptPath: source, SourceCWD: cwd, LaunchCWD: workspace, DataDir: dataDir}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ready := make(chan string, 2)
			release := make(chan struct{})
			published := make(chan struct{})
			type result struct {
				ref ports.NativeSessionRef
				err error
			}
			results := make(chan result, 2)
			for i, id := range []string{primeForkID, "6a6dd55c-3c42-401a-b0bd-3720b709b78d"} {
				r := request
				if scenario == "different workspace" && i == 1 {
					r.LaunchCWD = t.TempDir()
				}
				p := primeTestPlugin() // Ownership must hold across plugin instances.
				p.runCommand = func(ctx context.Context, _, resolvedCWD string, env map[string]string, _ ...string) ([]byte, error) {
					attempt := env["PRIME_AGENT_SESSION_DIR"]
					writePrimeNativeFixture(t, attempt, id, resolvedCWD, source, primeForkFixtureRows()...)
					ready <- attempt
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if scenario == "failed peer" && i == 1 {
						select {
						case <-published:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
						return nil, errors.New("synthetic peer failure after writing")
					}
					return nil, nil
				}
				go func() {
					ref, err := p.PrepareImportedSession(ctx, r)
					if i == 0 {
						close(published)
					}
					results <- result{ref, err}
				}()
			}
			attempts := make([]string, 0, 2)
			for range 2 {
				select {
				case path := <-ready:
					attempts = append(attempts, path)
				case <-ctx.Done():
					t.Fatal("concurrent forks did not both start")
				}
			}
			if attempts[0] == attempts[1] {
				t.Fatal("concurrent preparations share output ownership")
			}
			close(release)
			var adopted []ports.NativeSessionRef
			failures := 0
			for range 2 {
				select {
				case got := <-results:
					if got.err != nil {
						if scenario != "failed peer" || !strings.Contains(got.err.Error(), "synthetic peer failure") {
							t.Fatalf("concurrent preparation failed: %v", got.err)
						}
						failures++
					} else {
						adopted = append(adopted, got.ref)
					}
				case <-ctx.Done():
					t.Fatal("concurrent preparations did not finish")
				}
			}
			wantFiles := 1
			if scenario == "different workspace" {
				wantFiles = 2
			}
			if scenario == "same import" && (len(adopted) != 2 || adopted[0] != adopted[1]) {
				t.Fatalf("same import did not reuse the published proof: %#v", adopted)
			}
			if scenario == "failed peer" && (failures != 1 || len(adopted) != 1) {
				t.Fatalf("unexpected failed-peer outcome: %d, %#v", failures, adopted)
			}
			root, err := sessionDir(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			files, err := primeSessionFiles(context.Background(), root)
			if err != nil || len(files) != wantFiles {
				t.Fatalf("concurrent cleanup accumulated or removed forks: %v, %v", files, err)
			}
			for _, ref := range adopted {
				if !files[ref.TranscriptPath] {
					t.Fatalf("adopted history was removed: %#v", ref)
				}
			}
			for _, attempt := range attempts {
				if _, err := os.Stat(attempt); err == nil {
					kept := false
					for _, ref := range adopted {
						kept = kept || filepath.Dir(ref.TranscriptPath) == attempt
					}
					if !kept {
						t.Fatalf("unadopted concurrent attempt survived: %s", attempt)
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
			if got, err := os.ReadFile(source); err != nil || !bytes.Equal(got, sourceData) {
				t.Fatalf("concurrent cleanup changed source: %v", err)
			}
		})
	}
}

func TestPrimeForkStderrKeepsBoundedTail(t *testing.T) {
	var tail primeForkStderrTail
	want := ""
	for _, chunk := range []string{"small warning\n", strings.Repeat("a", primeForkStderrLimit*3), strings.Repeat("b", 100), "\nlast diagnostic\n"} {
		want += chunk
		if len(want) > primeForkStderrLimit {
			want = want[len(want)-primeForkStderrLimit:]
		}
		n, err := tail.Write([]byte(chunk))
		if err != nil || n != len(chunk) || string(tail.data) != want || len(tail.data) > primeForkStderrLimit {
			t.Fatalf("stderr tail write: n=%d, err=%v, buffered=%d", n, err, len(tail.data))
		}
	}
}
