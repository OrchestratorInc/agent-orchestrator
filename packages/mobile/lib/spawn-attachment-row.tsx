import { useState } from "react";
import { Image, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { ImageViewer } from "./ImageViewer";
import { spawnAttachmentPreview, type SpawnAttachment } from "./spawn-attachments";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, type } from "./tokens";

const THUMB_SIZE = 56;

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
	const [viewing, setViewing] = useState<{ name: string; uri: string }>();
	if (!attachments.length) return null;
	return <>
		{/* A ScrollView grows by default. Unpinned, the row split the sheet's
		    spare height with the prompt and floated in the middle of it, far
		    from the selectors. */}
		<ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.row} contentContainerStyle={styles.content}>
			{attachments.map((item, index) => {
				const preview = spawnAttachmentPreview(item);
				const remove = (
					<Pressable
						hitSlop={8}
						accessibilityRole="button"
						accessibilityLabel={`Remove ${item.name}`}
						onPress={() => { haptics.tap(); onRemove(item); }}
						style={preview ? styles.thumbRemove : undefined}
					>
						<Feather name="x" size={iconSize.xs} color={preview ? "#fff" : t.textTertiary} />
					</Pressable>
				);
				return preview ? (
					<View key={`${item.name}-${index}`} style={styles.thumb}>
						<Pressable
							accessibilityRole="imagebutton"
							accessibilityLabel={`Open ${item.name}`}
							onPress={() => { haptics.tap(); setViewing({ name: item.name, uri: preview }); }}
						>
							<Image accessibilityIgnoresInvertColors source={{ uri: preview }} resizeMode="cover" style={styles.thumbImage} />
						</Pressable>
						{remove}
					</View>
				) : (
					<View key={`${item.name}-${index}`} style={styles.chip}>
						<Feather name="file-text" size={iconSize.sm} color={t.accent} />
						<Text numberOfLines={1} style={styles.chipName}>{item.name}</Text>
						{remove}
					</View>
				);
			})}
		</ScrollView>
		{viewing ? <ImageViewer visible name={viewing.name} source={{ uri: viewing.uri }} onClose={() => setViewing(undefined)} /> : null}
	</>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { flexGrow: 0, flexShrink: 0 },
	content: { gap: space.sm, alignItems: "center", paddingTop: space.xs },
	thumb: { width: THUMB_SIZE, height: THUMB_SIZE },
	thumbImage: { width: THUMB_SIZE, height: THUMB_SIZE, borderRadius: 12, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	// Sits on the image, so it carries its own dark disc to stay legible on any photo.
	thumbRemove: { position: "absolute", top: -space.xs, right: -space.xs, width: 20, height: 20, borderRadius: 10, alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0, 0, 0, 0.6)" },
	chip: { maxWidth: 190, height: 36, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.sm, borderRadius: 12, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	chipName: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.caption1.fontSize },
});
