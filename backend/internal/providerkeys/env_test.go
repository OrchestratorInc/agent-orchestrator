package providerkeys

import (
	"os"
	"testing"
)

func TestExportForProbesRemembersAndRestoresTheInheritedValue(t *testing.T) {
	const exported = "AO_TEST_PROVIDER_KEY_EXPORTED"
	const absent = "AO_TEST_PROVIDER_KEY_ABSENT"
	t.Setenv(exported, "user-value")
	t.Setenv(absent, "")
	if err := os.Unsetenv(absent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RestoreInherited(exported); _ = RestoreInherited(absent) })

	if _, _, was := Inherited(exported); was {
		t.Fatal("a variable AO never exported reads as exported")
	}
	for _, value := range []string{"ao-first", "ao-second"} {
		if err := ExportForProbes(exported, value); err != nil {
			t.Fatal(err)
		}
	}
	// The value recorded is the one from before AO's first export.
	if value, set, was := Inherited(exported); !was || !set || value != "user-value" || os.Getenv(exported) != "ao-second" {
		t.Fatalf("inherited=%q set=%v exported=%v env=%q", value, set, was, os.Getenv(exported))
	}
	if err := RestoreInherited(exported); err != nil || os.Getenv(exported) != "user-value" {
		t.Fatalf("restore: env=%q err=%v", os.Getenv(exported), err)
	}

	if err := ExportForProbes(absent, "ao-only"); err != nil {
		t.Fatal(err)
	}
	if _, set, was := Inherited(absent); !was || set {
		t.Fatalf("absent variable: set=%v exported=%v", set, was)
	}
	if err := RestoreInherited(absent); err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv(absent); ok {
		t.Fatal("restoring a variable the user never set left AO's value behind")
	}
	// Restoring one AO never exported leaves the environment alone.
	t.Setenv(absent, "set-later")
	if err := RestoreInherited(absent); err != nil || os.Getenv(absent) != "set-later" {
		t.Fatalf("untouched variable changed: env=%q err=%v", os.Getenv(absent), err)
	}
}
