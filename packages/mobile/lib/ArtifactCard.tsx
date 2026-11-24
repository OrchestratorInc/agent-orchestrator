import { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View, type ViewToken } from "react-native";
import { ArtifactPoster } from "./ArtifactPoster";
import { formatArtifactSize, type SessionArtifact } from "./artifacts";
import type { ArtifactsBackend } from "./artifactsBackend";
import { relativeTime } from "./notificationView";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, press, radius, space, type } from "./tokens";

/**
 * How a grid of artifact cards loads lazily: a few screens of cards mounted at
 * a time, and a card's preview requested only once a quarter of it has been
 * on screen for a moment, so scrolling past does not fetch it.
 */
export const ARTIFACT_GRID_WINDOW = { initialNumToRender: 6, maxToRenderPerBatch: 4, updateCellsBatchingPeriod: 80, windowSize: 5 } as const;
export const ARTIFACT_VIEWABILITY = { itemVisiblePercentThreshold: 25, minimumViewTime: 200 } as const;

/** How long the grid must hold still before previews load or let go. */
export const ARTIFACT_SETTLE_MS = 150;

/**
 * Which cards are on screen, changed only once the grid has settled. A card's
 * preview is a web view (a page) or a rendered document, and making and
 * dropping them as cards crossed the edge mid-scroll is what made the grid
 * stutter: while it moves, every card keeps what it shows, and when it stops
 * the cards now on screen load and those gone let go.
 */
export function useSettledVisibility<T>(keyOf: (item: T) => string) {
	const [visibleKeys, setVisibleKeys] = useState<ReadonlySet<string>>(() => new Set());
	const pending = useRef<ReadonlySet<string> | null>(null);
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const keyOfRef = useRef(keyOf);
	keyOfRef.current = keyOf;
	useEffect(() => () => clearTimeout(timer.current), []);
	const onViewableItemsChanged = useRef(({ viewableItems }: { viewableItems: ViewToken<T>[] }) => {
		pending.current = new Set(viewableItems.flatMap((token) => token.item ? [keyOfRef.current(token.item)] : []));
		clearTimeout(timer.current);
		timer.current = setTimeout(() => {
			const next = pending.current;
			pending.current = null;
			if (next) setVisibleKeys((prev) => (sameKeys(prev, next) ? prev : next));
		}, ARTIFACT_SETTLE_MS);
	}).current;
	const isVisible = useCallback((key: string) => visibleKeys.has(key), [visibleKeys]);
	return { visibleKeys, isVisible, onViewableItemsChanged };
}

function sameKeys(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
	if (a.size !== b.size) return false;
	for (const key of a) if (!b.has(key)) return false;
	return true;
}

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
