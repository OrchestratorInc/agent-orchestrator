import { useNavigation } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from "react";
import { Platform, StyleSheet, View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { BAR_SEAM, Frost, FROST_TAIL, frostStops, SEAM_STOPS, type FrostStop } from "./frost";
import { useTheme } from "./ThemeProvider";
import { font, type } from "./tokens";

type ScrollHandler = (event: NativeSyntheticEvent<NativeScrollEvent>) => void;

/** How far the list scrolls before the bar's title settles from large to the bar's own size. */
export const LARGE_TITLE_COLLAPSE_AT = 28;

/** The title scales between its two sizes this fast. */
const SCALE_MS = 200;
/** The bar's collapsed title size, Material's top app bar title. */
const BAR_TITLE_SIZE = 20;
/** A solid bar's seam, and a lifted frost's tail, fade in and out this fast as the list leaves and returns to the top. */
const SEAM_MS = 150;

/**
 * What backs the Android bar the list runs on under.
 *
 * `frost`: the bar's frost (see `frost`), always up, dissolving into the page
 * over a long tail, so rows passing under it dim out.
 *
 * `solid`: the page's own colour, opaque, the bar's height and no deeper, so
 * nothing at all lies over the page at rest (the board's machine chips sat in
 * the frost's tail and read dimmed). Rows scrolling up meet the bar at a short
 * seam that comes up only once the list has left the top: at rest there is
 * nothing under the bar for it to soften.
 *
 * `lift` (the default): the frost over the bar, always up, with its tail
 * (and the pinned line's) coming up only once the list has left the top. Rows scrolling up
 * still dissolve into the bar over the long tail, but at rest nothing lies over
 * the page, so the board's machine chips read at full strength.
 */
export type LargeTitleBackdrop = "frost" | "solid" | "lift";

export function seamShown(y: number): boolean {
	return y > 0;
}

export function largeTitleCollapsed(y: number): boolean {
	return y > LARGE_TITLE_COLLAPSE_AT;
}

/**
 * A board tab's large title on Android: the name set big in the bar itself, on
 * the row its actions sit on, scaling down to the bar's own title size once
 * the list scrolls and back up at the top. iOS's large-title bar does its own,
 * so there the hook adds nothing and hands `onScroll` back as given.
 *
 * The list runs on under the bar, which is always backed by its frost (see
 * `frost`): nearly opaque behind the bar's items, so the bar never reads as
 * see-through. The frost's fade into the page comes up only once the list
 * leaves the top, so rows passing under the bar dissolve rather than meet a
 * hard edge, while at rest nothing lies over the page's first rows (see
 * `LargeTitleBackdrop` for the other backings).
 *
 * `header` goes first in the page's scroll view and holds its content clear of
 * the bar (`topInset` says how far, for anything else pinned to the page's top); `edge`
 * goes in the page after the scroll view, under anything that floats; and the
 * scroll view takes `onScroll`, which calls the one passed in too.
 *
 * `accessory` is a line that belongs under the title, such as the page's
 * freshness line. On iOS it comes back as `accessory` for the page to place
 * under its large title. On Android it is drawn by `edge`, pinned under the bar
 * over the frost: placed in the page instead, it sat in the frost's fade and
 * read washed out whenever nothing else (the machine chips) came first. The
 * frost keeps to the bar, so the page's first row starts no deeper in its fade
 * than it did without one; the line carries its own backing, a frost its height
 * plus a short seam, so rows scrolling under it don't show through its text.
 *
 * TalkBack reads a page's overlays after its full-screen list (Android orders a
 * view's children by where they sit), so the pinned line is a picture only. Its
 * twin, invisible but accessible, goes in `header` right under the bar, where
 * the line sits at rest: TalkBack reaches it and its Retry before the rows, as
 * it did when the line was in the page, and its live region announces changes.
 * The twin also makes room for the line in the same layout pass, so the line
 * never draws over the first row; `topInset` includes its height.
 *
 * `resetKey` is the key a page replaces its scroll view by, as the board does
 * for each filter. A new list starts at its top without reporting a scroll, so
 * the title and the seam go back to their resting state when the key changes
 * instead of describing the list that was replaced.
 */
export function useLargeTitle(title: string, onScroll?: ScrollHandler, { accessory, backdrop = "lift", resetKey }: { accessory?: ReactNode; backdrop?: LargeTitleBackdrop; resetKey?: string } = {}): {
	header: ReactElement | null;
	edge: ReactElement | null;
	onScroll: ScrollHandler | undefined;
	topInset: number;
	accessory: ReactNode;
} {
	const android = Platform.OS === "android";
	const solid = backdrop === "solid";
	const lift = backdrop === "lift";
	const t = useTheme();
	const navigation = useNavigation();
	const headerHeight = useHeaderHeight();
	// The accessory's measured height (its twin's, in the header); 0 while it draws nothing.
	const [accessoryHeight, setAccessoryHeight] = useState(0);
	const pinned = android && accessory != null && accessory !== false;
	const topInset = android ? headerHeight + (pinned ? accessoryHeight : 0) : 0;
	const measureAccessory = useCallback((event: LayoutChangeEvent) => {
		const height = Math.round(event.nativeEvent.layout.height);
		setAccessoryHeight((previous) => (previous === height ? previous : height));
	}, []);
	const collapsed = useSharedValue(0);
	const last = useRef(0);
	// A solid bar's seam, or a lifted frost's tail: up while the list is off the top.
	const scrolled = useSharedValue(0);
	const lastScrolled = useRef(0);
	const shownKey = useRef(resetKey);
	useLayoutEffect(() => {
		if (shownKey.current === resetKey) return;
		shownKey.current = resetKey;
		if (!android) return;
		if (last.current !== 0) collapsed.value = 0;
		if (lastScrolled.current !== 0) scrolled.value = 0;
		last.current = 0;
		lastScrolled.current = 0;
	}, [android, collapsed, resetKey, scrolled]);
	useLayoutEffect(() => {
		if (!android) return;
		// Set once: the title reads the shared value, so collapsing never
		// registers the header again.
		navigation.setOptions({
			headerTitle: () => <BarTitle title={title} collapsed={collapsed} />,
			headerTransparent: true,
			headerStyle: { backgroundColor: "transparent" },
		});
	}, [android, collapsed, navigation, title]);
	const handle = useCallback<ScrollHandler>((event) => {
		const next = largeTitleCollapsed(event.nativeEvent.contentOffset.y) ? 1 : 0;
		if (next !== last.current) collapsed.value = next;
		last.current = next;
		if (solid || lift) {
			const off = seamShown(event.nativeEvent.contentOffset.y) ? 1 : 0;
			if (off !== lastScrolled.current) scrolled.value = off;
			lastScrolled.current = off;
		}
		onScroll?.(event);
	}, [collapsed, lift, onScroll, scrolled, solid]);
	const header = android ? (
		<View>
			<View style={{ height: headerHeight }} />
			{pinned ? (
				// The pinned line's accessible twin (see above). Not collapsable, so it
				// measures 0 when the accessory draws nothing and the page closes up.
				<View collapsable={false} onLayout={measureAccessory} style={styles.twin}>
					{accessory}
				</View>
			) : null}
		</View>
	) : null;
	// Sized to the bar alone: grown to back the accessory too, its fade moved down
	// onto the page's first row, which then read dimmed.
	const frost = useMemo(() => {
		if (!android) return null;
		if (solid) return <View pointerEvents="none" style={[styles.frost, { height: headerHeight, backgroundColor: t.bgBase }]} />;
		if (lift) return <Frost from="top" stops={splitStops(frostStops(headerHeight), headerHeight / (headerHeight + FROST_TAIL)).body} style={[styles.frost, { height: headerHeight }]} />;
		return <Frost from="top" stops={frostStops(headerHeight)} style={[styles.frost, { height: headerHeight + FROST_TAIL }]} />;
	}, [android, headerHeight, lift, solid, t.bgBase]);
	const seamStyle = useAnimatedStyle(() => ({ opacity: withTiming(scrolled.value, { duration: SEAM_MS }) }));
	// Under the bar, or under the pinned line when there is one.
	const seamTop = headerHeight + (pinned ? accessoryHeight : 0);
	// A lifted frost's tail: the frost's own tail under the bar, and under the
	// pinned line its short one, so the line keeps the same backing it had.
	const liftTail = pinned && accessoryHeight > 0
		? splitStops(frostStops(accessoryHeight, BAR_SEAM), accessoryHeight / (accessoryHeight + BAR_SEAM)).tail
		: splitStops(frostStops(headerHeight), headerHeight / (headerHeight + FROST_TAIL)).tail;
	const edge = android ? (
		<>
			{frost}
			{solid ? (
				<Animated.View pointerEvents="none" style={[styles.frost, { top: seamTop, height: BAR_SEAM }, seamStyle]}>
					<Frost from="top" stops={SEAM_STOPS} style={StyleSheet.absoluteFill} />
				</Animated.View>
			) : null}
			{lift ? (
				<Animated.View pointerEvents="none" style={[styles.frost, { top: seamTop, height: pinned && accessoryHeight > 0 ? BAR_SEAM : FROST_TAIL }, seamStyle]}>
					<Frost from="top" stops={liftTail} style={StyleSheet.absoluteFill} />
				</Animated.View>
			) : null}
			{pinned ? (
				// A picture for TalkBack, which reads the twin in the header; box-none so
				// only its own controls take taps.
				// Not collapsable: flattened away, it took no-hide-descendants with it and
				// TalkBack found this copy as well as the twin.
				<View
					collapsable={false}
					accessibilityElementsHidden
					importantForAccessibility="no-hide-descendants"
					pointerEvents="box-none"
					style={[styles.accessory, { top: headerHeight }]}
				>
					{accessoryHeight > 0 ? (solid
						? <View style={[styles.backing, { height: accessoryHeight, backgroundColor: t.bgBase }]} />
						: lift
						? <Frost from="top" stops={splitStops(frostStops(accessoryHeight, BAR_SEAM), accessoryHeight / (accessoryHeight + BAR_SEAM)).body} style={[styles.backing, { height: accessoryHeight }]} />
						: <Frost from="top" stops={frostStops(accessoryHeight, BAR_SEAM)} style={[styles.backing, { height: accessoryHeight + BAR_SEAM }]} />) : null}
					{accessory}
				</View>
			) : null}
		</>
	) : null;
	return { header, edge, onScroll: android ? handle : onScroll, topInset, accessory: android ? null : accessory ?? null };
}

/**
 * A frost's stops cut at `at` (the bar's end), each half stretched back over
 * 0-1: `body` for a frost the bar's height, `tail` for one the tail's, drawn
 * just under it. Laid end to end they draw the uncut frost.
 */
export function splitStops(stops: readonly FrostStop[], at: number): { body: FrostStop[]; tail: FrostStop[] } {
	const opacityAt = (offset: number) => {
		const after = stops.findIndex(([o]) => o >= offset);
		if (after <= 0) return stops[Math.max(after, 0)][1];
		const [o0, a0] = stops[after - 1];
		const [o1, a1] = stops[after];
		return a0 + ((a1 - a0) * (offset - o0)) / (o1 - o0);
	};
	const edge = opacityAt(at);
	const body: FrostStop[] = [...stops.filter(([o]) => o < at).map(([o, a]): FrostStop => [o / at, a]), [1, edge]];
	const tail: FrostStop[] = [[0, edge], ...stops.filter(([o]) => o > at).map(([o, a]): FrostStop => [(o - at) / (1 - at), a])];
	return { body, tail };
}

function BarTitle({ title, collapsed }: { title: string; collapsed: SharedValue<number> }) {
	const t = useTheme();
	// Scaled rather than re-laid out at a new size, from its leading edge so it
	// stays put against the bar's start.
	const scaled = useAnimatedStyle(() => ({
		transform: [{ scale: withTiming(1 - collapsed.value * (1 - BAR_TITLE_SIZE / type.title1.fontSize), { duration: SCALE_MS }) }],
	}));
	return (
		<Animated.Text accessibilityRole="header" numberOfLines={1} style={[styles.bar, { color: t.textPrimary }, scaled]}>
			{title}
		</Animated.Text>
	);
}

const styles = StyleSheet.create({
	// The ramp's title1 (largeTitle read too big), set as heavy as iOS's (`largeTitleScreenOptions`), in the app's
	// own face: semibold read thin this big, and the system's Roboto at the same
	// weight looked foreign beside the DM Sans rows. No `fontWeight`: on Android
	// a bold weight asks for the family's bold style, which expo-font never
	// registers, and the text fell back to Roboto.
	bar: { fontFamily: font.bold, fontSize: type.title1.fontSize, lineHeight: type.title1.lineHeight, letterSpacing: type.title1.letterSpacing, transformOrigin: "left center" },
	frost: { position: "absolute", top: 0, start: 0, end: 0 },
	accessory: { position: "absolute", start: 0, end: 0 },
	backing: { position: "absolute", top: 0, start: 0, end: 0 },
	// Drawn by its pinned copy; present for TalkBack and for the room it takes.
	// Not 0: Android leaves a fully transparent view out of the accessibility
	// tree, and TalkBack then reached only the pinned copy, after every row.
	twin: { opacity: 0.01 },
});
