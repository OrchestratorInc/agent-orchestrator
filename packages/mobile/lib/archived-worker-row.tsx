import { memo, useMemo } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import type { DashboardSession } from "./api";
import { AgentLogo } from "./AgentLogo";
import { AppSymbol } from "./AppSymbol";
import { sessionMachineIdentity } from "./hostedRows";
import { MachineIdentity } from "./MachineIdentity";
import { prLine, workerRowPresentation } from "./agentsView";
import { usePressFeedback } from "./pressFeedback";
import { toneColor } from "./prView";
import { statusVisual, type Theme } from "./theme";
import { rowDividerWidth } from "./divider";
import { Spinner } from "./Spinner";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { iconSize, radius, space, stateOpacity, touchTarget, type } from "./tokens";
import { workerActionSymbol } from "./worker-action-model";

/**
 * An archived worker, as desktop's archive strip shows one: read-only, with
 * Restore as its only action.
 *
 * Deliberately not a WorkerListRow. A live row carries a native context menu (a
 * SwiftUI or Compose host on each platform), a swipe container and its gesture
 * handlers, and the archive is most of a long-running project's sessions:
 * opening it mounted a few hundred of those at once, which stalled the board and
 * could crash it. There is nothing to open, pin, rename or archive here, so none
 * of that is built.
 */
export const ArchivedWorkerRow = memo(function ArchivedWorkerRow({
	session,
	projectName,
	nowBucket: _nowBucket,
	restoring,
	restoreDisabled,
	offline,
	restoreError,
	onRestore,
}: {
	session: DashboardSession;
	projectName?: string;
	/** The current minute, so a memoised row still updates its relative timestamp. */
	nowBucket: number;
	restoring: boolean;
	/** Another restore is in flight; desktop allows one at a time. */
	restoreDisabled: boolean;
	/** The session's machine is unreachable, so a restore could not be sent. */
	offline: boolean;
	restoreError?: string;
	onRestore(session: DashboardSession): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const row = workerRowPresentation(t, session, projectName);
	const visual = statusVisual(t, session.status);
	const prs = prLine(session);
	const details = prs?.text ?? row.branch ?? "";
	const machine = useMemo(() => sessionMachineIdentity(session), [session]);
	const feedback = usePressFeedback("icon");
	const disabled = restoring || restoreDisabled || offline;

	return (
		<View style={styles.shell}>
			<View accessible accessibilityLabel={`${row.title}. ${visual.label}. ${row.project}.`} style={styles.content}>
				<View style={styles.eyebrow}>
					<AgentLogo harness={session.harness} size={14} />
					<Text style={styles.project} numberOfLines={1}>
						{row.project}
					</Text>
					<Text
						style={[styles.trailing, { color: row.trailingKind === "status" ? visual.color : t.textTertiary }]}
						numberOfLines={1}
					>
						{row.trailing}
					</Text>
				</View>
				<Text style={styles.title} numberOfLines={1}>
					{row.title}
				</Text>
				{details || machine ? (
					<View style={styles.metadata}>
						{details ? (
							<Text style={[styles.details, prs?.tone && { color: toneColor(t, prs.tone) }]} numberOfLines={1}>
								{details}
							</Text>
						) : <View style={styles.metadataSpacer} />}
						{machine ? <MachineIdentity testID="archived-machine-identity" name={machine.name} appearance={machine.appearance} style={styles.machineIdentity} /> : null}
					</View>
				) : null}
				{restoreError ? <Text accessibilityRole="alert" style={styles.restoreError}>{restoreError}</Text> : null}
			</View>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={`Restore ${row.title}`}
				accessibilityHint={offline ? "Unavailable while this machine is offline." : undefined}
				accessibilityState={{ disabled, busy: restoring }}
				disabled={disabled}
				android_ripple={feedback.android_ripple}
				onPress={() => onRestore(session)}
				style={({ pressed }) => [styles.restore, pressed && feedback.pressed, disabled && !restoring && styles.restoreDisabled]}
			>
				{restoring
					? <Spinner size={iconSize.lg} color={t.textPrimary} />
					: <AppSymbol name={workerActionSymbol("restore")} size={iconSize.lg} color={t.textSecondary} />}
			</Pressable>
		</View>
	);
});

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// The same box as a live row, so the board keeps one rhythm across Archive.
		shell: {
			minHeight: 76,
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingLeft: space.lg,
			paddingRight: space.sm,
			paddingVertical: space.sm,
			borderBottomWidth: rowDividerWidth,
			borderBottomColor: t.borderSubtle,
			backgroundColor: t.bgBase,
		},
		content: { flex: 1, minWidth: 0, gap: space.hair },
		eyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: 17 },
		project: { fontFamily: "Geist_500Medium", flex: 1, color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "500" },
		trailing: { fontFamily: "Geist_500Medium", flexShrink: 0, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "500", fontVariant: ["tabular-nums"] },
		title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600", letterSpacing: -0.15 },
		metadata: { minWidth: 0, flexDirection: "row", alignItems: "center", gap: space.sm },
		metadataSpacer: { flex: 1 },
		details: { flex: 1, minWidth: 0, color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontFamily: t.fontMono },
		machineIdentity: { maxWidth: "52%", marginLeft: "auto" },
		restoreError: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		// A quiet icon control, as on desktop's archive card, at the platform's
		// touch target.
		restore: { width: touchTarget, height: touchTarget, borderRadius: radius.pill, alignItems: "center", justifyContent: "center" },
		restoreDisabled: { opacity: stateOpacity.disabled },
	});
