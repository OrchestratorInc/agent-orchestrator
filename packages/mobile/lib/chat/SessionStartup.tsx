import { useState } from "react";
import { Animated as RNAnimated, StyleSheet, Text, View } from "react-native";
import Animated, { FadeOut } from "react-native-reanimated";

import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { layoutTransition } from "../layoutMotion";
import { ROW_ENTER_MS } from "../motion";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { Spinner } from "../Spinner";
import { iconSize, space, touchTarget, type } from "../tokens";
import { Button, useBreathing } from "../ui";
import { useNow } from "../useNow";
import { useReducedMotion } from "../useReducedMotion";
import {
	BRANCH_NAMING_LABELS,
	formatStepDuration,
	stepElapsedMs,
	stepLabel,
	stepStatus,
	type SessionStartupView,
	type StepStatus,
} from "../session/sessionStartup";

/**
 * A starting session's setup checklist, in the slot its opening turn's
 * "Working" row will take. Mirrors the desktop's SessionStartup: the header
 * stays put while the checklist folds away on handoff, and a failed start keeps
 * its failed step, the reason and Retry.
 */
export function SessionStartupStatus({ startup, onRetry, retrying }: { startup: SessionStartupView; onRetry?(): void; retrying?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const title = startup.failed ? "Session setup failed" : "Setting up session";
	return (
		<Animated.View
			accessibilityLabel="Session setup"
			exiting={reduceMotion ? undefined : FadeOut.duration(ROW_ENTER_MS)}
			layout={reduceMotion ? undefined : layoutTransition()}
			style={styles.wrap}
			testID="session-startup"
		>
			<View style={styles.header}>
				{startup.failed
					? <AppSymbol name="close" size={iconSize.xs} color={t.red} />
					: <Spinner size={iconSize.xs} color={t.textTertiary} />}
				<Text accessibilityRole={startup.failed ? "alert" : undefined} accessibilityLiveRegion="polite" style={[styles.headerLabel, startup.failed && { color: t.red }]}>{title}</Text>
			</View>
			{startup.steps.length ? <StartupSteps startup={startup} /> : null}
			{startup.failed ? (
				<View style={styles.failure}>
					{startup.error ? <Text selectable style={styles.failureCopy}>{startup.error}</Text> : null}
					<Text style={styles.failureCopy}>Your messages are saved here and will be sent if you retry.</Text>
					{onRetry ? <Button title="Retry start" icon="refresh" variant="secondary" loading={retrying} onPress={onRetry} /> : null}
				</View>
			) : null}
		</Animated.View>
	);
}

function StartupSteps({ startup }: { startup: SessionStartupView }) {
	// Ticks only while this is mounted, which is only while a session starts.
	const now = useNow(1_000);
	return (
		<View accessibilityLabel="Setup steps" style={{ paddingTop: space.xxs }}>
			{startup.steps.map((step) => {
				const status = stepStatus(step, startup.failed);
				const elapsed = status === "failed" ? undefined : stepElapsedMs(step, now);
				return (
					<StepRow
						key={step.id}
						label={stepLabel(step.id, startup.agentName)}
						status={status}
						detail={step.id === "worktree" && status !== "pending" ? startup.branch : undefined}
						naming={step.id === "worktree" && startup.namingBranch}
						elapsed={elapsed === undefined ? undefined : formatStepDuration(elapsed, status === "running")}
					/>
				);
			})}
		</View>
	);
}

const STATUS_COPY: Record<StepStatus, string> = { pending: "Pending", running: "In progress", done: "Done", failed: "Failed" };

function StepRow({ label, status, detail, naming, elapsed }: { label: string; status: StepStatus; detail?: string; naming: boolean; elapsed?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const color = status === "pending" ? t.textFaint : status === "running" ? t.textPrimary : status === "failed" ? t.red : t.textTertiary;
	return (
		<View
			accessible
			accessibilityLabel={[label, STATUS_COPY[status], detail, elapsed].filter(Boolean).join(", ")}
			style={styles.row}
			testID={`session-startup-step-${status}`}
		>
			<StepIcon status={status} />
			<Text numberOfLines={1} style={[styles.rowLabel, { color }]}>{label}</Text>
			{naming ? <BranchNamingLabel /> : detail ? <Text numberOfLines={1} ellipsizeMode="middle" style={styles.detail}>{detail}</Text> : <View style={styles.spacer} />}
			{elapsed ? <Text style={styles.elapsed}>{elapsed}</Text> : null}
		</View>
	);
}

function StepIcon({ status }: { status: StepStatus }) {
	const t = useTheme();
	switch (status) {
		case "running":
			return <Spinner size={iconSize.xs} color={t.textTertiary} />;
		case "done":
			return <AppSymbol name="checkmark" size={iconSize.xs} color={t.textTertiary} />;
		case "failed":
			return <AppSymbol name="close" size={iconSize.xs} color={t.red} />;
		case "pending":
			return <AppSymbol name={{ ios: "circle", android: "radio_button_unchecked" }} size={iconSize.xs} color={t.textFaint} />;
	}
}

/** One of the desktop's branch-naming phrases, breathing while the daemon picks the name. */
function BranchNamingLabel() {
	const styles = useThemedStyles(makeStyles);
	const [label] = useState(() => BRANCH_NAMING_LABELS[Math.floor(Math.random() * BRANCH_NAMING_LABELS.length)]);
	const pulse = useBreathing(true);
	return <RNAnimated.Text numberOfLines={1} style={[styles.detail, pulse ? { opacity: pulse } : undefined]}>{label}…</RNAnimated.Text>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	wrap: { paddingBottom: space.sm },
	// Full width with a rule under it, like the desktop's live status header, so
	// the checklist reads as its own block under the brief. A touch target tall,
	// as is the turn's status row that takes its place, so the handoff holds still.
	header: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderDefault, marginBottom: space.xxs },
	headerLabel: { fontFamily: "DMSans_500Medium", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	row: { minHeight: 28, flexDirection: "row", alignItems: "center", gap: space.xs },
	rowLabel: { flexShrink: 0, fontFamily: "DMSans_400Regular", fontSize: type.footnote.fontSize },
	detail: { flex: 1, minWidth: 0, fontFamily: "GeistMono_400Regular", color: t.textFaint, fontSize: type.footnote.fontSize },
	spacer: { flex: 1 },
	elapsed: { flexShrink: 0, paddingLeft: space.sm, fontFamily: "GeistMono_400Regular", color: t.textFaint, fontSize: type.footnote.fontSize, fontVariant: ["tabular-nums"] },
	// Under the header's label, past its glyph and gap.
	failure: { paddingLeft: iconSize.xs + space.xs, paddingTop: space.xs, gap: space.sm, alignItems: "flex-start" },
	failureCopy: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
