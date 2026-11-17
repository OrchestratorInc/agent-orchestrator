package chattraffic

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // git object ids are sha1; this verifies them, it is not a security hash.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Harness paths, relative to the repository root and slash-separated. These
// are the only files a baseline tree receives from the candidate. They are all
// benchmark-only: the leaf package with its fixtures and limits, and the
// test file that drives the product. Product code is never in this set.
const (
	HarnessPackageDir = "backend/internal/chattraffic"
	HarnessTestFile   = "backend/internal/httpd/chattraffic_harness_test.go"
	FixturesDir       = HarnessPackageDir + "/testdata/fixtures"
	ExpectedDir       = HarnessPackageDir + "/testdata/expected"
)

// ClientSourceFiles are the renderer files whose paging and event routing the
// Go client simulator mirrors.
var ClientSourceFiles = []string{
	"frontend/src/renderer/hooks/useConversation.ts",
	"frontend/src/renderer/lib/event-transport.ts",
}

// FileDigest names one file and the sha256 of its bytes.
type FileDigest struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

// HarnessFileDigests lists every harness file under root in path order.
// Symlinks and other non-regular files are refused.
func HarnessFileDigests(root string) ([]FileDigest, error) {
	var files []FileDigest
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(HarnessPackageDir)), func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		digest, err := regularFileDigest(p)
		if err != nil {
			return err
		}
		files = append(files, FileDigest{Path: filepath.ToSlash(rel), Sha256: digest})
		return nil
	})
	if err != nil {
		return nil, err
	}
	digest, err := regularFileDigest(filepath.Join(root, filepath.FromSlash(HarnessTestFile)))
	if err != nil {
		return nil, err
	}
	files = append(files, FileDigest{Path: HarnessTestFile, Sha256: digest})
	slices.SortFunc(files, func(a, b FileDigest) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// DirectoryDigests lists the regular files directly in dir (no recursion).
func DirectoryDigests(root, dir string) ([]FileDigest, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, err
	}
	var files []FileDigest
	for _, entry := range entries {
		rel := path.Join(dir, entry.Name())
		digest, err := regularFileDigest(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		files = append(files, FileDigest{Path: rel, Sha256: digest})
	}
	return files, nil
}

// ListDigest is one digest over a list of file digests: the sha256 of
// "path NUL sha256 LF" lines in path order.
func ListDigest(files []FileDigest) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b FileDigest) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	for _, file := range sorted {
		h.Write([]byte(file.Path + "\x00" + file.Sha256 + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256Hex is the lowercase hex sha256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// OverlayEntry records one harness file placed into a baseline tree.
type OverlayEntry struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
	// Mode is "added" when the baseline lacked the file, or "identical" when
	// the baseline already held the same bytes.
	Mode string `json:"mode"`
}

// Overlay copies the candidate's harness files into a baseline tree. It only
// adds: a file the baseline already has must be byte-identical, so the
// overlay can never shadow or replace baseline code. Each copy is re-read and
// compared with the candidate digest.
func Overlay(candidateRoot, baselineRoot string) ([]OverlayEntry, error) {
	files, err := HarnessFileDigests(candidateRoot)
	if err != nil {
		return nil, fmt.Errorf("list candidate harness: %w", err)
	}
	entries := make([]OverlayEntry, 0, len(files))
	for _, file := range files {
		if !isHarnessPath(file.Path) {
			return nil, fmt.Errorf("%s is not a harness path", file.Path)
		}
		source := filepath.Join(candidateRoot, filepath.FromSlash(file.Path))
		target := filepath.Join(baselineRoot, filepath.FromSlash(file.Path))
		mode := "added"
		switch info, err := os.Lstat(target); {
		case err == nil:
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("baseline %s exists and is not a regular file", file.Path)
			}
			existing, err := regularFileDigest(target)
			if err != nil {
				return nil, err
			}
			if existing != file.Sha256 {
				return nil, fmt.Errorf("baseline already has a different %s; refusing to shadow it", file.Path)
			}
			mode = "identical"
		case errors.Is(err, fs.ErrNotExist):
			data, err := os.ReadFile(source)
			if err != nil {
				return nil, err
			}
			if SHA256Hex(data) != file.Sha256 {
				return nil, fmt.Errorf("candidate %s changed while it was copied", file.Path)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil { //nolint:gosec // test sources in a private scratch tree.
				return nil, err
			}
			if copied, err := regularFileDigest(target); err != nil || copied != file.Sha256 {
				return nil, fmt.Errorf("copied %s does not match the candidate", file.Path)
			}
		default:
			return nil, err
		}
		entries = append(entries, OverlayEntry{Path: file.Path, Sha256: file.Sha256, Mode: mode})
	}
	return entries, nil
}

func isHarnessPath(p string) bool {
	return p == HarnessTestFile || (strings.HasPrefix(p, HarnessPackageDir+"/") && !strings.Contains(p, ".."))
}

// VerifyExport proves that a baseline tree is the commit it claims to be plus
// only the overlay. lsTree is the output of `git ls-tree -r -z --full-tree
// <commit> -- <paths>`. Every listed blob must exist in the tree with the same
// git object id, and every file in the tree must be listed or be an added
// overlay entry.
func VerifyExport(exportRoot string, lsTree []byte, overlay []OverlayEntry) error {
	listed := map[string]string{}
	for _, record := range bytes.Split(lsTree, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return errors.New("ls-tree output is malformed")
		}
		mode, kind, object := fields[0], fields[1], fields[2]
		if kind != "blob" || (mode != "100644" && mode != "100755") {
			return fmt.Errorf("ls-tree lists %s %s; only regular blobs are supported", mode, kind)
		}
		if !hex40Pattern.MatchString(object) {
			return errors.New("ls-tree object id is not sha1 hex")
		}
		listed[string(name)] = object
	}
	if len(listed) == 0 {
		return errors.New("ls-tree output lists no files")
	}
	added := map[string]string{}
	for _, entry := range overlay {
		if entry.Mode == "added" {
			added[entry.Path] = entry.Sha256
		}
	}
	// Walk and read through a root so no path can leave the export, even if
	// the tree changes during the walk.
	root, err := os.OpenRoot(exportRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tree := root.FS()
	found := map[string]bool{}
	err = fs.WalkDir(tree, ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		data, err := fs.ReadFile(tree, rel)
		if err != nil {
			return err
		}
		if want, ok := added[rel]; ok {
			if SHA256Hex(data) != want {
				return fmt.Errorf("overlay file %s changed", rel)
			}
			return nil
		}
		want, ok := listed[rel]
		if !ok {
			return fmt.Errorf("%s is in the baseline tree but not in the commit", rel)
		}
		if gitBlobID(data) != want {
			return fmt.Errorf("%s differs from the commit's blob", rel)
		}
		found[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for name := range listed {
		if !found[name] {
			return fmt.Errorf("%s from the commit is missing in the baseline tree", name)
		}
	}
	return nil
}

func gitBlobID(data []byte) string {
	h := sha1.New() //nolint:gosec // git object id.
	h.Write([]byte("blob " + strconv.Itoa(len(data)) + "\x00"))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func regularFileDigest(p string) (string, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", filepath.ToSlash(p))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

// ReadArtifact reads an artifact as plain data: it must be a regular file (not a
// symlink) no larger than MaxArtifactBytes.
func ReadArtifact(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact is not a regular file")
	}
	if info.Size() > MaxArtifactBytes {
		return nil, fmt.Errorf("artifact is %d bytes; the limit is %d", info.Size(), MaxArtifactBytes)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxArtifactBytes {
		return nil, errors.New("artifact grew past the limit while it was read")
	}
	return data, nil
}
