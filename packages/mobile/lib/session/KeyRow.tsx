import { useCallback, useEffect, useRef } from "react";
import { Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import type { Theme } from "../theme";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { CONTROL_KEYS, KEY_REPEAT, type ControlKey } from "./keys";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, iconSize, radius, space, touchTarget, type } from "../tokens";

// The control-key row: one fixed row of eight keys that divide the width
// between them. It stays custom, and on screen with the keyboard down: a native
// input accessory exists only while the keyboard is up.
//
// It used to be a `flexWrap` row that also held zoom, the mic and two mode
// toggles, two of which carried `marginLeft: "auto"` — so turning a toggle on
// reflowed the whole second line. Fixed count, fixed height, `flex: 1` each:
// this row is pixel-identical in every state the screen can be in.
export function KeyRow({ onKey }: { onKey: (seq: string) => void }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.row}>
			{CONTROL_KEYS.map((k) => <Key key={k.id} control={k} onKey={onKey} />)}
		</View>
	);
}

function Key({ control, onKey }: { control: ControlKey; onKey: (seq: string) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Android answers with the ripple alone; iOS darkens the key, as its own
	// keyboard does, rather than fading it.
	const ripple = usePressFeedback("button").android_ripple;
	const press = useKeyPress(() => onKey(control.seq), control.repeats === true);
	const { ios, android } = control.symbol;
	const textOnly = Platform.OS === "android" && !android;
	const text = textOnly ? control.androidText : control.suffix;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={control.hint}
			{...press}
			android_ripple={ripple}
			style={({ pressed }) => [styles.key, pressed && !ripple && styles.keyPressed]}
		>
			{/* iOS reads only its own name from the pair, so a cap Android draws as
			    text still gets one for AppSymbol; that Android name is never drawn. */}
			{textOnly ? null : <AppSymbol name={{ ios, android: android ?? "keyboard" }} size={iconSize.md} color={t.textPrimary} />}
			{text ? (
				// One line, always: eight keys share the width, so the text is
				// capped and shrinks to fit rather than wrapping.
				<Text
					style={styles.keyText}
					numberOfLines={1}
					maxFontSizeMultiplier={fontScaleCap.chrome}
					adjustsFontSizeToFit
					minimumFontScale={0.6}
				>
					{text}
				</Text>
			) : null}
		</Pressable>
	);
}

/**
 * A key fires once per tap; a repeating key fires the moment it goes down and
 * then again every interval while it is held, like a hardware arrow key.
 *
 * Screen readers activate with a press alone (TalkBack) or a quick down-up
 * (VoiceOver), so a repeating key also fires from `onPress` when no press-in
 * came first, and only one of the two counts for a single tap.
 */
function useKeyPress(fire: () => void, repeats: boolean) {
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const firedOnPressIn = useRef(false);
	const fireRef = useRef(fire);
	fireRef.current = fire;

	const stop = useCallback(() => {
		if (timer.current) clearTimeout(timer.current);
		timer.current = null;
	}, []);
	useEffect(() => stop, [stop]);

	const onPressIn = useCallback(() => {
		if (!repeats) return;
		stop();
		haptics.tap();
		fireRef.current();
		firedOnPressIn.current = true;
		const tick = () => {
			fireRef.current();
			timer.current = setTimeout(tick, KEY_REPEAT.intervalMs);
		};
		timer.current = setTimeout(tick, KEY_REPEAT.delayMs);
	}, [repeats, stop]);

	const onPressOut = useCallback(() => {
		stop();
		// `onPress` follows `onPressOut` in the same dispatch when the press
		// completes; a press that slid off gets none, so clear the mark after.
		setTimeout(() => {
			firedOnPressIn.current = false;
		}, 0);
	}, [stop]);

	const onPress = useCallback(() => {
		if (firedOnPressIn.current) {
			firedOnPressIn.current = false;
			return;
		}
		haptics.tap();
		fireRef.current();
	}, []);

	return { onPressIn, onPressOut, onPress };
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		row: {
			flexDirection: "row",
			gap: space.xxs,
			paddingHorizontal: space.sm,
			paddingTop: space.xs,
		},
		// Borderless tonal keys: the fill alone separates them from the dock.
		key: {
			flex: 1,
			flexDirection: "row",
			height: touchTarget,
			gap: space.hair,
			borderRadius: radius.sm,
			borderCurve: "continuous",
			overflow: "hidden",
			backgroundColor: t.accentTint,
			alignItems: "center",
			justifyContent: "center",
		},
		keyPressed: { backgroundColor: t.accentPressed },
		keyText: { color: t.textPrimary, fontFamily: t.fontMono, fontSize: type.subheadline.fontSize },
	});
