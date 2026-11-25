package sessionimport

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAuggieListOutputBoundCancelsWithoutKeepingOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &auggieListBuffer{limit: 4, cancel: cancel}
	if n, err := b.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := b.Write([]byte("def")); n != 1 || !errors.Is(err, errAuggieListTooLarge) {
		t.Fatalf("overflow = %d, %v", n, err)
	}
	if b.buffer.String() != "abcd" || !b.exceeded || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("overflow state: %q, exceeded=%v, context=%v", b.buffer.String(), b.exceeded, ctx.Err())
	}
}

func TestAuggieListUsesBoundedCommandContext(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestAuggieListFixtureProcess$", "--", "ao-cli-list-fixture"}
	out, err := readAuggieList(context.Background(), binary, args...)
	if err != nil || string(out) != "synthetic native list" {
		t.Fatalf("list output = %q, %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := readAuggieList(ctx, binary, append(args, "wait")...); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("list deadline = %v", err)
	}
	if _, err := readAuggieList(context.Background(), binary, append(args, "overflow")...); !errors.Is(err, errAuggieListTooLarge) {
		t.Fatalf("list output bound = %v", err)
	}
}

func TestAuggieListFixtureProcess(t *testing.T) {
	fixture := false
	for _, arg := range os.Args {
		fixture = fixture || arg == "ao-cli-list-fixture"
	}
	if !fixture {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "wait":
		time.Sleep(time.Minute)
	case "overflow":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", auggieListMaxBytes+1))
	default:
		_, _ = os.Stdout.WriteString("synthetic native list")
	}
	os.Exit(0)
}
