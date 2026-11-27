import Star from "lucide-react-native/icons/star";
import { useEffect, useMemo, useRef, useState } from "react";
import { FlatList, StyleSheet, Text, View } from "react-native";
import { Pressable } from "react-native-gesture-handler";
import { AgentLogo } from "./AgentLogo";
import { fadeGradient } from "./effort-slider-veil";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { font, radius, space } from "./tokens";

/*
 * An agent's models as a plain list: a tap picks one. The one in force sits in
 * a soft band. The list opens where it starts when that model shows there, and
 * otherwise with it in the middle; it is never scrolled to pin it at the top.
 * A longer list fades out at its foot, so it reads as more to scroll. Only the
 * rows near what shows are built, so a catalog of any length (Cursor lists one
 * model per setting) costs the same few rows.
 */

/** Every row this tall: a thumb's press. */
export const LIST_ROW = 46;
/** How many rows show at once. */
export const LIST_ROWS = 5;

export type WheelItem = {
	key: string;
	label: string;
	/** Marked with a star after its name: the agent's default model. */
	starred?: boolean;
	/** An agent whose mark leads the name. */
	mark?: string;
};

const rowLayout = (_data: unknown, row: number) => ({ length: LIST_ROW, offset: LIST_ROW * row, index: row });

/**
 * Where the list opens for the row in force: at its start when that row shows
 * there, else with it in the middle, never past the list's end.
 */
export function listOffset(index: number, count: number): number {
	if (index < LIST_ROWS || count <= LIST_ROWS) return 0;
	const centred = (index - Math.floor(LIST_ROWS / 2)) * LIST_ROW;
	return Math.min(centred, (count - LIST_ROWS) * LIST_ROW);
}

// The fade at a longer list's foot: about a row, the page's colour coming in.
const FADE_ROWS = 1.2;
const STAR = 14;
const MARK = 20;

export function ModelList({ items, index, visible = true, onPick, accessibilityLabel, testID }: {
	items: readonly WheelItem[];
	/** The list is on show: each time it opens, it opens on the row in force. */
	visible?: boolean;
	/** The row in force, highlighted and shown first. */
	index: number;
	/** A row was tapped. */
	onPick: (index: number) => void;
	/** What the list picks, for a screen reader ("Model"). */
	accessibilityLabel?: string;
	testID?: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const list = useRef<FlatList<WheelItem>>(null);
	// Kept built between opens, it kept wherever it was last scrolled: each
	// open starts again with the row in force on show.
	const offset = listOffset(index, items.length);
	useEffect(() => {
		if (visible) list.current?.scrollToOffset({ offset, animated: false });
	}, [visible, offset]);
	const fade = useMemo(() => fadeGradient(t.bgBase, [0, 0.55, 0.9, 1], "to bottom"), [t.bgBase]);
	const fadeTop = useMemo(() => fadeGradient(t.bgBase, [0, 0.55, 0.9, 1], "to top"), [t.bgBase]);
	// Rows above: the first row on show fades too, as the last does. Only once
	// scrolled, so a list at its start never dims its first model.
	const [above, setAbove] = useState(offset > 0);
	useEffect(() => { setAbove(offset > 0); }, [offset, visible]);
	// The row the scroll last ticked on.
	const rowAt = useRef(Math.round(offset / LIST_ROW));
	useEffect(() => { rowAt.current = Math.round(offset / LIST_ROW); }, [offset, visible]);
	// Rows below: the last row on show fades, as a cue to scroll. Not at the
	// list's end, where it is the last model and must read in full.
	const end = Math.max(0, (items.length - LIST_ROWS) * LIST_ROW);
	const [below, setBelow] = useState(items.length > LIST_ROWS && offset < end - 2);
	useEffect(() => { setBelow(items.length > LIST_ROWS && offset < end - 2); }, [offset, end, items.length, visible]);
	return (
		<View style={styles.frame}>
		<FlatList
			ref={list}
			testID={testID}
			style={styles.list}
			data={items as WheelItem[]}
			keyExtractor={(item) => item.key}
			getItemLayout={rowLayout}
			contentOffset={{ x: 0, y: offset }}
			// Comes to rest on whole rows: a row cut by the list's edge (the pill
			// in force among them) shows only while it moves, under the fades.
			snapToInterval={LIST_ROW}
			decelerationRate="fast"
			scrollEventThrottle={16}
			onScroll={(event) => {
				const y = event.nativeEvent.contentOffset.y;
				// A faint tick as each row passes.
				const passing = Math.round(y / LIST_ROW);
				if (passing !== rowAt.current) {
					rowAt.current = passing;
					haptics.scrollTick();
				}
				const scrolled = y > 2;
				const more = items.length > LIST_ROWS && y < end - 2;
				if (scrolled !== above) setAbove(scrolled);
				if (more !== below) setBelow(more);
			}}
			initialNumToRender={LIST_ROWS + 2}
			maxToRenderPerBatch={8}
			windowSize={5}
			showsVerticalScrollIndicator={false}
			// The list sits over a focused composer: a tap on it must not blur the
			// field and drop the keyboard.
			keyboardShouldPersistTaps="always"
			accessibilityRole="radiogroup"
			accessibilityLabel={accessibilityLabel}
			renderItem={({ item, index: row }) => {
				const on = row === index;
				return (
					<Pressable
						onPress={() => { haptics.select(); onPick(row); }}
						accessibilityRole="radio"
						accessibilityState={{ checked: on }}
						accessibilityLabel={item.starred ? `${item.label}, default` : item.label}
						testID={testID ? `${testID}-${item.key}` : undefined}
						style={({ pressed }) => [styles.row, on ? styles.rowOn : pressed && styles.rowPressed]}
					>
						{item.mark ? <AgentLogo harness={item.mark} size={MARK} /> : null}
						<Text numberOfLines={1} style={[styles.label, on && styles.labelOn]}>{item.label}</Text>
						{item.starred ? <Star size={STAR} color={t.textSecondary} fill={t.textSecondary} strokeWidth={1.5} /> : null}
					</Pressable>
				);
			}}
		/>
		{/* More above: the first row fades into the page. */}
		{above ? <View pointerEvents="none" style={[styles.fadeTop, { experimental_backgroundImage: fadeTop }]} /> : null}
		{/* More below: the last row fades into the page, as a cue to scroll. */}
		{below ? <View pointerEvents="none" style={[styles.fade, { experimental_backgroundImage: fade }]} /> : null}
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		frame: { height: LIST_ROW * LIST_ROWS },
		list: { height: LIST_ROW * LIST_ROWS, flexGrow: 0 },
		fade: { position: "absolute", left: 0, right: 0, bottom: 0, height: LIST_ROW * FADE_ROWS },
		fadeTop: { position: "absolute", left: 0, right: 0, top: 0, height: LIST_ROW * FADE_ROWS },
		row: {
			height: LIST_ROW,
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "center",
			gap: 8,
			paddingHorizontal: space.md,
			borderRadius: radius.lg,
			borderCurve: "continuous",
		},
		rowOn: { backgroundColor: t.bgElevated },
		rowPressed: { backgroundColor: t.bgSubtle },
		// One face for every row; the one in force reads brighter, not bolder.
		label: { fontFamily: font.regular, fontSize: 18, lineHeight: 24, color: t.textSecondary, flexShrink: 1 },
		labelOn: { color: t.textPrimary },
	});
