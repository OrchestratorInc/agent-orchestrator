import { useNavigation } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useLayoutEffect, useMemo, useRef, type ReactElement } from "react";
import { Platform, StyleSheet, Text, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { Frost, FROST_TAIL, frostStops } from "./frost";
import { useTheme } from "./ThemeProvider";
import { font, gutter, space, type } from "./tokens";

type ScrollHandler = (event: NativeSyntheticEvent<NativeScrollEvent>) => void;

/**
 * How far the list scrolls before its large title has gone under the bar:
 * past the title's cap height, so the bar takes the name over just as the big
 * one stops being readable.
 */
export const LARGE_TITLE_COLLAPSE_AT = 28;

/** The bar's own title fades in this fast once the big one is under it. */
const FADE_MS = 150;
/** The frost behind the bar comes up a touch slower, as a surface settling rather than a label. */
const FROST_MS = 220;

export function largeTitleCollapsed(y: number): boolean {
	return y > LARGE_TITLE_COLLAPSE_AT;
}

/** Whether anything has moved under the bar yet: the frost comes up on the first move, ahead of the rows reaching it. */
export function scrolledUnderBar(y: number): boolean {
	return y > 0;
}

/**
 * A board tab's large title on Android, as Material 3's large top app bar
 * draws one: the name set big at the head of the list, scrolling with it, and
 * taken over by the bar's own title once the list has carried it out of
 * sight. iOS's large-title bar does this natively, so there the hook adds
 * nothing and hands `onScroll` back as given.
 *
 * The bar is clear, as iOS 26's is, and the list runs on under it: at rest the
 * board is one canvas with the bar's items over it, and on the first move the
 * bar's frost (see `frost`) comes up behind them, so rows passing under the
 * bar frost out rather than meet a hard edge.
 *
 * `header` goes first in the page's scroll view, and holds it clear of the bar
 * (`topInset` says how far, for anything else pinned to the page's top); `edge`
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
	const scrolled = useSharedValue(0);
	const last = useRef({ collapsed: 0, scrolled: 0 });
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
		const y = event.nativeEvent.contentOffset.y;
		const next = { collapsed: largeTitleCollapsed(y) ? 1 : 0, scrolled: scrolledUnderBar(y) ? 1 : 0 };
		if (next.collapsed !== last.current.collapsed) collapsed.value = next.collapsed;
		if (next.scrolled !== last.current.scrolled) scrolled.value = next.scrolled;
		last.current = next;
		onScroll?.(event);
	}, [collapsed, scrolled, onScroll]);
	const header = useMemo(() => (android ? <LargeTitle title={title} topInset={topInset} /> : null), [android, title, topInset]);
	const edge = useMemo(() => (android ? <HeaderFrost height={topInset} scrolled={scrolled} /> : null), [android, scrolled, topInset]);
	return { header, edge, onScroll: android ? handle : onScroll, topInset };
}

function LargeTitle({ title, topInset }: { title: string; topInset: number }) {
	const t = useTheme();
	return (
		<Text accessibilityRole="header" numberOfLines={1} style={[styles.large, { color: t.textPrimary, paddingTop: topInset + space.xxs }]}>
			{title}
		</Text>
	);
}

function BarTitle({ title, collapsed }: { title: string; collapsed: SharedValue<number> }) {
	const t = useTheme();
	const shown = useAnimatedStyle(() => ({
		opacity: withTiming(collapsed.value, { duration: FADE_MS }),
		transform: [{ translateY: withTiming((1 - collapsed.value) * space.xs, { duration: FADE_MS }) }],
	}));
	return (
		// The large title already names the page to TalkBack.
		<Animated.Text numberOfLines={1} importantForAccessibility="no" style={[styles.bar, { color: t.textPrimary }, shown]}>
			{title}
		</Animated.Text>
	);
}

/** The frost behind the clear bar, up only while something has scrolled under it. */
function HeaderFrost({ height, scrolled }: { height: number; scrolled: SharedValue<number> }) {
	const shown = useAnimatedStyle(() => ({ opacity: withTiming(scrolled.value, { duration: FROST_MS }) }));
	return (
		<Animated.View pointerEvents="none" style={[styles.frost, { height: height + FROST_TAIL }, shown]}>
			<Frost from="top" stops={frostStops(height)} style={styles.fill} />
		</Animated.View>
	);
}

const styles = StyleSheet.create({
	large: { ...type.largeTitle, paddingHorizontal: gutter, paddingTop: space.xxs, paddingBottom: space.sm },
	bar: { fontFamily: font.semibold, fontSize: 20 },
	frost: { position: "absolute", top: 0, start: 0, end: 0 },
	fill: { flex: 1 },
});
