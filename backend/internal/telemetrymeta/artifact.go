package telemetrymeta

import (
	"path"
	"strings"
)

// artifactExtensionTypes buckets an artifact's extension into the closed
// vocabulary the desktop uses (frontend/src/renderer/lib/artifact-telemetry.ts).
// Keep the two in step. The extension itself is never exported.
var artifactExtensionTypes = func() map[string]string {
	groups := map[string]string{
		"html":     "html htm",
		"markdown": "md markdown mdx",
		"pdf":      "pdf",
		"image":    "png jpg jpeg gif webp svg avif bmp ico heic tif tiff",
		"video":    "mp4 mov webm mkv avi m4v",
		"audio":    "mp3 wav m4a ogg flac aac",
		"data":     "csv tsv json jsonl xlsx xls yaml yml xml parquet sqlite db",
		"text":     "txt log rtf",
		"office":   "docx doc pptx ppt key pages numbers odt ods odp",
		"code":     "js mjs cjs ts tsx jsx py go rs java rb sh css scss swift kt c cc cpp h hpp cs php sql",
		"archive":  "zip tar gz tgz 7z rar",
	}
	out := map[string]string{}
	for bucket, extensions := range groups {
		for _, extension := range strings.Fields(extensions) {
			out[extension] = bucket
		}
	}
	return out
}()

// ArtifactFileType is the coarse file type of an artifact at a slash-separated
// path: one of html, markdown, pdf, image, video, audio, data, text, office,
// code, archive, bundle or other.
func ArtifactFileType(artifactPath string, bundle bool) string {
	if bundle {
		return "bundle"
	}
	name := path.Base(artifactPath)
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return "other"
	}
	if bucket, ok := artifactExtensionTypes[strings.ToLower(name[dot+1:])]; ok {
		return bucket
	}
	return "other"
}

// ValidArtifactFileType reports whether value is one of ArtifactFileType's buckets.
func ValidArtifactFileType(value string) bool {
	switch value {
	case "html", "markdown", "pdf", "image", "video", "audio", "data", "text", "office", "code", "archive", "bundle", "other":
		return true
	}
	return false
}
