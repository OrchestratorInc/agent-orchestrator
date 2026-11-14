import { useNavigation } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useLayoutEffect, useMemo, useRef, type ReactElement } from "react";
import { Platform, StyleSheet, View, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { Frost, FROST_TAIL, frostStops } from "./frost";
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
 * `frost`): solid behind the bar's items, then fading into the page, so rows
 * passing under it dissolve rather than meet a hard edge. The frost used to
 * come up only once the list moved, and the bar read as see-through at rest.
 *
 * `header` goes first in the page's scroll view and holds its content clear of
 * the bar (`topInset` says how far, for anything else pinned to the page's top); `edge`
 * goes in the page after the scroll view, under anything that floats; and the
 * scroll view takes `onScroll`, which calls the one passed in too.
 */
export function useLargeTitle(title: string, onScroll?: ScrollHandler): {
	header: ReactElement | null;
	edge: ReactElement | null;
	onScroll: ScrollHandler | undefined;
	topInset: number;
} {
	const android = Platform.OS === "android";
	const navigation = useNavigation();
	const headerHeight = useHeaderHeight();
	const topInset = android ? headerHeight : 0;
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
	const header = useMemo(() => (android ? <View style={{ height: topInset }} /> : null), [android, topInset]);
	const edge = useMemo(() => (android ? <Frost from="top" stops={frostStops(topInset)} style={[styles.frost, { height: topInset + FROST_TAIL }]} /> : null), [android, topInset]);
	return { header, edge, onScroll: android ? handle : onScroll, topInset };
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
});
