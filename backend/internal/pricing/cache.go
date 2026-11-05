package pricing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrNoCachedCatalog indicates that no last-known-good manifest is installed.
var ErrNoCachedCatalog = errors.New("no cached pricing catalog")

// Cache 将最近可用价格目录保存在 AO_DATA_DIR 内；先发布 blob，最后替换清单。
type Cache struct {
	root   string
	rename func(*os.Root, string, string) error
}

// NewCache returns a private v1 pricing cache rooted below dataDir.
func NewCache(dataDir string) *Cache {
	return &Cache{root: filepath.Join(dataDir, "pricing", "catalog", "v1"), rename: (*os.Root).Rename}
}

// Root returns the resolved cache directory.
func (c *Cache) Root() string { return c.root }

// Load reads and validates the complete last-known-good catalog synchronously.
func (c *Cache) Load(ctx context.Context) (*Catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(c.root, "manifest.json")
	manifestBytes, err := readBoundedFile(manifestPath, ManifestMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCachedCatalog
	}
	if err != nil {
		return nil, fmt.Errorf("read cached pricing manifest: %w", err)
	}
	var manifest catalogManifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("decode cached pricing manifest: %w", err)
	}
	if err := validateRuntimeManifest(manifest); err != nil {
		return nil, err
	}
	providers := make(map[string][]byte, len(manifest.Providers))
	for _, ref := range manifest.Providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		contents, err := readBoundedFile(filepath.Join(c.root, filepath.FromSlash(ref.Path)), ProviderMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("read cached provider %q: %w", ref.ProviderID, err)
		}
		providers[ref.Path] = contents
	}
	return ParseCatalog(manifestBytes, providers)
}

// Install 校验完整候选目录后先写 blob，再原子替换清单；失败时保留原有可用清单。
func (c *Cache) Install(ctx context.Context, catalog *Catalog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if catalog == nil || catalog.snapshot == nil {
		return errors.New("pricing catalog is nil")
	}
	if _, err := decodeCatalog(catalog.manifestBytes, catalog.providerBytes); err != nil {
		return fmt.Errorf("validate pricing cache candidate: %w", err)
	}
	// 一并收紧旧版创建的中间目录，避免其他账户从可写父目录替换缓存根。
	for _, directory := range []string{filepath.Dir(filepath.Dir(c.root)), filepath.Dir(c.root), c.root, filepath.Join(c.root, "providers")} {
		if err := ensurePrivateDir(directory); err != nil {
			return err
		}
	}
	for _, ref := range catalog.manifest.Providers {
		if err := ctx.Err(); err != nil {
			return err
		}
		directory := filepath.Join(c.root, filepath.FromSlash(filepath.Dir(ref.Path)))
		if err := ensurePrivateDir(directory); err != nil {
			return err
		}
		providerPath := filepath.Join(c.root, filepath.FromSlash(ref.Path))
		if err := installImmutableFile(providerPath, catalog.providerBytes[ref.Path], c.rename); err != nil {
			return fmt.Errorf("install provider %q: %w", ref.ProviderID, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	manifestPath := filepath.Join(c.root, "manifest.json")
	if err := atomicReplaceFile(manifestPath, catalog.manifestBytes, c.rename); err != nil {
		return fmt.Errorf("replace pricing manifest: %w", err)
	}
	return nil
}

// installImmutableFile 保留内容匹配的 blob 并修复其权限；内容不匹配说明本地损坏，
// 使用已校验摘要的候选字节替换，避免同一摘要的后续刷新持续失败。
func installImmutableFile(target string, contents []byte, rename func(*os.Root, string, string) error) error {
	existing, err := os.ReadFile(target)
	switch {
	case err == nil && bytes.Equal(existing, contents):
		return protectCacheFile(target)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	return atomicReplaceFile(target, contents, rename)
}

// atomicReplaceFile 用同一固定目录句柄创建、清理和发布私有临时对象，避免父路径替换。
func atomicReplaceFile(target string, contents []byte, rename func(*os.Root, string, string) error) (err error) {
	root, err := os.OpenRoot(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	temporary, err := createPrivateCacheTemp(root)
	if err != nil {
		return err
	}
	temporaryName := filepath.Base(temporary.Name())
	closed := false
	defer func() { _ = root.Remove(temporaryName) }()
	defer func() {
		if closed {
			return
		}
		if closeErr := temporary.Close(); err == nil {
			err = closeErr
		}
	}()
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := rename(root, temporaryName, filepath.Base(target)); err != nil {
		return err
	}
	return nil
}

func readBoundedFile(path string, limit int64) (contents []byte, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}()
	contents, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("file exceeds %d-byte limit", limit)
	}
	return contents, nil
}
