import { Pressable, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { Feather, type FeatherIconName } from "../icons";
import { usePressFeedback } from "../pressFeedback";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { fontScaleCap, iconSize, radius, space, touchTarget, type } from "../tokens";
import { compactCount } from "./model";

/** A trigger's capsule; its hit area grows to the touch target around it. */
const CAPSULE = 32;
const HIT_SLOP = { top: (touchTarget - CAPSULE) / 2, bottom: (touchTarget - CAPSULE) / 2 };

/**
 * What the Changes list compares, as two triggers: the branch, then which of
 * its changes. Each opens its picker sheet. A trigger left out (Orchestrator
 * Cloud reads no other branch, an older daemon splits no scopes) leaves the
 * label alone, as before the pickers.
 */
export function CompareBar({ branch, changes, summary, onBranch, onChanges }: {
	branch?: { label: string; icon: FeatherIconName };
	/** Left out on All files, which lists a branch's tip rather than its changes. */
	changes?: { label: string; mono?: boolean; accessibilityLabel: string };
	summary?: { additions: number; deletions: number };
	/** Undefined leaves the branch as a label. */
	onBranch?: () => void;
	/** Undefined leaves the changes as a label. */
	onChanges?: () => void;
}) {
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.row}>
			<View style={styles.triggers}>
				{branch ? <Trigger icon={branch.icon} label={branch.label} mono accessibilityLabel={`Branch, ${branch.label}`} onPress={onBranch} /> : null}
				{/* Its labels are short, so the branch name is the one that gives way. */}
				{changes ? <Trigger label={changes.label} mono={changes.mono} accessibilityLabel={changes.accessibilityLabel} onPress={onChanges} keepWidth /> : null}
			</View>
			{summary ? <Counts additions={summary.additions} deletions={summary.deletions} /> : null}
		</View>
	);
}

/** The totals at a glance ("+2.4k"); the changes trigger reads them exactly to a screen reader. */
function Counts({ additions, deletions }: { additions: number; deletions: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Text accessible={false} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.counts}>
			{additions ? <Text style={{ color: t.green }}>+{compactCount(additions)}</Text> : null}
			{additions && deletions ? " " : null}
			{deletions ? <Text style={{ color: t.red }}>−{compactCount(deletions)}</Text> : null}
			{!additions && !deletions ? <Text style={{ color: t.textTertiary }}>±0</Text> : null}
		</Text>
	);
}

function Trigger({ icon, label, mono, accessibilityLabel, onPress, keepWidth }: { icon?: FeatherIconName; label: string; mono?: boolean; accessibilityLabel: string; onPress?: () => void; keepWidth?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button", { radius: radius.pill });
	const content = (
		<>
			{icon ? <Feather name={icon} size={iconSize.sm} color={t.textSecondary} /> : null}
			{/* A branch's distinctive part is usually its end (ao/<session>/<topic>), so it gives way in the middle. */}
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} ellipsizeMode={icon ? "middle" : "tail"} style={[styles.label, mono && styles.mono]}>{label}</Text>
			{onPress ? <AppSymbol name="chevronUpDown" size={iconSize.xs} color={t.textTertiary} /> : null}
		</>
	);
	if (!onPress) return <View accessible accessibilityLabel={accessibilityLabel} style={[styles.capsule, styles.still, keepWidth && styles.keep]}>{content}</View>;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			accessibilityHint="Opens a list to choose from"
			hitSlop={HIT_SLOP}
			onPress={onPress}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.capsule, keepWidth && styles.keep, pressed && feedback.pressed]}
		>
			{content}
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	row: { flexDirection: "row", alignItems: "center", gap: space.sm },
	triggers: { flex: 1, flexDirection: "row", alignItems: "center", gap: space.sm, minWidth: 0 },
	capsule: {
		flexShrink: 1,
		minWidth: 0,
		minHeight: CAPSULE,
		flexDirection: "row",
		alignItems: "center",
		gap: space.xs,
		paddingHorizontal: space.md,
		borderRadius: radius.pill,
		borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		overflow: "hidden",
	},
	// A label only: no fill, so it does not read as something to press.
	still: { backgroundColor: "transparent", paddingHorizontal: 0 },
	keep: { flexShrink: 0 },
	label: { flexShrink: 1, ...type.footnote, color: t.textPrimary },
	counts: { fontFamily: t.fontMono, fontSize: type.footnote.fontSize, color: t.textSecondary },
	mono: { fontFamily: t.fontMono },
});
