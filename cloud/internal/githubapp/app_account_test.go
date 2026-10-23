package githubapp

import (
	"net/http"
	"testing"
)

func TestIsAppAccountMatchesBothLoginForms(t *testing.T) {
	svc := newCheckoutTestService(t, &checkoutStubStore{}, newAppTestClient(t, "https://api.example.com", http.DefaultClient))
	for login, want := range map[string]bool{
		"ao-test[bot]": true, "AO-Test": true, " ao-test ": true,
		"octocat": false, "ao-test-other[bot]": false, "": false,
	} {
		if got := svc.IsAppAccount(login); got != want {
			t.Errorf("IsAppAccount(%q) = %v, want %v", login, got, want)
		}
	}
	var none *Service
	if none.IsAppAccount("ao-test") {
		t.Error("a nil service matched an App account")
	}
}
