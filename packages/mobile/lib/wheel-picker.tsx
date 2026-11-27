import Star from "lucide-react-native/icons/star";
import { useEffect, useRef } from "react";
import { StyleSheet, Text, View, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { Extrapolation, interpolate, runOnJS, useAnimatedReaction, useAnimatedRef, useAnimatedScrollHandler, useAnimatedStyle, useSharedValue, type SharedValue } from "react-native-reanimated";
import { AgentLogo } from "./AgentLogo";
import { haptics } from "./haptics";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import type { Theme } from "./theme";
import { font } from "./tokens";

/*
 * A wheel, as iOS's date picker draws its columns: the rows roll past a band
 * in the middle, the one in the band in full, the others smaller, fainter and
 * tilted away the further they are from it. It snaps a row at a time, ticks as
 * each row crosses the band, and reports the row it comes to rest on.
 */

/** Every row this tall. */
export const WHEEL_ROW = 40;
/** How many rows show at once, the chosen one in the middle. */
export const WHEEL_ROWS = 5;
const PAD = WHEEL_ROW * Math.floor(WHEEL_ROWS / 2);
/** The default's star, a little under the names' cap height. */
const STAR = 14;
/** An agent's mark, about the names' height. */
const MARK = 20;
/** How long a drag let go without a fling waits for a snap it may still start. */
const SETTLE_AFTER_DRAG_MS = 220;

export type WheelItem = {
	key: string;
	label: string;
	/** Marked with a star after its name: the agent's default model. */
	starred?: boolean;
	/** An agent whose mark leads the name. */
	mark?: string;
};

export function WheelPicker({ items, index, onSettle, disabled = false, align = "center", accessibilityLabel, testID }: {
	items: readonly WheelItem[];
	/** The row chosen; the wheel rests on it whenever no finger turns it. */
	index: number;
	/** The row the wheel came to rest on, once it is still. */
	onSettle: (index: number) => void;
	disabled?: boolean;
	align?: "center" | "left" | "right";
	/** What the wheel picks, for a screen reader ("Model"). */
	accessibilityLabel?: string;
	testID?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	const scroller = useAnimatedRef<Animated.ScrollView>();
	const offset = useSharedValue(Math.max(index, 0) * WHEEL_ROW);
	const turning = useRef(false);
	const settleTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const clear = () => {
		if (settleTimer.current !== undefined) clearTimeout(settleTimer.current);
		settleTimer.current = undefined;
	};
	useEffect(() => clear, []);

	// Resting on the chosen row whenever no finger turns it: on open, and when
	// the choice changes from outside (another column picked a new list).
	// Android has no starting offset and drops a scroll made before the rows
	// are laid out, so it is made again once they are.
	const rest = () => {
		if (turning.current) return;
		const y = Math.max(index, 0) * WHEEL_ROW;
		offset.value = y;
		scroller.current?.scrollTo({ y, animated: false });
	};
	useEffect(rest, [index, items]); // eslint-disable-line react-hooks/exhaustive-deps

	const onScroll = useAnimatedScrollHandler((event) => { offset.value = event.contentOffset.y; });
	// A tick as each row crosses the band, while it turns.
	useAnimatedReaction(
		() => Math.round(offset.value / WHEEL_ROW),
		(row, previous) => { if (previous !== null && row !== previous) runOnJS(haptics.detent)(); },
	);

	const settle = (y: number) => {
		clear();
		turning.current = false;
		const row = Math.min(items.length - 1, Math.max(0, Math.round(y / WHEEL_ROW)));
		onSettle(row);
	};
	const y = (event: NativeSyntheticEvent<NativeScrollEvent>) => event.nativeEvent.contentOffset.y;
	// A screen reader's swipe up or down steps a row, as the wheel coming to
	// rest on it would; it reads the row in the band as the wheel's value.
	const step = (by: number) => {
		const next = Math.max(index, 0) + by;
		if (disabled || next < 0 || next >= items.length) return;
		clear();
		haptics.detent();
		onSettle(next);
	};

	return (
		<Animated.ScrollView
			ref={scroller}
			testID={testID}
			style={styles.wheel}
			contentContainerStyle={styles.content}
			contentOffset={{ x: 0, y: Math.max(index, 0) * WHEEL_ROW }}
			showsVerticalScrollIndicator={false}
			snapToInterval={WHEEL_ROW}
			decelerationRate="fast"
			scrollEnabled={!disabled}
			overScrollMode="never"
			// The wheels sit over a focused composer. A scroll view's default
			// ("never") blurs the focused field on a tap that lands on it, so a
			// tap on a wheel, or on the empty rows above or below its first and
			// last items, dropped the keyboard (and the tap never reached the
			// overlay to put it away).
			keyboardShouldPersistTaps="always"
			scrollEventThrottle={16}
			onScroll={onScroll}
			onContentSizeChange={rest}
			onScrollBeginDrag={() => { clear(); turning.current = true; }}
			onMomentumScrollBegin={() => { clear(); turning.current = true; }}
			onScrollEndDrag={(event) => {
				// Let go without a fling: the snap may yet start a glide of its
				// own; settle here only if none does.
				const at = y(event);
				clear();
				settleTimer.current = setTimeout(() => settle(at), SETTLE_AFTER_DRAG_MS);
			}}
			onMomentumScrollEnd={(event) => settle(y(event))}
			accessible
			accessibilityRole="adjustable"
			accessibilityLabel={accessibilityLabel}
			accessibilityValue={{ text: items[index]?.label ?? items[0]?.label }}
			accessibilityState={{ disabled }}
			accessibilityActions={[{ name: "increment" }, { name: "decrement" }]}
			onAccessibilityAction={(event) => step(event.nativeEvent.actionName === "increment" ? 1 : -1)}
		>
			{items.map((item, i) => (
				<WheelRow key={item.key} label={item.label} starred={item.starred} mark={item.mark} at={i} offset={offset} align={align} />
			))}
		</Animated.ScrollView>
	);
}

function WheelRow({ label, starred, mark, at, offset, align }: { label: string; starred?: boolean; mark?: string; at: number; offset: SharedValue<number>; align: "center" | "left" | "right" }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// How far, in rows, this one is from the band: the further, the smaller,
	// fainter and more tilted away.
	const look = useAnimatedStyle(() => {
		const away = (at * WHEEL_ROW - offset.value) / WHEEL_ROW;
		const far = Math.abs(away);
		return {
			opacity: interpolate(far, [0, 1, 2, 2.6], [1, 0.42, 0.18, 0], Extrapolation.CLAMP),
			transform: [
				{ perspective: 600 },
				{ rotateX: `${interpolate(away, [-3, 0, 3], [54, 0, -54], Extrapolation.CLAMP)}deg` },
				{ scale: interpolate(far, [0, 2], [1, 0.94], Extrapolation.CLAMP) },
			],
		};
	});
	return (
		<View style={styles.row}>
			<Animated.View style={[styles.line, align === "left" ? styles.left : align === "right" ? styles.right : styles.centre, look]}>
				{mark ? <AgentLogo harness={mark} size={MARK} /> : null}
				{/* A long name shrinks a little to fit rather than losing its end. */}
				<Text numberOfLines={1} adjustsFontSizeToFit minimumFontScale={0.75} style={styles.label}>{label}</Text>
				{starred ? <Star size={STAR} color={t.textSecondary} fill={t.textSecondary} strokeWidth={1.5} accessibilityLabel="Default" /> : null}
			</Animated.View>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		wheel: { height: WHEEL_ROW * WHEEL_ROWS, flexGrow: 0 },
		content: { paddingVertical: PAD },
		row: { height: WHEEL_ROW, justifyContent: "center", paddingHorizontal: 6 },
		// One face for every row, as iOS's picker: regular weight at a size of its
		// own, no tightened tracking. Only brightness and the tilt set the chosen
		// row apart; bold everywhere read as shouting, and the scale skewed it.
		line: { flexDirection: "row", alignItems: "center", gap: 8 },
		label: { fontFamily: font.regular, fontSize: 20, lineHeight: 26, fontWeight: "400", letterSpacing: 0, color: t.textPrimary, flexShrink: 1 },
		left: { justifyContent: "flex-start" },
		right: { justifyContent: "flex-end" },
		centre: { justifyContent: "center" },
	});
