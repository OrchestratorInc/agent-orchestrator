import { Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import { usePressFeedback } from "./pressFeedback";
import { Spinner } from "./Spinner";
import type { AppSymbolName, PlatformSymbol } from "./symbols";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, space, touchTarget, type } from "./tokens";

export type InlineBannerTone = "warning" | "danger" | "muted";

export type InlineBannerProps = {
	tone: InlineBannerTone;
	/** The leading status glyph, or `"busy"` for a state that is still in progress. */
	icon: AppSymbolName | PlatformSymbol | "busy";
	title: string;
	body?: string;
	/** The banner's main action. Without `onPress` it does nothing, as while it runs ("Retrying…"). */
	action?: string;
	/** What screen readers say for the action, when its visible text is not enough. */
	actionLabel?: string;
	onPress?(): void;
	secondary?: string;
	onSecondary?(): void;
	/** Shows a close button. */
	onDismiss?(): void;
	/** `"alert"` announces the banner when it appears. */
	accessibilityRole?: "alert";
	accessibilityLabel?: string;
};

/**
 * A status bar across the top of a screen: a headline, at most two lines of
 * detail, and up to two text actions and a close button. One banner language
 * for the chat screen's states and the boards' stale-data notice.
 *
 * The actions are borderless text buttons with full touch targets and a button
 * role, the way a notice's actions read on both platforms; the row grows to
 * hold them rather than shrinking them to the text.
 */
export function InlineBanner({ tone, icon, title, body, action, actionLabel, onPress, secondary, onSecondary, onDismiss, accessibilityRole, accessibilityLabel }: InlineBannerProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const color = tone === "danger" ? t.red : tone === "warning" ? t.amber : t.textTertiary;
	const fill = tone === "danger" ? t.tintRed : tone === "warning" ? t.tintAmber : t.bgSubtle;
	return (
		<View accessibilityRole={accessibilityRole} accessibilityLabel={accessibilityLabel} style={[styles.banner, { backgroundColor: fill }]}>
			<View style={styles.glyph}>
				{icon === "busy"
					? <Spinner size={iconSize.sm} color={color} />
					: <AppSymbol name={icon} size={iconSize.sm} color={color} />}
			</View>
			<View style={styles.copy}>
				<Text style={[styles.title, { color: tone === "muted" ? t.textSecondary : color }]} numberOfLines={body ? 1 : 2}>{title}</Text>
				{body ? <Text style={styles.body} numberOfLines={2}>{body}</Text> : null}
			</View>
			{secondary ? <BannerAction label={secondary} color={t.textSecondary} onPress={onSecondary} /> : null}
			{action ? <BannerAction label={action} accessibilityLabel={actionLabel} color={color} onPress={onPress} /> : null}
			{onDismiss ? <BannerClose title={title} onPress={onDismiss} /> : null}
		</View>
	);
}

function BannerAction({ label, accessibilityLabel, color, onPress }: { label: string; accessibilityLabel?: string; color: string; onPress?(): void }) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button");
	const disabled = !onPress;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			accessibilityState={{ disabled }}
			disabled={disabled}
			android_ripple={feedback.android_ripple}
			onPress={() => { haptics.tap(); onPress?.(); }}
			style={({ pressed }) => [styles.action, pressed && feedback.pressed]}
		>
			<Text style={[styles.actionText, { color }]}>{label}</Text>
		</Pressable>
	);
}

// Closing is housekeeping, not a moment worth a haptic.
function BannerClose({ title, onPress }: { title: string; onPress(): void }) {
	const styles = useThemedStyles(makeStyles);
	const t = useTheme();
	const feedback = usePressFeedback("icon");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Close: ${title}`}
			android_ripple={feedback.android_ripple}
			onPress={onPress}
			style={({ pressed }) => [styles.close, pressed && feedback.pressed]}
		>
			<AppSymbol name="close" size={iconSize.sm} color={t.textTertiary} />
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	banner: { flexDirection: "row", alignItems: "center", gap: space.xs, paddingLeft: space.md, paddingRight: space.xxs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	// One title line tall, so the glyph sits beside the headline however many lines follow it.
	glyph: { alignSelf: "flex-start", height: type.footnote.lineHeight, marginTop: space.sm, justifyContent: "center" },
	copy: { flex: 1, minWidth: 0, paddingVertical: space.sm },
	title: { fontFamily: "DMSans_600SemiBold", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	body: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	action: { minWidth: touchTarget, minHeight: touchTarget, alignItems: "center", justifyContent: "center", paddingHorizontal: space.sm },
	actionText: { fontFamily: "DMSans_600SemiBold", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	close: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
});
