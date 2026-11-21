import { describe, expect, it } from "vitest";
import { isBrowserArtifactPath } from "../../shared/editor-handoff";
import { fileMediaKind } from "./file-media";

// The Open in browser action and the main process share one rule; it must
// cover every media format the viewer previews, so the action never fails.
const media = [
	"a.apng", "a.avif", "a.bmp", "a.gif", "a.ico", "a.jpg", "a.jpeg", "a.png", "a.webp",
	"a.m4v", "a.mov", "a.mp4", "a.ogv", "a.webm",
	"a.aac", "a.flac", "a.m4a", "a.mp3", "a.oga", "a.ogg", "a.opus", "a.wav",
	"a.pdf",
];

describe("isBrowserArtifactPath", () => {
	it.each(media)("opens %s, which the viewer previews as media", (path) => {
		expect(fileMediaKind(path)).toBeDefined();
		expect(isBrowserArtifactPath(path)).toBe(true);
	});

	it.each(["page.html", "page.HTM", "nested/dir/logo.svg"])("opens %s", (path) => {
		expect(isBrowserArtifactPath(path)).toBe(true);
	});

	it.each(["notes.md", "run.sh", "tool.command", "app.exe", "pdf", "dir.png/readme", ".png"])("refuses %s", (path) => {
		expect(isBrowserArtifactPath(path)).toBe(false);
	});
});
