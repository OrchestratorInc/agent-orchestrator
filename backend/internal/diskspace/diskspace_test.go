package diskspace

import "testing"

func TestFreeReportsSpaceOnTheTempVolume(t *testing.T) {
	free, err := Free(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("free space on the temp volume = 0")
	}
}
