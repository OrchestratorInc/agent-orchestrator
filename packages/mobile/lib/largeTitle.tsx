import { useNavigation } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactElement, type ReactNode } from "react";
import { Platform, StyleSheet, View, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { BAR_SEAM, Frost, FROST_TAIL, frostStops } from "./frost";
import { useTheme } from "./ThemeProvider";
import { font, type } from "./tokens";

type ScrollHandler = (event: NativeSyntheticEvent<NativeScrollEvent>) => void;

/** How far the list scrolls before the bar's title settles from large to the bar's own size. */
export const LARGE_TITLE_COLLAPSE_AT = 28;

/** The title scales between its two sizes this fast. */
const SCALE_MS = 200;
/** The bar's collapsed title size, Material's top app bar title. */
const BAR_TITLE_SIZE = 20;

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
 * `frost`): nearly opaque behind the bar's items, then fading into the page, so rows
 * passing under it dissolve rather than meet a hard edge. The frost used to
 * come up only once the list moved, and the bar read as see-through at rest.
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
 */
export function useLargeTitle(title: string, onScroll?: ScrollHandler, { accessory }: { accessory?: ReactNode } = {}): {
	header: ReactElement | null;
	edge: ReactElement | null;
	onScroll: ScrollHandler | undefined;
	topInset: number;
	accessory: ReactNode;
} {
	const android = Platform.OS === "android";
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
		onScroll?.(event);
	}, [collapsed, onScroll]);
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
	const frost = useMemo(() => (android ? <Frost from="top" stops={frostStops(headerHeight)} style={[styles.frost, { height: headerHeight + FROST_TAIL }]} /> : null), [android, headerHeight]);
	const edge = android ? (
		<>
			{frost}
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
					{accessoryHeight > 0 ? <Frost from="top" stops={frostStops(accessoryHeight, BAR_SEAM)} style={[styles.backing, { height: accessoryHeight + BAR_SEAM }]} /> : null}
					{accessory}
				</View>
			) : null}
		</>
	) : null;
	return { header, edge, onScroll: android ? handle : onScroll, topInset, accessory: android ? null : accessory ?? null };
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
