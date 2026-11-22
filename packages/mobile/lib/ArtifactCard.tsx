import { Pressable, StyleSheet, Text, View } from "react-native";
import { ArtifactPoster } from "./ArtifactPoster";
import { formatArtifactSize, type SessionArtifact } from "./artifacts";
import type { ArtifactsBackend } from "./artifactsBackend";
import { relativeTime } from "./notificationView";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, press, radius, space, type } from "./tokens";

/**
 * An artifact as a card, as the desktop's Artifacts grid draws one: a preview
 * of the file filling its top, then its name and a line under it. Long press
 * opens its actions, where it has any.
 */
export function ArtifactCard({ artifact, backend, sessionId, visible, refreshes, versionOf, meta: metaOverride, onOpen, onMenu }: {
	artifact: SessionArtifact;
	backend: ArtifactsBackend | null;
	sessionId: string;
	/** On screen: the card's preview of its contents may load. */
	visible: boolean;
	/** Pulls to refresh so far; a failed preview is tried again after one. */
	refreshes: number;
	versionOf(path: string): string | undefined;
	/** The line under the name; by default whether it is public, its age and its size. */
	meta?: string;
	onOpen(): void;
	onMenu?(): void;
}) {
	const styles = useThemedStyles(makeStyles);
	const age = relativeTime(artifact.updatedAt);
	const meta = metaOverride ?? [artifact.shareUrl ? "Public" : "", age, formatArtifactSize(artifact.size)].filter(Boolean).join(" · ");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${artifact.name}, ${meta}`}
			accessibilityHint="Opens the artifact. Long-press for more actions."
			accessibilityActions={[{ name: "longpress", label: "More actions" }]}
			onAccessibilityAction={(event) => { if (event.nativeEvent.actionName === "longpress") onMenu?.(); }}
			onPress={onOpen}
			onLongPress={onMenu}
			delayLongPress={350}
			style={styles.card}
		>
			{({ pressed }) => (<>
				<ArtifactPoster artifact={artifact} backend={backend} sessionId={sessionId} visible={visible} versionOf={versionOf} retries={refreshes} style={styles.poster} />
				<View style={styles.label}>
					<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.name}>{artifact.name}</Text>
					{meta ? <Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.meta}>{meta}</Text> : null}
				</View>
				{/* Pressed, the card dims under a veil: an Android WebView does not fade
				    with a parent's opacity, and fading one can blank it for a frame. */}
				{pressed ? <View pointerEvents="none" style={styles.pressed} /> : null}
			</>)}
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	card: { overflow: "hidden", borderRadius: radius.lg, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	poster: { aspectRatio: 4 / 3 },
	pressed: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase, opacity: 1 - press.opacity },
	label: { paddingHorizontal: space.md, paddingVertical: space.sm, gap: space.hair },
	name: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "500" },
	meta: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
});
