import Star from "lucide-react-native/icons/star";
import { useEffect, useMemo, useRef } from "react";
import { StyleSheet, Text, View, type FlatList, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { Extrapolation, interpolate, runOnJS, useAnimatedReaction, useAnimatedScrollHandler, useAnimatedStyle, useSharedValue, type SharedValue } from "react-native-reanimated";
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
export const WHEEL_ROW = 36;
/**
 * How many rows show at once, the chosen one in the middle: one either side,
 * so the wheels stay a short band over the keys rather than a tall panel.
 */
export const WHEEL_ROWS = 3;
const PAD = WHEEL_ROW * Math.floor(WHEEL_ROWS / 2);
/** The default's star, a little under the names' cap height. */
const STAR = 14;
/** An agent's mark, about the names' height. */
const MARK = 18;
/** How long a drag let go without a fling waits for a snap it may still start. */
const SETTLE_AFTER_DRAG_MS = 220;
/**
 * A looping wheel's list runs this many rows past its resting place either
 * way, as copies of itself: more than anyone flings through, so it never has
 * to jump back to the middle while turning (a jump showed as a flash).
 */
const LOOP_REACH = 2000;
/**
 * The rows the list builds as it opens: those on show and one either side, so
 * a new agent's wheel is a handful of rows to mount on the tap that picks it
 * (a screenful and a batch after it cost a frame of 25 ms and one of 15). The
 * rest follow a few at a time once it is up.
 */
const FIRST_ROWS = WHEEL_ROWS + 2;
const BATCH_ROWS = 4;

/**
 * How a wheel's rows are laid out: once, or, for a wheel that loops, as copies
 * of the list with the one it rests in at `base`. A list shorter than the rows
 * on show does not loop: it would show one entry twice at once.
 */
export function wheelLoop(count: number): { copies: number; base: number } {
	if (count < WHEEL_ROWS) return { copies: 1, base: 0 };
	const half = Math.ceil(LOOP_REACH / count);
	return { copies: half * 2 + 1, base: half * count };
}

/** The entry a laid-out row shows. */
export function wheelEntry(row: number, count: number): number {
	return ((row % count) + count) % count;
}

export type WheelItem = {
	key: string;
	label: string;
	/** Marked with a star after its name: the agent's default model. */
	starred?: boolean;
	/** An agent whose mark leads the name. */
	mark?: string;
};

/** Where row `row` sits in the list, past the empty rows above the first. */
const rowLayout = (_data: unknown, row: number) => ({ length: WHEEL_ROW, offset: PAD + WHEEL_ROW * row, index: row });

export function WheelPicker({ items, index, onSettle, onEmptyTap, disabled = false, align = "center", accessibilityLabel, testID }: {
	items: readonly WheelItem[];
	/** The row chosen; the wheel rests on it whenever no finger turns it. */
	index: number;
	/** The row the wheel came to rest on, once it is still. */
	onSettle: (index: number) => void;
	/**
	 * A tap on the empty rows above the first item or below the last: they look
	 * like the chat around the picker, so a tap there puts it away.
	 */
	onEmptyTap?: () => void;
	disabled?: boolean;
	align?: "center" | "left" | "right";
	/** What the wheel picks, for a screen reader ("Model"). */
	accessibilityLabel?: string;
	testID?: string;
}) {
	const styles = useThemedStyles(makeStyles);
	const list = useRef<FlatList<number>>(null);
	// Looping like a real wheel: the list comes round again past either end.
	// Only the rows near the band are built (a list), so a catalog of any length
	// (Cursor lists one model per setting) costs the same few rows: laid out in
	// full, its copies passed React Native's limit of children in one view and
	// the app aborted.
	const count = items.length;
	const { copies, base } = wheelLoop(count);
	const rows = useMemo(() => Array.from({ length: count * copies }, (_, row) => row), [count, copies]);
	const restingY = (entry: number) => (base + Math.max(entry, 0)) * WHEEL_ROW;
	const offset = useSharedValue(restingY(index));
	const turning = useRef(false);
	const settleTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const clear = () => {
		if (settleTimer.current !== undefined) clearTimeout(settleTimer.current);
		settleTimer.current = undefined;
	};
	useEffect(() => clear, []);
	// A tap on the empty rows: one whose touch ends where it began. A drag
	// turns the wheel and cancels the touch instead; a tap that stops a wheel
	// still coasting stops it and nothing more.
	const stillAtTouch = useRef(false);
	const emptyRows = onEmptyTap ? {
		onTouchStart: () => { stillAtTouch.current = !turning.current; },
		onTouchEnd: () => { if (stillAtTouch.current && !turning.current) onEmptyTap(); },
	} : null;

	// Resting on the chosen row whenever no finger turns it: on open, and when
	// the choice changes from outside. Android drops a scroll made before the
	// rows are laid out, so it is made again once they are.
	const rest = () => {
		if (turning.current) return;
		const y = restingY(index);
		offset.value = y;
		list.current?.scrollToOffset({ offset: y, animated: false });
	};
	useEffect(rest, [index, count]); // eslint-disable-line react-hooks/exhaustive-deps

	const onScroll = useAnimatedScrollHandler((event) => { offset.value = event.contentOffset.y; });
	// A tick as each row crosses the band, while it turns.
	useAnimatedReaction(
		() => Math.round(offset.value / WHEEL_ROW),
		(row, previous) => { if (previous !== null && row !== previous) runOnJS(haptics.detent)(); },
	);

	const settle = (y: number) => {
		clear();
		turning.current = false;
		const at = Math.round(y / WHEEL_ROW);
		onSettle(copies > 1 ? wheelEntry(at, count) : Math.min(count - 1, Math.max(0, at)));
	};
	const y = (event: NativeSyntheticEvent<NativeScrollEvent>) => event.nativeEvent.contentOffset.y;
	// A screen reader's swipe up or down steps a row, as the wheel coming to
	// rest on it would; it reads the row in the band as the wheel's value. It
	// stops at the ends even on a wheel that loops: a list read aloud has a
	// first and a last entry.
	const step = (by: number) => {
		const next = Math.max(index, 0) + by;
		if (disabled || next < 0 || next >= count) return;
		clear();
		haptics.detent();
		onSettle(next);
	};
	const empty = (where: "above" | "below") => <View testID={testID ? `${testID}-empty-${where}` : undefined} style={styles.empty} {...emptyRows} />;

	return (
		<Animated.FlatList
			ref={list}
			testID={testID}
			style={styles.wheel}
			data={rows}
			keyExtractor={String}
			getItemLayout={rowLayout}
			renderItem={({ item: row }) => {
				const item = items[wheelEntry(row, count)];
				return <WheelRow label={item.label} starred={item.starred} mark={item.mark} at={row} offset={offset} align={align} />;
			}}
			ListHeaderComponent={empty("above")}
			ListFooterComponent={empty("below")}
			initialScrollIndex={Math.max(0, base + Math.max(index, 0) - Math.floor(WHEEL_ROWS / 2))}
			initialNumToRender={FIRST_ROWS}
			maxToRenderPerBatch={BATCH_ROWS}
			windowSize={5}
			contentOffset={{ x: 0, y: restingY(index) }}
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
		/>
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
		// The empty rows that let the first and last items reach the band.
		empty: { height: PAD },
		row: { height: WHEEL_ROW, justifyContent: "center", paddingHorizontal: 6 },
		// One face for every row, as iOS's picker: regular weight at a size of its
		// own, no tightened tracking. Only brightness and the tilt set the chosen
		// row apart; bold everywhere read as shouting, and the scale skewed it.
		line: { flexDirection: "row", alignItems: "center", gap: 8 },
		label: { fontFamily: font.regular, fontSize: 18, lineHeight: 24, fontWeight: "400", letterSpacing: 0, color: t.textPrimary, flexShrink: 1 },
		left: { justifyContent: "flex-start" },
		right: { justifyContent: "flex-end" },
		centre: { justifyContent: "center" },
	});
