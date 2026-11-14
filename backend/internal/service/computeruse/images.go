package computeruse

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const keepImages = 200

const sharedScope = "shared"

var scopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var screenshotTools = map[string]bool{
	"get_window_state":  true,
	"get_desktop_state": true,
}

type imageStore struct {
	root string
}

func newImageStore(dataDir string) imageStore {
	return imageStore{root: filepath.Join(dataDir, "computer-use")}
}

func (s imageStore) scope(sessionID string) (string, error) {
	name := strings.TrimSpace(sessionID)
	if name == "" {
		name = sharedScope
	}
	if !scopePattern.MatchString(name) {
		return "", apierr.Invalid("COMPUTER_USE_INVALID_SESSION", "sessionId may contain only letters, digits, '.', '_' and '-'.", nil)
	}
	dir := filepath.Join(s.root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("computeruse: create image folder: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("computeruse: resolve image folder: %w", err)
	}
	return resolved, nil
}

func (s imageStore) prune(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keepImages {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names[:max(0, len(names)-keepImages)] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func dropImageFiles(arguments map[string]any) {
	delete(arguments, "debug_image_out")
	steps, ok := arguments["steps"].([]any)
	if !ok {
		return
	}
	copied := make([]any, len(steps))
	for index, raw := range steps {
		copied[index] = raw
		step, _ := raw.(map[string]any)
		tool, stepArguments := stepAction(step)
		if !hasAny(stepArguments, "debug_image_out", "screenshot_out_file") {
			continue
		}
		stepArguments = maps.Clone(stepArguments)
		delete(stepArguments, "debug_image_out")
		delete(stepArguments, "screenshot_out_file")
		step = maps.Clone(step)
		if _, written := step["tool"].(string); written {
			step["args"] = stepArguments
		} else {
			step[tool] = stepArguments
		}
		copied[index] = step
	}
	arguments["steps"] = copied
}

type savedImages struct {
	store  imageStore
	scope  string
	tool   string
	stamp  string
	count  int
	images []Image
}

func (c *savedImages) nextPath(extension string) string {
	if c.stamp == "" {
		nonce := make([]byte, 6)
		_, _ = rand.Read(nonce)
		c.stamp = time.Now().UTC().Format("20060102T150405.000Z") + "-" + hex.EncodeToString(nonce)
	}
	c.count++
	return filepath.Join(c.scope, fmt.Sprintf("%s-%s-%d%s", c.stamp, c.tool, c.count, extension))
}

func (c *savedImages) requestScreenshotFile(tool string, arguments map[string]any) string {
	if !screenshotTools[tool] {
		return ""
	}
	if include, ok := arguments["include_screenshot"].(bool); ok && !include {
		delete(arguments, "screenshot_out_file")
		return ""
	}
	path := c.nextPath(".png")
	arguments["screenshot_out_file"] = path
	return path
}

func (c *savedImages) adopt(path string) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return
	}
	for _, image := range c.images {
		if image.Path == path {
			return
		}
	}
	c.images = append(c.images, Image{Path: path, MimeType: "image/png"})
}

func (c *savedImages) externalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if kind, _ := typed["type"].(string); kind == "image" {
			if data, ok := typed["data"].(string); ok {
				delete(typed, "data")
				if path, err := c.write(data); err == nil {
					typed["path"] = path
				} else {
					typed["error"] = err.Error()
				}
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			data, isString := typed[key].(string)
			if isString && strings.HasSuffix(key, "_b64") {
				delete(typed, key)
				name := strings.TrimSuffix(key, "_b64")
				if path, err := c.write(data); err == nil {
					typed[name+"_file_path"] = path
				} else {
					typed[name+"_error"] = err.Error()
				}
				continue
			}
			typed[key] = c.externalize(typed[key])
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = c.externalize(typed[index])
		}
		return typed
	default:
		return value
	}
}

func (c *savedImages) write(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", errors.New("inline image could not be decoded")
	}
	mimeType := http.DetectContentType(data)
	extension := ".bin"
	switch mimeType {
	case "image/png":
		extension = ".png"
	case "image/jpeg":
		extension = ".jpg"
	case "image/gif":
		extension = ".gif"
	case "image/webp":
		extension = ".webp"
	}
	path := c.nextPath(extension)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", errors.New("inline image could not be saved")
	}
	c.images = append(c.images, Image{Path: path, MimeType: mimeType})
	return path, nil
}
