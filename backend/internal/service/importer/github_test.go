package importer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubGh answers gh calls by their first API path, for the GitHub owner and
// availability lookups.
func stubGh(t *testing.T, answer func(args []string) (string, error)) *[][]string {
	t.Helper()
	calls := &[][]string{}
	prev := importGhOutputFunc
	importGhOutputFunc = func(_ context.Context, _ string, args ...string) (string, error) {
		*calls = append(*calls, args)
		return answer(args)
	}
	t.Cleanup(func() { importGhOutputFunc = prev })
	return calls
}

func TestGitHubOwnersListsUserThenOrganizations(t *testing.T) {
	stubGh(t, func(args []string) (string, error) {
		switch args[1] {
		case "user":
			return "octo\thttps://a/octo\n", nil
		case "user/memberships/orgs":
			return "acme\thttps://a/acme\nocto\thttps://a/octo\n\n", nil
		}
		return "", errors.New("unexpected")
	})
	got, err := (&Manager{}).GitHubOwners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Owners) != 2 || got.Owners[0].Login != "octo" || got.Owners[1].Login != "acme" {
		t.Fatalf("owners = %+v, want octo then acme", got.Owners)
	}
}

func TestGitHubOwnersKeepsTheUserWithoutOrgScope(t *testing.T) {
	stubGh(t, func(args []string) (string, error) {
		if args[1] == "user" {
			return "octo\thttps://a/octo", nil
		}
		return "HTTP 403", errors.New("forbidden")
	})
	got, _ := (&Manager{}).GitHubOwners(context.Background())
	if len(got.Owners) != 1 || got.Owners[0].Login != "octo" {
		t.Fatalf("owners = %+v, want only octo", got.Owners)
	}
}

func TestGitHubOwnersIsEmptyWhenSignedOut(t *testing.T) {
	stubGh(t, func([]string) (string, error) { return "not logged in", errors.New("exit 4") })
	got, err := (&Manager{}).GitHubOwners(context.Background())
	if err != nil || got.Owners == nil || len(got.Owners) != 0 {
		t.Fatalf("owners = %+v, err = %v; want an empty list", got.Owners, err)
	}
}

func TestGitHubRepositoryAvailability(t *testing.T) {
	cases := []struct {
		name      string
		out       string
		err       error
		available bool
		message   string
	}{
		{"taken", "{}", nil, false, "already in use"},
		{"free", "gh: Not Found (HTTP 404)", errors.New("exit 1"), true, ""},
		{"unknown", "connection refused", errors.New("exit 1"), false, "Could not check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubGh(t, func([]string) (string, error) { return tc.out, tc.err })
			got, err := (&Manager{}).GitHubRepositoryAvailability(context.Background(), GitHubAvailabilityQuery{Owner: "octo", Name: "app"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Available != tc.available || !strings.Contains(got.Message, tc.message) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestGitHubRepositoryAvailabilityRejectsUnsafeNamesWithoutAskingGh(t *testing.T) {
	calls := stubGh(t, func([]string) (string, error) { return "", nil })
	for _, in := range []GitHubAvailabilityQuery{{Owner: "", Name: "app"}, {Owner: "octo", Name: "../x"}, {Owner: "--help", Name: "app"}, {Owner: "octo", Name: "."}, {Owner: "octo", Name: ".."}, {Owner: ".octo", Name: "app"}} {
		got, _ := (&Manager{}).GitHubRepositoryAvailability(context.Background(), in)
		if got.Available {
			t.Fatalf("%+v reported available", in)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("gh was called: %v", *calls)
	}
}

func TestGitHubRepositoryAvailabilityAcceptsNamesStartingWithAPeriod(t *testing.T) {
	calls := stubGh(t, func([]string) (string, error) { return "gh: Not Found (HTTP 404)", errors.New("exit 1") })
	got, err := (&Manager{}).GitHubRepositoryAvailability(context.Background(), GitHubAvailabilityQuery{Owner: "acme", Name: ".github"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available {
		t.Fatalf("got %+v, want .github available", got)
	}
	if len(*calls) != 1 || (*calls)[0][1] != "repos/acme/.github" {
		t.Fatalf("gh calls = %v, want one lookup of repos/acme/.github", *calls)
	}
}

func TestGitHubRepositoryAvailabilityIsNotFreeWithoutGh(t *testing.T) {
	stubGh(t, func(args []string) (string, error) {
		return "", errors.New(`gh api repos/octo/app: exec: "gh": executable file not found in $PATH`)
	})
	got, err := (&Manager{}).GitHubRepositoryAvailability(context.Background(), GitHubAvailabilityQuery{Owner: "octo", Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Available || !strings.Contains(got.Message, "Could not check") {
		t.Fatalf("got %+v, want the sign-in message", got)
	}
}
