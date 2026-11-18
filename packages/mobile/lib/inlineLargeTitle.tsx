import { useNavigation } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useLayoutEffect, useMemo, useRef, type ReactElement, type ReactNode } from "react";
import { Platform, Pressable, StyleSheet, Text, View, type NativeScrollEvent, type NativeSyntheticEvent } from "react-native";
import Animated, { useAnimatedStyle, useSharedValue, withTiming, type SharedValue } from "react-native-reanimated";
import { AppSymbol } from "./AppSymbol";
import { useLargeTitle } from "./largeTitle";
import { usePressFeedback } from "./pressFeedback";
import { SYSTEM_GLASS_HEADER } from "./stackScreenOptions";
import type { AppSymbolName } from "./symbols";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { gutter, iconSize, radius, space, touchTarget } from "./tokens";

type ScrollHandler = (event: NativeSyntheticEvent<NativeScrollEvent>) => void;

/** How far the title row scrolls under the bar before the bar shows the title instead. */
export const INLINE_TITLE_HANDOFF = 32;
/** The bar's title fades in and out this fast, as the system's collapsed title does. */
const FADE_MS = 160;
/** The action's disc, the size of a project row's round button. */
const ACTION_SIZE = 36;

/**
 * Whether the page's own title has scrolled far enough under the bar for the
 * bar to name the page. `restY` is the list's offset at rest: under iOS 26's
 * clear bar the automatic insets leave it at minus the bar's height; under an
 * opaque bar it rests at zero.
 */
export function inlineTitleCollapsed(offsetY: number, restY: number): boolean {
	return offsetY - restY > INLINE_TITLE_HANDOFF;
}

/**
 * A board tab's large title with an action at the end of its line.
 *
 * iOS's large title has no room for a button on its own line: UIKit offers a
 * view under it, not beside it, and the bar's items sit on the row above. So
 * on iOS the page draws the title itself, in the large title's face and size,
 * as the first row of its list with the action at its end, and turns the
 * system's large title off for this page only. The bar keeps its own items as
 * they were and shows the title, small and centred, once the row has scrolled
 * under it, as the system's collapsed title would.
 *
 * Android keeps `useLargeTitle` as it is (its title is the top app bar's own),
 * and its create action stays the page's floating button, so `action` is
 * ignored there.
 *
 * The return value is `useLargeTitle`'s: `header` goes first in the page's
 * scroll view (on iOS it is the title row), and the scroll view takes
 * `onScroll`, which calls the one passed in too.
 */
export function useInlineLargeTitle(title: string, { action, onScroll }: { action?: ReactNode; onScroll?: ScrollHandler } = {}): {
	header: ReactElement | null;
	edge: ReactElement | null;
	onScroll: ScrollHandler | undefined;
	topInset: number;
} {
	const largeTitle = useLargeTitle(title, onScroll);
	const ios = Platform.OS === "ios";
	const navigation = useNavigation();
	const headerHeight = useHeaderHeight();
	const restY = SYSTEM_GLASS_HEADER ? -headerHeight : 0;
	const collapsed = useSharedValue(0);
	const last = useRef(0);
	useLayoutEffect(() => {
		if (!ios) return;
		// Set once: the bar's title reads the shared value, so scrolling never
		// registers the header again.
		navigation.setOptions({
			headerLargeTitleEnabled: false,
			// The page still scrolls up under iOS 26's clear glass bar, as it did
			// under the large title; earlier versions keep the page-coloured bar.
			...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null),
			headerTitle: () => <BarTitle title={title} collapsed={collapsed} />,
		});
	}, [collapsed, ios, navigation, title]);
	const handle = useCallback<ScrollHandler>((event) => {
		const next = inlineTitleCollapsed(event.nativeEvent.contentOffset.y, restY) ? 1 : 0;
		if (next !== last.current) collapsed.value = next;
		last.current = next;
		onScroll?.(event);
	}, [collapsed, onScroll, restY]);
	const header = useMemo(() => <TitleRow title={title} action={action} />, [action, title]);
	if (!ios) return largeTitle;
	return { header, edge: null, onScroll: handle, topInset: 0 };
}

/** The page's large title and its action, on one line at the top of the list. */
function TitleRow({ title, action }: { title: string; action?: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.row}>
			<Text accessibilityRole="header" numberOfLines={1} style={styles.title}>{title}</Text>
			{action}
		</View>
	);
}

/**
 * The bar's title, hidden while the page's own title is in view. VoiceOver
 * reads the page's title, so this one stays out of the way.
 */
function BarTitle({ title, collapsed }: { title: string; collapsed: SharedValue<number> }) {
	const t = useTheme();
	const faded = useAnimatedStyle(() => ({ opacity: withTiming(collapsed.value, { duration: FADE_MS }) }));
	return (
		<Animated.Text accessibilityElementsHidden importantForAccessibility="no-hide-descendants" numberOfLines={1} style={[styles.bar, { color: t.textPrimary }, faded]}>
			{title}
		</Animated.Text>
	);
}

/** The title row's action: a round button, as the project rows' own. */
export function TitleActionButton({ icon, accessibilityLabel, onPress, testID }: { icon: AppSymbolName; accessibilityLabel: string; onPress: () => void; testID?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button", { tone: "neutral" });
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			hitSlop={(touchTarget - ACTION_SIZE) / 2}
			android_ripple={feedback.android_ripple}
			onPress={onPress}
			style={({ pressed }) => [styles.action, pressed && feedback.pressed]}
			testID={testID}
		>
			<AppSymbol name={icon} size={iconSize.md} weight="semibold" color={t.textPrimary} />
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	// The title starts where the system's large title does; the action is
	// inset a little more, so it sits in the column of the bar's own buttons
	// above it rather than past them.
	row: { flexDirection: "row", alignItems: "center", gap: space.md, paddingLeft: gutter, paddingRight: gutter + space.xs, paddingTop: space.xs, paddingBottom: space.sm },
	// The system's large title: its face, size and tracking, at the weight the
	// board tabs set it (`largeTitleScreenOptions`).
	title: { flex: 1, minWidth: 0, color: t.textPrimary, fontSize: 34, lineHeight: 41, letterSpacing: 0.4, fontWeight: "800" },
	action: { width: ACTION_SIZE, height: ACTION_SIZE, alignItems: "center", justifyContent: "center", borderRadius: radius.pill, overflow: "hidden", backgroundColor: t.bgElevated },
});

const styles = StyleSheet.create({
	// The bar's title as `stackScreenOptions` sets it on iOS.
	bar: { fontSize: 17, fontWeight: "800" },
});
