import { useId, type ReactElement } from "react";
import { Platform, StyleSheet, View, type StyleProp, type ViewStyle } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import Svg, { Defs, LinearGradient, Rect, Stop } from "react-native-svg";
import { useTheme } from "./ThemeProvider";

/**
 * Android's answer to iOS 26's scroll edge: the page's own colour laid over
 * the content under a bar, near-solid at the screen's edge and thinning
 * towards the bar's inner edge, where it lets a trace of the content through,
 * then dissolving into the page over a short tail. Content passing under a bar
 * reads as frosted out rather than cut off at a hard line.
 *
 * Without a blur to soften what shows through, the bar keeps most of its
 * cover: just enough of the rows comes through to say they are under it.
 */

/** One step of the gradient: where it sits (0 at the screen edge) and how much of the page colour it lays down. */
export type FrostStop = readonly [offset: number, opacity: number];

/** How far the frost runs past the bar into the page. */
export const FROST_TAIL = 32;

/**
 * The frost for a bar `barHeight` tall: solid at the screen's edge, a trace of
 * the content through the bar, then an eased fade over the tail, with enough
 * stops that the fade shows no band.
 */
export function frostStops(barHeight: number, tail = FROST_TAIL): FrostStop[] {
	const bar = barHeight / (barHeight + tail);
	const fade = (at: number) => bar + (1 - bar) * at;
	return [
		[0, 0.98],
		[bar * 0.6, 0.96],
		[bar, 0.93],
		[fade(0.2), 0.72],
		[fade(0.45), 0.4],
		[fade(0.7), 0.15],
		[fade(0.88), 0.04],
		[1, 0],
	];
}

/**
 * The frost itself, filling `style`'s box. `from` is the screen edge it is
 * solid at; the gradient runs away from it.
 */
export function Frost({ stops, from, style }: { stops: readonly FrostStop[]; from: "top" | "bottom"; style?: StyleProp<ViewStyle> }) {
	const color = useTheme().bgBase;
	const id = `frost${useId().replace(/:/g, "")}`;
	return (
		<View pointerEvents="none" style={style}>
			<Svg width="100%" height="100%" preserveAspectRatio="none">
				<Defs>
					<LinearGradient id={id} x1="0" y1={from === "top" ? "0" : "1"} x2="0" y2={from === "top" ? "1" : "0"}>
						{stops.map(([offset, opacity]) => <Stop key={offset} offset={offset} stopColor={color} stopOpacity={opacity} />)}
					</LinearGradient>
				</Defs>
				<Rect x="0" y="0" width="100%" height="100%" fill={`url(#${id})`} />
			</Svg>
		</View>
	);
}

/**
 * Under an opaque bar, the frost's tail alone: content scrolling up dissolves
 * into the bar over this short seam rather than meeting a hard edge, and the
 * seam is too short to reach a page's first row at rest.
 */
export const BAR_SEAM = 16;
export const SEAM_STOPS: readonly FrostStop[] = [[0, 0.9], [0.3, 0.55], [0.6, 0.2], [0.85, 0.05], [1, 0]];

/** Material 3's navigation bar, before the system navigation inset it pads below its items. */
export const NAVIGATION_BAR_HEIGHT = 80;

/**
 * A tab page's footing on Android, where the tab bar is clear and the page
 * runs on under it: how far the page's content has to stay clear of the bar,
 * and the frost that is the bar's backdrop. The page lays the frost over its
 * content, last, so the rows dissolve into the bar as they pass under it.
 *
 * iOS's tab bar is the system's glass, which insets the page itself, so there
 * this is nothing.
 */
export function useTabBarFrost(): { inset: number; frost: ReactElement | null } {
	const insets = useSafeAreaInsets();
	if (Platform.OS !== "android") return { inset: 0, frost: null };
	const inset = NAVIGATION_BAR_HEIGHT + insets.bottom;
	return { inset, frost: <Frost from="bottom" stops={frostStops(inset)} style={[styles.footer, { height: inset + FROST_TAIL }]} /> };
}

const styles = StyleSheet.create({
	footer: { position: "absolute", left: 0, right: 0, bottom: 0 },
});
