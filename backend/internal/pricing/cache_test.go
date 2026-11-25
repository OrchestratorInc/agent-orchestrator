package pricing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheInstallsPrivateManifestLastAndLoadsLKG(t *testing.T) {
	dataDir := t.TempDir()
	fixture := testCandidate(t, testBaseModels("0.1"))
	catalog, err := ParseCatalog(fixture.manifest, fixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(dataDir)
	if err := cache.Install(t.Context(), catalog); err != nil {
		t.Fatalf("Install: %v", err)
	}
	loaded, err := cache.Load(t.Context())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Snapshot().ProviderVersion("openai"); got != fixture.versions["openai"] {
		t.Fatalf("loaded openai version = %q", got)
	}
	for _, file := range append([]string{filepath.Join(cache.Root(), "manifest.json")}, providerFilePaths(t, cache.Root(), fixture)...) {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", file, got)
		}
	}
	if info, err := os.Stat(cache.Root()); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache root mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestCacheHonorsCancellationBeforeLoadAndBetweenProviderInstalls(t *testing.T) {
	fixture := testCandidate(t, testBaseModels("0.1"))
	catalog, err := ParseCatalog(fixture.manifest, fixture.providers)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("load already canceled", func(t *testing.T) {
		cache := NewCache(t.TempDir())
		if err := cache.Install(context.Background(), catalog); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := cache.Load(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Load error = %v, want context canceled", err)
		}
	})

	t.Run("install canceled between providers", func(t *testing.T) {
		cache := NewCache(t.TempDir())
		ctx, cancel := context.WithCancel(context.Background())
		realRename := cache.rename
		providerRenames := 0
		cache.rename = func(oldPath, newPath string) error {
			if err := realRename(oldPath, newPath); err != nil {
				return err
			}
			if filepath.Base(newPath) != "manifest.json" {
				providerRenames++
				if providerRenames == 1 {
					cancel()
				}
			}
			return nil
		}
		if err := cache.Install(ctx, catalog); !errors.Is(err, context.Canceled) {
			t.Fatalf("Install error = %v, want context canceled", err)
		}
		if providerRenames != 1 {
			t.Fatalf("provider installs after cancellation = %d, want 1", providerRenames)
		}
		if _, err := os.Stat(filepath.Join(cache.Root(), "manifest.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("manifest committed after cancellation: %v", err)
		}
	})
}

func TestCacheManifestReplaceFailurePreservesOldLKG(t *testing.T) {
	dataDir := t.TempDir()
	cache := NewCache(dataDir)
	oldFixture := testCandidate(t, testBaseModels("0.1"))
	oldCatalog, err := ParseCatalog(oldFixture.manifest, oldFixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Install(t.Context(), oldCatalog); err != nil {
		t.Fatal(err)
	}
	newFixture := testCandidate(t, testBaseModels("0.2"))
	newCatalog, err := ParseCatalog(newFixture.manifest, newFixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	realRename := cache.rename
	cache.rename = func(oldPath, newPath string) error {
		if filepath.Base(newPath) == "manifest.json" {
			return errors.New("injected manifest replace failure")
		}
		return realRename(oldPath, newPath)
	}
	if err := cache.Install(t.Context(), newCatalog); err == nil {
		t.Fatal("Install error = nil")
	}
	cache.rename = realRename
	loaded, err := cache.Load(t.Context())
	if err != nil {
		t.Fatalf("Load old LKG: %v", err)
	}
	if got := loaded.Snapshot().ProviderVersion("anthropic"); got != oldFixture.versions["anthropic"] {
		t.Fatalf("version after failed install = %q, want old %q", got, oldFixture.versions["anthropic"])
	}
}

func TestCacheMissingOrCorruptLKGIsUnavailable(t *testing.T) {
	cache := NewCache(t.TempDir())
	if _, err := cache.Load(t.Context()); !errors.Is(err, ErrNoCachedCatalog) {
		t.Fatalf("missing Load error = %v", err)
	}
	if err := os.MkdirAll(cache.Root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache.Root(), "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(t.Context()); err == nil || errors.Is(err, ErrNoCachedCatalog) {
		t.Fatalf("corrupt Load error = %v", err)
	}
}

func TestCacheInstallRepairsCorruptProviderBlob(t *testing.T) {
	dataDir := t.TempDir()
	fixture := testCandidate(t, testBaseModels("0.1"))
	catalog, err := ParseCatalog(fixture.manifest, fixture.providers)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(dataDir)
	if err := cache.Install(t.Context(), catalog); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	corrupted := providerFilePaths(t, cache.Root(), fixture)[0]
	if err := os.WriteFile(corrupted, []byte(`{"providerId":"openai","models":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(t.Context()); err == nil {
		t.Fatal("corrupt provider blob loaded successfully")
	}

	if err := cache.Install(t.Context(), catalog); err != nil {
		t.Fatalf("reinstall over corrupt blob: %v", err)
	}
	loaded, err := cache.Load(t.Context())
	if err != nil {
		t.Fatalf("Load after repair: %v", err)
	}
	if got := loaded.Snapshot().ProviderVersion("openai"); got != fixture.versions["openai"] {
		t.Fatalf("repaired openai version = %q, want %q", got, fixture.versions["openai"])
	}
	if info, err := os.Stat(corrupted); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("repaired blob mode = %v, %v", info.Mode().Perm(), err)
	}
}

func providerFilePaths(t *testing.T, root string, fixture testCatalog) []string {
	t.Helper()
	paths := make([]string, 0, len(fixture.providers))
	for relative := range fixture.providers {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(relative)))
	}
	return paths
}
