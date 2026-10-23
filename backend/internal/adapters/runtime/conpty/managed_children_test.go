package conpty

import "testing"

func TestParseManagedChildrenUsesKernelStateOnly(t *testing.T) {
	out := `    1     0 Ss
  100     1 S+
  101   100 R+
  102   100 Z+
  103   100 X
  104   101 S
`
	got, err := parseManagedChildren(out, 100)
	if err != nil {
		t.Fatal(err)
	}
	// 101 runs; 102 (zombie) and 103 (dead) have exited; 104 is a grandchild.
	if !got.supervisorListed || got.running != 1 {
		t.Fatalf("children = %+v, want supervisor listed with one running child", got)
	}
	got, err = parseManagedChildren("  100 1 Z\n  101 100 S\n", 100)
	if err != nil || got.supervisorListed {
		t.Fatalf("exited supervisor = %+v, %v; want not listed", got, err)
	}
	got, err = parseManagedChildren("", 100)
	if err != nil || got.supervisorListed || got.running != 0 {
		t.Fatalf("empty snapshot = %+v, %v; want nothing listed", got, err)
	}
}

func TestParseManagedChildrenRejectsIncompleteRows(t *testing.T) {
	for _, out := range []string{
		"100 1\n",
		"100 1 S <defunct>\n",
		"abc 1 S\n",
		"100 x S\n",
		"-1 1 S\n",
	} {
		if got, err := parseManagedChildren(out, 100); err == nil {
			t.Fatalf("parse %q = %+v, want an error", out, got)
		}
	}
}
