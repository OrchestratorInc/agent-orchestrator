import { useState } from "react";
import { Animated as RNAnimated, Pressable, StyleSheet, Text, View } from "react-native";
import Animated, { FadeOut, LinearTransition } from "react-native-reanimated";

import { Feather } from "../icons";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { LAYOUT_MS, ROW_ENTER_MS } from "../motion";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space, type } from "../tokens";
import { Spinning, useBreathing } from "../ui";
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
			layout={reduceMotion ? undefined : LinearTransition.duration(LAYOUT_MS)}
			style={styles.wrap}
			testID="session-startup"
		>
			<View style={styles.header}>
				{startup.failed
					? <Feather name="x" size={12} color={t.red} />
					: <Spinning enabled><Feather name="loader" size={12} color={t.textTertiary} /></Spinning>}
				<Text accessibilityRole={startup.failed ? "alert" : undefined} accessibilityLiveRegion="polite" style={[styles.headerLabel, startup.failed && { color: t.red }]}>{title}</Text>
			</View>
			{startup.steps.length ? <StartupSteps startup={startup} /> : null}
			{startup.failed ? (
				<View style={styles.failure}>
					{startup.error ? <Text selectable style={styles.failureCopy}>{startup.error}</Text> : null}
					<Text style={styles.failureCopy}>Your messages are saved here and will be sent if you retry.</Text>
					{onRetry ? (
						<Pressable
							accessibilityRole="button"
							accessibilityState={{ disabled: retrying }}
							disabled={retrying}
							onPress={() => { haptics.tap(); onRetry(); }}
							style={({ pressed }) => [styles.retry, pressed && { opacity: 0.75 }, retrying && { opacity: 0.45 }]}
						>
							<Text style={styles.retryLabel}>{retrying ? "Retrying…" : "Retry start"}</Text>
						</Pressable>
					) : null}
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
			return <Spinning enabled><Feather name="loader" size={12} color={t.textTertiary} /></Spinning>;
		case "done":
			return <Feather name="check" size={12} color={t.textTertiary} />;
		case "failed":
			return <Feather name="x" size={12} color={t.red} />;
		case "pending":
			return <Feather name="circle" size={12} color={t.textFaint} />;
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
	// the checklist reads as its own block under the brief.
	header: { minHeight: 34, flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderDefault, marginBottom: space.xxs },
	headerLabel: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, fontWeight: "500" },
	row: { minHeight: 28, flexDirection: "row", alignItems: "center", gap: space.xs },
	rowLabel: { flexShrink: 0, fontFamily: "Geist_400Regular", fontSize: type.footnote.fontSize },
	detail: { flex: 1, minWidth: 0, fontFamily: "GeistMono_400Regular", color: t.textFaint, fontSize: type.caption2.fontSize },
	spacer: { flex: 1 },
	elapsed: { flexShrink: 0, paddingLeft: space.sm, fontFamily: "GeistMono_400Regular", color: t.textFaint, fontSize: type.caption2.fontSize, fontVariant: ["tabular-nums"] },
	failure: { paddingLeft: 18, paddingTop: space.xs, gap: space.sm, alignItems: "flex-start" },
	failureCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	retry: { minHeight: 44, justifyContent: "center", borderRadius: 12, borderCurve: "continuous", paddingHorizontal: space.lg, borderWidth: 1, borderColor: t.borderDefault, backgroundColor: t.bgElevated },
	retryLabel: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.caption1.fontSize, fontWeight: "600" },
});
