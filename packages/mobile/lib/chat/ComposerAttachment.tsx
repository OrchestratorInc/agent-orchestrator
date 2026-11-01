import { Image, Pressable, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { usePressFeedback } from "../pressFeedback";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, iconSize } from "../tokens";
import { composerStyleRules } from "./composerStyles";
import type { ChatImage } from "./types";

/**
 * One attachment waiting in a composer: a thumbnail or file glyph, its name, and
 * a remove button the size of a touch target. Shared by the session and
 * reviewer composers, which attach the same way.
 */
export function ComposerAttachment({ name, image, onRemove }: { name: string; image?: ChatImage; onRemove(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(composerStyleRules);
	const feedback = usePressFeedback("icon");
	return (
		<View style={styles.attachment}>
			{image
				? <Image accessibilityIgnoresInvertColors source={{ uri: `data:${image.mimeType};base64,${image.data}` }} style={styles.attachmentImage} />
				: <AppSymbol name="fileText" size={iconSize.sm} color={t.accent} />}
			<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.attachmentName}>{name}</Text>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={`Remove ${name}`}
				android_ripple={feedback.android_ripple}
				onPress={onRemove}
				style={({ pressed }) => [styles.attachmentRemove, pressed && feedback.pressed]}
			>
				<AppSymbol name="close" size={iconSize.sm} color={t.textTertiary} />
			</Pressable>
		</View>
	);
}
