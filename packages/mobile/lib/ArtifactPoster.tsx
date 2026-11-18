import { useCallback, useState } from "react";
import { Image, StyleSheet, Text, View, type LayoutChangeEvent, type StyleProp, type ViewStyle } from "react-native";
import { ArtifactThumbnail, hasContentThumbnail } from "./ArtifactThumbnail";
import { artifactExtensionLabel, artifactViewKind, type SessionArtifact } from "./artifacts";
import type { ArtifactsBackend } from "./artifactsBackend";
import { artifactVisual } from "./artifactVisual";
import { Feather } from "./icons";
import { imageSourceForPlatform } from "./imageSource";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, space, type } from "./tokens";

/**
 * The picture on an artifact's card: an image artifact's own picture, a peek
 * at a page or document's contents, or its type's icon tile. The gallery and
 * the chat's turn cards draw the same one; the card around it sets its shape.
 */
export function ArtifactPoster({ artifact, backend, sessionId, visible, versionOf, retries = 0, iconSize = 28, style }: {
	artifact: SessionArtifact;
	backend: ArtifactsBackend | null;
	sessionId: string;
	/** On screen: the preview of its contents may load. */
	visible: boolean;
	/** A listed file's modified time, for the images a markdown card shows. */
	versionOf(path: string): string | undefined;
	/** Bumped to let a preview that failed try again (a pull to refresh). */
	retries?: number;
	iconSize?: number;
	style?: StyleProp<ViewStyle>;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The URI carries the file's modified time, so a rewrite loads afresh.
	const thumbnail = backend && artifactViewKind(artifact) === "image" ? backend.imageSource(sessionId, artifact.path, artifact.updatedAt) : undefined;
	const [failedUri, setFailedUri] = useState<string>();
	// Requested once the poster first comes near the screen, then kept, so a
	// long thread or gallery does not fetch every picture up front and one
	// scrolled past and back does not blank and reload.
	const [reached, setReached] = useState(visible);
	if (visible && !reached) setReached(true);
	const picture = reached && thumbnail && failedUri !== thumbnail.uri ? thumbnail : undefined;
	// A card that previews its contents shows a plain tile while the preview
	// loads, and its icon only if the preview cannot load: until the file
	// changes or the preview is retried.
	const attempt = `${artifact.updatedAt}#${retries}`;
	const [previewFailedAt, setPreviewFailedAt] = useState<string>();
	const preview = Boolean(backend && hasContentThumbnail(artifact)) && previewFailedAt !== attempt;
	const onPreviewFailed = useCallback(() => setPreviewFailedAt(attempt), [attempt]);
	const visual = artifactVisual(artifact, t);
	// Cropped from the top, as the desktop's object-cover object-top: a phone
	// screenshot shows its status bar and header, not its middle.
	const [box, setBox] = useState<{ width: number; height: number }>();
	const [aspect, setAspect] = useState<number>();
	const onLayout = (event: LayoutChangeEvent) => {
		const { width, height } = event.nativeEvent.layout;
		setBox({ width, height });
	};
	const pictureStyle = box && aspect && box.width / aspect > box.height
		? { position: "absolute" as const, top: 0, left: 0, width: box.width, height: box.width / aspect }
		: StyleSheet.absoluteFill;
	return (
		<View onLayout={picture ? onLayout : undefined} style={[styles.poster, { backgroundColor: preview ? t.bgSubtle : visual.tint }, style]}>
			{preview ? null : <>
				<Feather name={visual.icon} size={iconSize} color={visual.color} />
				<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.extension, { color: visual.color }]}>{artifactExtensionLabel(artifact.path)}</Text>
			</>}
			{preview && backend ? <ArtifactThumbnail backend={backend} sessionId={sessionId} artifact={artifact} visible={visible} versionOf={versionOf} onFailed={onPreviewFailed} /> : null}
			{picture ? (
				<Image
					accessibilityIgnoresInvertColors
					source={imageSourceForPlatform(picture)}
					resizeMode="cover"
					onError={() => setFailedUri(picture.uri)}
					onLoad={(event) => {
						const { width, height } = event.nativeEvent.source;
						if (width > 0 && height > 0) setAspect(width / height);
					}}
					style={pictureStyle}
				/>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	poster: { alignItems: "center", justifyContent: "center", gap: space.xs, overflow: "hidden" },
	extension: { fontFamily: t.fontMono, fontSize: type.caption2.fontSize, fontWeight: "600", letterSpacing: 0.4 },
});
