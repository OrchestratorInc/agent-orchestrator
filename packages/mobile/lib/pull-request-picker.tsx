import { useNavigation } from "expo-router";
import { useLayoutEffect } from "react";
import { Platform, Pressable, ScrollView, StyleSheet, Text, View, type DimensionValue } from "react-native";
import Animated, { FadeIn, FadeOut } from "react-native-reanimated";
import Svg, { Defs, Ellipse, RadialGradient, Stop } from "react-native-svg";
import type { DashboardPR } from "./api";
import { haptics } from "./haptics";
import { Feather } from "./icons";
import { toneColor } from "./prView";
import type { PullRequestRow } from "./sessionPullRequests";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { duration, iconSize, radius, space, type } from "./tokens";
import { markTouchInside } from "./touchOutside";

/*
 * Every pull request a session raised, floating over its composer when the
 * PR chip is tapped: the permission picker's look (#8944) on main's own
 * overlay pattern (EffortSliderOverlay). Each row is the PR's lifecycle icon
 * in its colour, its number and title, and the one status that decides what
 * to do next. Nothing is ticked: picking a row opens that PR's review.
 *
 * When #8944 lands, this moves onto its ComposerOverlay and drops the veil
 * copied here.
 */

export const PR_PICKER_ROW_HEIGHT = 58;
// Wide enough for a title beside its number; the overlay caps it to the composer.
const PICKER_WIDTH = 360;
const ICON_COLUMN = 24;
// Past five, the list scrolls inside five and a half rows: the half row says there is more.
const VISIBLE_ROWS = 5;
// How far above the list the veil reaches: its arch is solid only over its lower half or so.
const VEIL_RISE = 220;
const VEIL_BLEED = 24;

/**
 * The list floating a small gap above `bottom` in the composer: nothing under
 * it moves when it opens, and it covers the chips row. While it is up the iOS
 * back swipe is off, as for the effort slider; the header's back button still
 * leaves, and a tap outside closes it.
 */
export function PullRequestPickerOverlay({ bottom, rows, onChoose }: {
	bottom: DimensionValue;
	rows: readonly PullRequestRow[];
	onChoose(pr: DashboardPR): void;
}) {
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	useLayoutEffect(() => {
		if (Platform.OS !== "ios") return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [navigation]);
	return (
		<Animated.View
			entering={FadeIn.duration(duration.base)}
			exiting={FadeOut.duration(duration.fast)}
			onTouchStart={markTouchInside}
			style={[styles.overlay, { bottom }]}
		>
			<Veil />
			<PullRequestPicker rows={rows} onChoose={onChoose} />
		</Animated.View>
	);
}

export function PullRequestPicker({ rows, onChoose }: { rows: readonly PullRequestRow[]; onChoose(pr: DashboardPR): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const scrolls = rows.length > VISIBLE_ROWS;
	return (
		<ScrollView
			testID="pull-request-picker"
			accessibilityLabel="Pull requests"
			scrollEnabled={scrolls}
			showsVerticalScrollIndicator={scrolls}
			keyboardShouldPersistTaps="handled"
			style={[styles.list, { maxHeight: PR_PICKER_ROW_HEIGHT * (VISIBLE_ROWS + 0.5) }]}
			contentContainerStyle={styles.listContent}
		>
			{rows.map((row) => (
				<Pressable
					key={row.number}
					testID={`pull-request-${row.number}`}
					accessibilityRole="button"
					accessibilityLabel={row.accessibilityLabel}
					accessibilityHint="Opens the pull request review"
					onPress={() => {
						haptics.select();
						onChoose(row.pr);
					}}
					style={({ pressed }) => [styles.row, pressed && styles.rowPressed]}
				>
					<View style={styles.icon}>
						<Feather name={row.glyph} size={iconSize.md} strokeWidth={2} color={toneColor(t, row.glyphTone)} />
					</View>
					<View style={styles.text}>
						<Text style={styles.title} numberOfLines={1}>
							<Text style={styles.number}>{`#${row.number} `}</Text>
							{row.title}
						</Text>
						<Text style={[styles.status, { color: toneColor(t, row.status.tone) }]} numberOfLines={1}>{row.status.text}</Text>
					</View>
				</Pressable>
			))}
		</ScrollView>
	);
}

/** #8944's arch veil, in the page's colour, so the list reads over a busy conversation. */
function Veil() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View pointerEvents="none" style={styles.veil}>
			<Svg width="100%" height="100%" preserveAspectRatio="none" viewBox="0 0 100 100">
				<Defs>
					<RadialGradient id="pull-request-picker-veil" cx="50%" cy="50%" r="50%" gradientUnits="objectBoundingBox">
						<Stop offset="0" stopColor={t.bgBase} stopOpacity={1} />
						<Stop offset="0.58" stopColor={t.bgBase} stopOpacity={1} />
						<Stop offset="0.72" stopColor={t.bgBase} stopOpacity={0.88} />
						<Stop offset="0.87" stopColor={t.bgBase} stopOpacity={0.42} />
						<Stop offset="1" stopColor={t.bgBase} stopOpacity={0} />
					</RadialGradient>
				</Defs>
				<Ellipse cx="50" cy="100" rx="72" ry="100" fill="url(#pull-request-picker-veil)" />
			</Svg>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		overlay: {
			position: "absolute",
			left: 0,
			right: 0,
			zIndex: 1,
			alignItems: "center",
			// The gap above the composer; a margin does not move an absolutely placed view off its `bottom`.
			transform: [{ translateY: -space.sm }],
		},
		veil: { position: "absolute", top: -VEIL_RISE, bottom: -space.sm, left: -VEIL_BLEED, right: -VEIL_BLEED },
		list: { width: PICKER_WIDTH, maxWidth: "100%", flexGrow: 0 },
		listContent: { gap: space.hair },
		row: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.md,
			height: PR_PICKER_ROW_HEIGHT,
			paddingHorizontal: space.sm,
			borderRadius: radius.lg,
			borderCurve: "continuous",
		},
		rowPressed: { backgroundColor: t.bgSubtle },
		icon: { width: ICON_COLUMN, alignItems: "center", justifyContent: "center" },
		text: { flex: 1, minWidth: 0, gap: 1, justifyContent: "center" },
		title: { ...type.subheadline, fontFamily: type.headline.fontFamily, color: t.textPrimary },
		number: { color: t.textSecondary },
		status: { ...type.footnote },
	});
