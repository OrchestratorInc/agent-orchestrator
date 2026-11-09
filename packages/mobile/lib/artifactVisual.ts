import { artifactViewKind, type SessionArtifact } from "./artifacts";
import type { FeatherIconName } from "./icons";
import type { Theme } from "./theme";

/** Each kind of file gets its own glyph and hue, so a grid or strip of them scans at a glance. */
export function artifactVisual(artifact: SessionArtifact, t: Theme): { icon: FeatherIconName; color: string; tint: string } {
	switch (artifactViewKind(artifact)) {
		case "html": return { icon: "globe", color: t.blue, tint: t.tintBlue };
		case "markdown": return { icon: "file-text", color: t.purple, tint: t.tintPurple };
		case "image": return { icon: "image", color: t.green, tint: t.tintGreen };
		case "web-media": return { icon: /\.svg$/i.test(artifact.path) ? "image" : "play", color: t.amber, tint: t.tintAmber };
		case "text": return { icon: "code", color: t.orange, tint: t.tintOrange };
	}
}
