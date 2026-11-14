import { artifactViewKind, type SessionArtifact } from "./artifacts";
import type { FeatherIconName } from "./icons";
import type { Theme } from "./theme";

/**
 * Each kind of file gets its own glyph and hue, so a grid or strip of them
 * scans at a glance. The glyphs are the desktop's (artifact-file-icon).
 */
export function artifactVisual(artifact: SessionArtifact, t: Theme): { icon: FeatherIconName; color: string; tint: string } {
	switch (artifactViewKind(artifact)) {
		case "html": return { icon: "globe", color: t.blue, tint: t.tintBlue };
		case "markdown": return { icon: "file-text", color: t.purple, tint: t.tintPurple };
		case "image":
		case "svg": return { icon: "image", color: t.green, tint: t.tintGreen };
		case "video": return { icon: "video", color: t.amber, tint: t.tintAmber };
		case "audio": return { icon: "music", color: t.amber, tint: t.tintAmber };
		case "pdf": return { icon: "file", color: t.red, tint: t.tintRed };
		case "table": return { icon: "table", color: t.green, tint: t.tintGreen };
		case "diagram": return { icon: "bar-chart", color: t.purple, tint: t.tintPurple };
		case "binary": return { icon: "archive", color: t.textTertiary, tint: t.bgSubtle };
		case "text": return { icon: "code", color: t.orange, tint: t.tintOrange };
	}
}
