package amp

import "testing"

func TestAmpActiveEmailFromList(t *testing.T) {
	list := "  other@example.com (user_02)\n* dev@example.com (user_01KWBSAFBZR63FSA0NQXJYVRQ8)\n"
	if got := ampActiveEmailFromList(list); got != "dev@example.com" {
		t.Fatalf("active email = %q", got)
	}
	if got := ampActiveEmailFromList("No saved accounts.\n"); got != "" {
		t.Fatalf("no accounts = %q", got)
	}
}
