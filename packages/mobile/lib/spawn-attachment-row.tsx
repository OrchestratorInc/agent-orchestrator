import { useState } from "react";
import { Image, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import { ImageViewer } from "./ImageViewer";
import { usePressFeedback } from "./pressFeedback";
import { spawnAttachmentPreview, type SpawnAttachment } from "./spawn-attachments";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, space, touchTarget, type } from "./tokens";

const THUMB_SIZE = 56;
// The remove disc on a thumbnail's corner stays small so it covers little of
// the photo; its hit area is grown to a full touch target around it.
const REMOVE_DISC_SIZE = iconSize.lg;
const REMOVE_OVERHANG = space.xs;
const removeSlop = (glyph: number) => (touchTarget - glyph) / 2;
// How far that target reaches past the thumbnail's top and right edges. The
// scroll view drops touches outside its bounds, so the row keeps this much room
// there; the last disc's overhang is no longer cut off at the scroll's end either.
const REMOVE_REACH = REMOVE_OVERHANG + removeSlop(REMOVE_DISC_SIZE);
const CHIP_MAX_WIDTH = 190;

/**
 * Picked attachments above the spawn controls. Images are square thumbnails
 * that open full screen on tap; anything else stays a name chip.
 */
export function SpawnAttachmentRow({ attachments, onRemove }: {
	attachments: readonly SpawnAttachment[];
	onRemove(item: SpawnAttachment): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The photo covers the thumbnail's own background, so Android's ripple is
	// drawn over it rather than behind it.
	const openFeedback = usePressFeedback("button", { foreground: true });
	const removeFeedback = usePressFeedback("icon");
	const [viewing, setViewing] = useState<{ name: string; uri: string }>();
	if (!attachments.length) return null;
	return <>
		{/* A ScrollView grows by default. Unpinned, the row split the sheet's
		    spare height with the prompt and floated in the middle of it, far
		    from the selectors.
		    iOS form sheets (react-native-screens) look for a ScrollView among the
		    sheet's direct native children and stretch it to the sheet frame at
		    (0, 0) on every sheet resize, so the keyboard or the image viewer
		    threw the row to the top-left and left an invisible full-sheet
		    scroller over the prompt that swallowed taps. A non-collapsable
		    wrapper keeps it out of that lookup. */}
		<View collapsable={false} style={styles.row}>
			<ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.content}>
				{attachments.map((item, index) => {
					const preview = spawnAttachmentPreview(item);
					const remove = (
						<Pressable
							hitSlop={removeSlop(preview ? REMOVE_DISC_SIZE : iconSize.sm)}
							accessibilityRole="button"
							accessibilityLabel={`Remove ${item.name}`}
							android_ripple={removeFeedback.android_ripple}
							onPress={() => { haptics.tap(); onRemove(item); }}
							style={({ pressed }) => [preview ? styles.thumbRemove : undefined, pressed && removeFeedback.pressed]}
						>
							<AppSymbol name="close" size={preview ? iconSize.xs : iconSize.sm} color={preview ? t.textPrimary : t.textTertiary} weight="semibold" />
						</Pressable>
					);
					return preview ? (
						<View key={`${item.name}-${index}`} style={styles.thumb}>
							<Pressable
								accessibilityRole="imagebutton"
								accessibilityLabel={`Open ${item.name}`}
								android_ripple={openFeedback.android_ripple}
								onPress={() => setViewing({ name: item.name, uri: preview })}
								style={({ pressed }) => [styles.thumbOpen, pressed && openFeedback.pressed]}
							>
								<Image accessibilityIgnoresInvertColors source={{ uri: preview }} resizeMode="cover" style={styles.thumbImage} />
							</Pressable>
							{remove}
						</View>
					) : (
						<View key={`${item.name}-${index}`} style={styles.chip}>
							<AppSymbol name="fileText" size={iconSize.sm} color={t.accent} />
							<Text numberOfLines={1} style={styles.chipName}>{item.name}</Text>
							{remove}
						</View>
					);
				})}
			</ScrollView>
		</View>
		{viewing ? <ImageViewer visible name={viewing.name} source={{ uri: viewing.uri }} onClose={() => setViewing(undefined)} /> : null}
	</>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { flexGrow: 0, flexShrink: 0 },
	content: { gap: space.sm, alignItems: "center", paddingTop: REMOVE_REACH, paddingRight: REMOVE_REACH },
	thumb: { width: THUMB_SIZE, height: THUMB_SIZE },
	thumbOpen: { borderRadius: radius.md, borderCurve: "continuous", overflow: "hidden" },
	thumbImage: { width: THUMB_SIZE, height: THUMB_SIZE, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderSubtle },
	// Sits on the photo, so it is an opaque disc of the theme's own surface:
	// the mark reads on any image without a fixed scrim colour.
	thumbRemove: {
		position: "absolute",
		top: -REMOVE_OVERHANG,
		right: -REMOVE_OVERHANG,
		width: REMOVE_DISC_SIZE,
		height: REMOVE_DISC_SIZE,
		borderRadius: radius.pill,
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.bgElevated,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
	},
	chip: { maxWidth: CHIP_MAX_WIDTH, minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.sm, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderSubtle },
	chipName: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
