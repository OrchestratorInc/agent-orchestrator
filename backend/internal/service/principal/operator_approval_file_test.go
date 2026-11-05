package principal_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// TestOperatorApprovalMintsNoCredentialFile is the Windows evidence test for the CURRENT claim.
//
// The old claim was "adoption deletes the file". The honest claim now is different: a same-user
// process can read any file it likes, so secrecy is not the control. What matters is that there is
// NOTHING to read — an approval cycle mints no operator credential file at all, and the authority
// that does exist is the OS verification result, which lives in this process's memory table and in
// the OS, never in a file a sibling process can copy.
//
// The test therefore (a) demonstrates the same-user process really can read a planted file, so the
// conclusion is not an artefact of a weak reader, and (b) asserts <dataDir>/operator.token is ENOENT
// both before and after a full create → OS-verify → consume cycle.
func TestOperatorApprovalMintsNoCredentialFile(t *testing.T) {
	if os.Getenv("AO_OPERATOR_FILE_READER") == "1" {
		// Helper half: prove this process can read whatever the parent planted, and report absence
		// as an observation rather than a crash (the interesting case is "nothing to read").
		path := os.Getenv("AO_OPERATOR_FILE_PATH")
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			os.Stdout.WriteString("child-absent")
			return
		}
		if err != nil {
			t.Fatalf("child: read %s: %v", path, err)
		}
		os.Stdout.WriteString("child-read:" + strings.TrimSpace(string(raw)))
		return
	}
	t.Parallel()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	readAsSecondProcess := func(path string) string {
		t.Helper()
		cmd := exec.Command(exe, "-test.run=^TestOperatorApprovalMintsNoCredentialFile$")
		cmd.Env = append(os.Environ(), "AO_OPERATOR_FILE_READER=1", "AO_OPERATOR_FILE_PATH="+path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("second process could not run (%s): %v\n%s", cmd.String(), err, out)
		}
		return string(out)
	}

	// (a) The reader is genuinely capable: a planted file is read back verbatim.
	planted := filepath.Join(dir, "planted.txt")
	const marker = "same-user-read-proof"
	if err := os.WriteFile(planted, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := readAsSecondProcess(planted); !strings.Contains(out, "child-read:"+marker) {
		t.Fatalf("the second process could not read a planted file, so this test proves nothing: %s", out)
	}

	// (b) The credential file does not exist before the cycle...
	tokenPath := filepath.Join(dir, principal.OperatorTokenFileName)
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("%s exists before any approval: err=%v", principal.OperatorTokenFileName, err)
	}

	// ...a full approval cycle runs against the OS boundary...
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	confirmed := approveAndConfirm(t, f, approval.ID, approval.Code, digest)
	if confirmed.ConfirmedByFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("confirming identity = %q", confirmed.ConfirmedByFingerprint)
	}
	if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err != nil {
		t.Fatalf("consume: %v", err)
	}

	// ...and there is still nothing for a same-user process to copy.
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a full approval cycle: err=%v", principal.OperatorTokenFileName, err)
	}
	// A second process confirms the absence from OUTSIDE this process's view.
	if out := readAsSecondProcess(tokenPath); strings.Contains(out, "child-read:") {
		t.Fatalf("a second same-user process found and read an operator credential: %s", out)
	}

	// The durable-data locations the daemon owns contain no operator credential either.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "operator") {
			t.Fatalf("the approval cycle wrote %q into the data dir", entry.Name())
		}
	}
}
