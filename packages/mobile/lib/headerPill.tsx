import type { NativeStackNavigationOptions } from "expo-router";
import type { ReactNode } from "react";
import { Platform, Pressable, StyleSheet, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import type { AppSymbolProps } from "./AppSymbol.types";
import { CupertinoChevron } from "./cupertinoChevron";
import { rowDividerWidth } from "./divider";
import { usePressFeedback } from "./pressFeedback";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, space } from "./tokens";

/**
 * Header controls that float over a page scrolling under a clear bar, each on
 * an opaque pill of the composer's own surface (see `ComposerGlass` on
 * Android): the bar has no strip of its own, so the page runs on up to the
 * screen's edge, under the frost, and only the pills hold it back. The frost
 * behind them can then stay light, since nothing in the bar sits on it bare.
 */

/**
 * The bar's own back button taken out on Android, for a page whose title pill
 * leads with one (`HeaderPill`'s `onBack`). The leading slot holds an empty
 * view rather than nothing: native-stack keeps the system's "Navigate up"
 * arrow beside a custom title whenever that slot is empty, whatever
 * `headerBackVisible` says. iOS keeps the system's own back button.
 */
export function pillBackOptions(): NativeStackNavigationOptions {
	return Platform.OS === "android" ? { headerBackVisible: false, headerLeft: () => <View /> } : {};
}

/** A pill's height: a round button's diameter, and a capsule's. */
export const HEADER_PILL_SIZE = 44;

/**
 * A capsule holding whatever the bar sets on it, such as the title. With
 * `onBack` it leads with the back chevron, so the way back and the page it
 * leaves read as one control, as the pill's own touch target.
 */
export function HeaderPill({ children, onBack }: { children: ReactNode; onBack?(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("icon");
	return (
		<View style={[styles.surface, styles.capsule, onBack ? styles.capsuleWithBack : null]}>
			{onBack ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="Back"
					android_ripple={feedback.android_ripple}
					hitSlop={space.xxs}
					onPress={onBack}
					testID="header-back"
					style={({ pressed }) => [styles.back, pressed && feedback.pressed]}
				>
					<CupertinoChevron color={t.textPrimary} size={iconSize.lg} />
				</Pressable>
			) : null}
			{children}
		</View>
	);
}

/** A round button on its own pill: the bar's back button, or one action. */
export function HeaderPillButton({
	label,
	onPress,
	icon,
	children,
	testID,
}: {
	label: string;
	onPress(): void;
	icon?: AppSymbolProps["name"];
	/** Drawn in place of `icon`, for a glyph `AppSymbol` does not have. */
	children?: ReactNode;
	testID?: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("icon");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={label}
			android_ripple={feedback.android_ripple}
			// The pill is smaller than a touch target; the slop makes up the rest.
			hitSlop={space.xxs}
			onPress={onPress}
			testID={testID}
			style={({ pressed }) => [styles.surface, styles.round, pressed && feedback.pressed]}
		>
			{children ?? (icon ? <AppSymbol name={icon} size={iconSize.xl} color={t.textPrimary} /> : null)}
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	surface: {
		height: HEADER_PILL_SIZE,
		borderRadius: radius.pill,
		borderCurve: "continuous",
		overflow: "hidden",
		backgroundColor: t.bgSurface,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
		alignItems: "center",
		justifyContent: "center",
	},
	round: { width: HEADER_PILL_SIZE },
	// Hugs what it holds, from the bar's leading edge, rather than stretching
	// over the slot the bar gives it and centring its content in the middle; the
	// margin keeps it off the action pill when a long title fills the slot.
	capsule: { flexDirection: "row", alignSelf: "flex-start", justifyContent: "flex-start", maxWidth: "100%", marginEnd: space.sm, paddingHorizontal: space.md },
	// The chevron's own round target sits in the capsule's leading end.
	capsuleWithBack: { paddingStart: space.xxs, gap: space.xxs },
	back: { width: HEADER_PILL_SIZE - space.xs, height: HEADER_PILL_SIZE - space.xs, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
});
