import { useEffect } from "react";
import { AccessibilityInfo, StyleSheet, Text, View } from "react-native";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "./AppSymbol";
import { rowDividerWidth } from "./divider";
import { BANNER_MS } from "./motion";
import type { AppSymbolName } from "./symbols";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import type { ToastDuration, ToastHostProps, ToastTone } from "./Toast.types";
import { font, fontScaleCap, gutter, iconSize, radius, space, type } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

export type { ToastController, ToastDuration, ToastHostProps, ToastMessage, ToastOptions, ToastTone } from "./Toast.types";

/** How long the HUD stays up. Long enough to read a sentence, short enough not to cover the screen. */
const VISIBLE_MS: Record<ToastDuration, number> = { short: 3_000, long: 6_000 };

const toneGlyph: Record<ToastTone, AppSymbolName | undefined> = { neutral: undefined, success: "checkCircle", warning: "warning" };

/**
 * The iOS transient message: a capsule HUD above the bottom edge. iOS has no
 * system toast, so this is the one custom piece; it fades rather than slides,
 * holds still when Reduce Motion is on, never takes a touch, and is announced
 * to VoiceOver because a HUD is otherwise silent.
 */
export function ToastHost({ toast, bottomOffset }: ToastHostProps) {
	const t = useTheme();
	const s = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const reduceMotion = useReducedMotion();
	const { current, hide } = toast;

	useEffect(() => {
		if (!current) return;
		AccessibilityInfo.announceForAccessibility(current.text);
		const timer = setTimeout(() => hide(current.id), VISIBLE_MS[current.duration]);
		return () => clearTimeout(timer);
	}, [current, hide]);

	const glyph = current ? toneGlyph[current.tone] : undefined;
	// The anchor stays mounted so the HUD inside it can run its exit fade.
	return (
		<View pointerEvents="none" style={[s.anchor, { bottom: bottomOffset ?? insets.bottom + space.xl }]}>
			{current ? (
				<Animated.View
					key={current.id}
					entering={reduceMotion ? undefined : FadeIn.duration(BANNER_MS)}
					exiting={reduceMotion ? undefined : FadeOut.duration(BANNER_MS)}
					style={s.hud}
					testID="toast"
				>
					{glyph ? <AppSymbol name={glyph} size={iconSize.sm} color={current.tone === "success" ? t.green : t.amber} /> : null}
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={s.text}>{current.text}</Text>
				</Animated.View>
			) : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	anchor: { position: "absolute", left: gutter, right: gutter, alignItems: "center" },
	hud: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		maxWidth: "100%",
		paddingHorizontal: space.lg,
		paddingVertical: space.sm,
		// A capsule on one line; a wrapped message keeps rounded corners rather
		// than swelling into a stadium.
		borderRadius: radius.xl,
		backgroundColor: t.bgElevated,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
	},
	text: { flexShrink: 1, fontFamily: font.medium, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500", color: t.textPrimary },
});
