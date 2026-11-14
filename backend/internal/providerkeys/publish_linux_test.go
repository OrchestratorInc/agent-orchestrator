package providerkeys

import "testing"

func TestEnvironmentValueReadsSystemdQuoting(t *testing.T) {
	// systemctl show-environment prints values needing quotes in ANSI-C form.
	out := "HOME=/home/u\nOPENAI_API_KEY=plain-key\n" + `GROQ_API_KEY=$'a b\'c\\d\x24e'` + "\nXAI_API_KEY_OTHER=x\n"
	for name, want := range map[string]string{
		"OPENAI_API_KEY": "plain-key",
		"GROQ_API_KEY":   `a b'c\d$e`,
		"XAI_API_KEY":    "",
	} {
		if got := environmentValue(out, name); got != want {
			t.Errorf("environmentValue(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestOwnedValueProtectsTheUsersValue(t *testing.T) {
	for _, tc := range []struct {
		current, value, previous string
		want                     bool
	}{
		{"", "new", "", true},
		{"new", "new", "", true},
		{"old", "new", "old", true},
		{"mine", "new", "old", false},
		{"mine", "new", "", false},
	} {
		if got := ownedValue(tc.current, tc.value, tc.previous); got != tc.want {
			t.Errorf("ownedValue(%q, %q, %q) = %v", tc.current, tc.value, tc.previous, got)
		}
	}
}
