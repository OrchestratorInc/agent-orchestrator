import { useEffect, useState } from "react";
import { Image, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { ImageViewer } from "../ImageViewer";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, iconSize, onImage, radius, space, touchTarget, type } from "../tokens";

/** One attachment waiting to be sent: a picture when there is one to show, else its name. */
export type ComposerTrayItem = { key: string; name: string; previewUri?: string };

export const TRAY_TILE_SIZE = 80;
// The pill's corner is its inset plus the tile's, so the two curves run
// concentric where the first tile meets the pill's top corner.
const TILE_RADIUS = radius.lg;
const TILE_INSET = space.xs;
const FILE_TILE_MAX_WIDTH = 160;
// The remove disc sits inside the tile's corner so it never leaves the pill;
// its hit area is grown to a full touch target around it. A tile clips touches
// to its own bounds (hit slop does not count as overflow), so the disc is inset
// by its slop and the whole target stays inside the tile.
const REMOVE_DISC_SIZE = iconSize.xl;
export const removeSlop = (touchTarget - REMOVE_DISC_SIZE) / 2;
const REMOVE_INSET = Math.max(space.xs, removeSlop);

/**
 * The attachments waiting in a composer, drawn inside its pill above the
 * field: photos as rounded thumbnails that open full screen on tap, anything
 * else as a tile of the same height that names it, each with a remove disc in
 * its corner. Shared by the session, reviewer and new-task composers.
 */
export function ComposerAttachmentTray({ items, onRemove }: { items: readonly ComposerTrayItem[]; onRemove(key: string): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The photo covers the tile's own background, so Android's ripple is drawn
	// over it rather than behind it.
	const openFeedback = usePressFeedback("button", { foreground: true });
	const removeFeedback = usePressFeedback("icon");
	// The photo open full screen, by key, and only while it is still attached: a
	// send can land meanwhile and take it, and the tray stays mounted, so a kept
	// selection would reopen on the next pick.
	const [viewingKey, setViewingKey] = useState<string>();
	const viewing = viewingKey === undefined ? undefined : items.find((item) => item.key === viewingKey && item.previewUri);
	useEffect(() => { if (viewingKey !== undefined && !viewing) setViewingKey(undefined); }, [viewingKey, viewing]);
	if (!items.length) return null;
	return <>
		{/* iOS form sheets (react-native-screens) look for a ScrollView among a
		    sheet's direct native children and stretch it to the sheet's frame on
		    every resize. A non-collapsable wrapper keeps this one out of that
		    lookup wherever the tray is mounted. */}
		<View collapsable={false} style={styles.tray}>
			<ScrollView horizontal showsHorizontalScrollIndicator={false} keyboardShouldPersistTaps="handled" contentContainerStyle={styles.content}>
				{items.map((item) => {
					const remove = (
						<Pressable
							hitSlop={removeSlop}
							accessibilityRole="button"
							accessibilityLabel={`Remove ${item.name}`}
							android_ripple={removeFeedback.android_ripple}
							onPress={() => { haptics.tap(); onRemove(item.key); }}
							style={({ pressed }) => [styles.remove, pressed && removeFeedback.pressed]}
						>
							<AppSymbol name="close" size={iconSize.xs} color={onImage.mark} weight="bold" />
						</Pressable>
					);
					const preview = item.previewUri;
					return preview ? (
						<View key={item.key} style={styles.thumb}>
							<Pressable
								accessibilityRole="imagebutton"
								accessibilityLabel={`Open ${item.name}`}
								android_ripple={openFeedback.android_ripple}
								onPress={() => setViewingKey(item.key)}
								style={({ pressed }) => [styles.thumbOpen, pressed && openFeedback.pressed]}
							>
								<Image accessibilityIgnoresInvertColors source={{ uri: preview }} resizeMode="cover" style={styles.thumbImage} />
							</Pressable>
							{remove}
						</View>
					) : (
						<View key={item.key} style={styles.fileTile}>
							<AppSymbol name="fileText" size={iconSize.lg} color={t.accent} />
							<Text numberOfLines={2} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.fileName}>{item.name}</Text>
							{remove}
						</View>
					);
				})}
			</ScrollView>
		</View>
		{viewing?.previewUri ? <ImageViewer visible name={viewing.name} source={{ uri: viewing.previewUri }} onClose={() => setViewingKey(undefined)} /> : null}
	</>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	tray: { flexGrow: 0, flexShrink: 0 },
	content: { gap: space.sm, paddingTop: TILE_INSET, paddingHorizontal: TILE_INSET, paddingBottom: space.xxs },
	thumb: { width: TRAY_TILE_SIZE, height: TRAY_TILE_SIZE },
	thumbOpen: { borderRadius: TILE_RADIUS, borderCurve: "continuous", overflow: "hidden" },
	thumbImage: { width: TRAY_TILE_SIZE, height: TRAY_TILE_SIZE, borderRadius: TILE_RADIUS, borderCurve: "continuous", backgroundColor: t.bgSubtle, borderWidth: rowDividerWidth, borderColor: t.borderSubtle },
	// As tall as a photo; wider when the name needs it, so short names keep a square.
	fileTile: { height: TRAY_TILE_SIZE, minWidth: TRAY_TILE_SIZE, maxWidth: FILE_TILE_MAX_WIDTH, justifyContent: "space-between", padding: space.sm, paddingRight: REMOVE_INSET + REMOVE_DISC_SIZE, borderRadius: TILE_RADIUS, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderSubtle },
	fileName: { fontFamily: "DMSans_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	// Over a photo of any colour, so a fixed dark disc and a white mark rather
	// than theme colours.
	remove: {
		position: "absolute",
		top: REMOVE_INSET,
		right: REMOVE_INSET,
		width: REMOVE_DISC_SIZE,
		height: REMOVE_DISC_SIZE,
		borderRadius: radius.pill,
		overflow: "hidden",
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: onImage.disc,
	},
});
