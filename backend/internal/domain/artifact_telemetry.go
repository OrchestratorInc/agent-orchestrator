package domain

import (
	"path"
	"strings"
)

// Closed vocabularies for the daemon's artifact telemetry
// (ao.artifacts.registered and ao.artifacts.register_failed). Only these
// values leave the machine: never a path, file name, extension or session.

// artifactContentCategories maps an extension to its content category.
var artifactContentCategories = func() map[string]string {
	groups := map[string]string{
		"markdown": "md markdown mdx",
		"html":     "html htm",
		"image":    "png jpg jpeg gif webp svg avif bmp ico heic tif tiff",
		"video":    "mp4 mov webm mkv avi m4v",
		"audio":    "mp3 wav m4a ogg flac aac",
		"pdf":      "pdf",
		"text":     "txt log rtf csv tsv json jsonl yaml yml xml toml",
	}
	out := map[string]string{}
	for category, extensions := range groups {
		for _, extension := range strings.Fields(extensions) {
			out[extension] = category
		}
	}
	return out
}()

// ArtifactContentCategory is the content category of the artifact at a
// slash-separated path, from its extension: markdown, html, image, video,
// audio, pdf, text or other. A bundle directory is other.
func ArtifactContentCategory(artifactPath string) string {
	name := path.Base(artifactPath)
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return "other"
	}
	if category, ok := artifactContentCategories[strings.ToLower(name[dot+1:])]; ok {
		return category
	}
	return "other"
}

// ValidArtifactContentCategory reports whether value is one of ArtifactContentCategory's values.
func ValidArtifactContentCategory(value string) bool {
	switch value {
	case "markdown", "html", "image", "video", "audio", "pdf", "text", "other":
		return true
	}
	return false
}

// ArtifactSizeBucket bands an artifact's size in bytes: xs under 10 KB, s
// under 100 KB, m under 1 MB, l under 10 MB, xl from 10 MB.
func ArtifactSizeBucket(size int64) string {
	const kb = 1024
	switch {
	case size < 10*kb:
		return "xs"
	case size < 100*kb:
		return "s"
	case size < kb*kb:
		return "m"
	case size < 10*kb*kb:
		return "l"
	}
	return "xl"
}

// ValidArtifactSizeBucket reports whether value is one of ArtifactSizeBucket's values.
func ValidArtifactSizeBucket(value string) bool {
	switch value {
	case "xs", "s", "m", "l", "xl":
		return true
	}
	return false
}

// ArtifactSource is where an artifact came from, by the session that
// registered it: artifacts_tab for a worker started by New artifact (its
// requested output is artifact), otherwise worker or orchestrator.
func ArtifactSource(kind SessionKind, requested RequestedOutput) string {
	switch {
	case kind == KindOrchestrator:
		return "orchestrator"
	case requested == RequestedOutputArtifact:
		return "artifacts_tab"
	}
	return "worker"
}

// ValidArtifactSource reports whether value is one of ArtifactSource's values.
func ValidArtifactSource(value string) bool {
	switch value {
	case "artifacts_tab", "worker", "orchestrator":
		return true
	}
	return false
}

// Why the daemon refused an artifact registration.
const (
	ArtifactRegisterOutsideDir     = "outside_dir"
	ArtifactRegisterNotFound       = "not_found"
	ArtifactRegisterNotRegistrable = "not_registrable"
	// The session was deleted while its artifacts were being registered.
	ArtifactRegisterSessionNotFound = "session_not_found"
	ArtifactRegisterInternal        = "internal"
)

// ValidArtifactRegisterFailure reports whether value is one of the ArtifactRegister* reasons.
func ValidArtifactRegisterFailure(value string) bool {
	switch value {
	case ArtifactRegisterOutsideDir, ArtifactRegisterNotFound, ArtifactRegisterNotRegistrable, ArtifactRegisterSessionNotFound, ArtifactRegisterInternal:
		return true
	}
	return false
}
