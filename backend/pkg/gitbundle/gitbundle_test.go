package gitbundle

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeBundle(t *testing.T, header string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "b.bundle")
	if err := os.WriteFile(path, []byte(header+"\nPACK....binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrerequisitesReadsTheHeader(t *testing.T) {
	a, b := "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	for name, header := range map[string]string{
		"v2": "# v2 git bundle\n-" + a + " first commit\n-" + b + "\n" + a + " refs/ao/x\n",
		"v3": "# v3 git bundle\n@object-format=sha1\n-" + a + " first\n-" + b + " second\n" + b + " refs/ao/x\n",
	} {
		got, err := Prerequisites(writeBundle(t, header))
		if err != nil || !reflect.DeepEqual(got, []string{a, b}) {
			t.Errorf("%s: Prerequisites = %v, %v; want [%s %s]", name, got, err, a, b)
		}
	}
	got, err := Prerequisites(writeBundle(t, "# v2 git bundle\n"+a+" refs/ao/x\n"))
	if err != nil || len(got) != 0 {
		t.Fatalf("self-contained bundle: Prerequisites = %v, %v; want none", got, err)
	}
}

func TestPrerequisitesRefusesWhatIsNotABundle(t *testing.T) {
	if _, err := Prerequisites(writeBundle(t, "PK\x03\x04 a zip file\n")); !errors.Is(err, ErrNotBundle) {
		t.Fatalf("Prerequisites of a non-bundle = %v, want ErrNotBundle", err)
	}
	if _, err := Prerequisites(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Prerequisites of a missing file succeeded")
	}
}
