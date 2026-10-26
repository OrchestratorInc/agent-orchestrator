import { useState, type ReactNode } from "react";
import { Pressable, StyleSheet, Text, View, type StyleProp, type ViewStyle } from "react-native";
import { OrchestratorIcon } from "./orchestrator-icon";
import {
	orchestratorButtonCopy,
	orchestratorStatus,
	projectBlockerLine,
	projectCardSummary,
	type ProjectPageStats,
	type OrchestratorProjectRow,
} from "./orchestratorView";
import type { Theme } from "./theme";
import type { HostedProjectRow } from "./hostedRows";
import type { HostSnapshot } from "./otherHosts";
import { rowDividerWidth } from "./divider";
import { usePressFeedback } from "./pressFeedback";
import { Spinner } from "./Spinner";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, radius, space, touchTarget, type } from "./tokens";
import { Dot } from "./ui";
import { MachineIdentity } from "./MachineIdentity";
import { machineAppearance } from "./machineAppearance";

/** The pill's resting height; a larger text size grows it. */
const PILL_HEIGHT = 32;
/** Reaches the platform's touch target around a pill drawn smaller than it. */
const PILL_HIT_SLOP = { top: (touchTarget - PILL_HEIGHT) / 2, bottom: (touchTarget - PILL_HEIGHT) / 2, left: space.sm, right: space.sm };

/**
 * One project on the Projects page: who it is, how its fleet is doing, and the
 * way into its orchestrator.
 *
 * A flat row with a divider, like a worker row, rather than a card: the two
 * lists read as one app. The body opens the project page; a trailing slot
 * carries a compact pill that opens, starts or resumes the orchestrator. The
 * pill is the only filled control on the row, so it outranks the counts without
 * dominating the name.
 */
export function ProjectCard({
	row,
	busy,
	onOpenProject,
	onOrchestrator,
}: {
	row: HostedProjectRow;
	busy: boolean;
	onOpenProject: (row: OrchestratorProjectRow) => void;
	onOrchestrator: (row: OrchestratorProjectRow) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const t = useTheme();
	const status = orchestratorStatus(t, row.link);
	const summary = projectCardSummary(row);
	const blocker = projectBlockerLine(row);

	return (
		<TrailingActionRow
			style={styles.row}
			bodyStyle={styles.body}
			accessibilityLabel={`${row.project.name}, ${row.project.hostName} machine, ${status.label}, ${summary.workers}`}
			accessibilityHint="Opens the project"
			onPress={() => onOpenProject(row)}
			trailing={<OrchestratorPill row={row} busy={busy} onPress={onOrchestrator} />}
		>
			<Text style={styles.project} numberOfLines={1}>
				{row.project.name}
			</Text>

			<View style={styles.summaryRow}>
				<Dot color={status.color} size={6} breathing={status.breathing} />
				<Text style={[styles.summaryStrong, styles.statusLabel, { color: status.color }]} numberOfLines={1}>
					{status.label}
				</Text>
			</View>

			{blocker ? (
				// Two texts so the ellipsis lands on the worker name, not the age.
				<View style={styles.blockerRow}>
					<Text style={styles.blockerWorker} numberOfLines={1}>
						{blocker.worker}
					</Text>
					<Text style={styles.blocker} numberOfLines={1}>
						{` · ${blocker.reason}`}
						{blocker.age ? ` · ${blocker.age}` : ""}
					</Text>
				</View>
			) : null}

			{/* The worker row's metadata line: the count, and the machine at its end. */}
			<View style={styles.footerRow}>
				<Text style={styles.footerLabel} numberOfLines={1}>
					{summary.workers}
				</Text>
				<MachineIdentity
					name={row.project.hostName}
					appearance={machineAppearance({ id: row.project.hostId, name: row.project.hostName, appearance: row.project.hostAppearance })}
					style={styles.machineIdentity}
				/>
			</View>
		</TrailingActionRow>
	);
}

/**
 * A list row whose body is one control and whose trailing slot holds another.
 * The two sit side by side rather than one inside the other, so each stays its
 * own accessible control, and the slot takes only the width its control needs:
 * at a large text size the body narrows instead of running under it.
 *
 * iOS highlights the whole row while the body is held, as a table cell
 * highlights under its accessory; Android ripples where the finger is.
 */
function TrailingActionRow({
	style,
	bodyStyle,
	accessibilityLabel,
	accessibilityHint,
	disabled,
	onPress,
	trailing,
	children,
}: {
	style: StyleProp<ViewStyle>;
	bodyStyle: StyleProp<ViewStyle>;
	accessibilityLabel: string;
	accessibilityHint?: string;
	disabled?: boolean;
	onPress: () => void;
	trailing: ReactNode;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [pressed, setPressed] = useState(false);
	return (
		<View style={[style, pressed && feedback.pressed]}>
			<View style={styles.trailingRow}>
				<Pressable
					accessibilityRole="button"
					accessibilityLabel={accessibilityLabel}
					accessibilityHint={accessibilityHint}
					accessibilityState={disabled ? { disabled } : undefined}
					disabled={disabled}
					android_ripple={feedback.android_ripple}
					onPress={onPress}
					// Only iOS draws a pressed style; Android's ripple needs no re-render.
					onPressIn={feedback.pressed ? () => setPressed(true) : undefined}
					onPressOut={feedback.pressed ? () => setPressed(false) : undefined}
					style={[styles.trailingBody, bodyStyle]}
				>
					{children}
				</Pressable>
				<View style={styles.trailingSlot}>{trailing}</View>
			</View>
			<View style={styles.divider} />
		</View>
	);
}

/** The compact orchestrator action on a project row. */
function OrchestratorPill({
	row,
	busy,
	onPress,
	openLabel,
}: {
	row: OrchestratorProjectRow;
	busy: boolean;
	onPress: (row: OrchestratorProjectRow) => void;
	/** Overrides the running label where "Orchestrator" is already on screen. */
	openLabel?: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const copy = orchestratorButtonCopy(row, busy);
	// Filled when it opens something live; outlined when it would start one, so a
	// stopped orchestrator does not look ready. Tonal rather than solid, as T3
	// Code draws a row's secondary action: the row's name stays the loudest thing.
	const solid = copy.running;
	const ink = solid ? t.textPrimary : t.textSecondary;
	const feedback = usePressFeedback("button", { tone: "neutral" });
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${copy.label}, ${row.project.name}`}
			accessibilityState={{ busy, disabled: busy }}
			disabled={busy}
			hitSlop={PILL_HIT_SLOP}
			android_ripple={feedback.android_ripple}
			onPress={() => onPress(row)}
			style={({ pressed }) => [styles.pill, solid ? styles.pillSolid : styles.pillOutline, pressed && feedback.pressed]}
		>
			{busy ? <Spinner size={iconSize.sm} color={ink} /> : <OrchestratorIcon size={iconSize.sm} color={ink} />}
			<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.pillLabel, { color: ink }]} numberOfLines={1}>
				{copy.running && openLabel ? openLabel : copy.short}
			</Text>
		</Pressable>
	);
}

/** The machine owning a project route, shown as quiet context in its header. */
export function ProjectMachineIndicator({
	hostId,
	hosts,
}: {
	hostId: string | undefined;
	hosts: readonly Pick<HostSnapshot, "hostId" | "name" | "appearance">[];
}) {
	const styles = useThemedStyles(makeStyles);
	const host = hosts.find((candidate) => candidate.hostId === hostId);
	if (!host) return null;
	return <MachineIdentity name={host.name} appearance={host.appearance} style={styles.projectMachineIdentity} testID="project-machine-identity" />;
}

/**
 * The top of a project page: a strip of counts for the overview, then the
 * orchestrator as one line — its state and what it reports — with the pill that
 * opens it. Flat like the rows beneath it, so the page reads as one list.
 */
export function ProjectPageHeader({
	row,
	stats,
	busy,
	onPress,
}: {
	row: OrchestratorProjectRow;
	stats: ProjectPageStats;
	busy: boolean;
	onPress: (row: OrchestratorProjectRow) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const status = orchestratorStatus(t, row.link);
	const tiles: { label: string; value: number; color?: string }[] = [
		{ label: "Workers", value: stats.workers },
		{ label: "Needs you", value: stats.needsYou, color: stats.needsYou ? t.amber : undefined },
		{ label: "Ready", value: stats.ready },
		{ label: "Archived", value: stats.archived },
	];
	return (
		<View>
			<View style={styles.stats} accessible accessibilityLabel={tiles.map((tile) => `${tile.value} ${tile.label}`).join(", ")}>
				{tiles.map((tile) => (
					<View key={tile.label} style={styles.stat}>
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.statValue, tile.color ? { color: tile.color } : null]}>
							{tile.value}
						</Text>
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.statLabel} numberOfLines={1}>
							{tile.label}
						</Text>
					</View>
				))}
			</View>

			{/* The whole row opens the orchestrator, and so does the pill beside it. */}
			<TrailingActionRow
				style={styles.orchestratorRow}
				bodyStyle={styles.orchestratorBody}
				accessibilityLabel={`${orchestratorButtonCopy(row, busy).label}, ${status.label}, ${row.detail}`}
				disabled={busy}
				onPress={() => onPress(row)}
				trailing={<OrchestratorPill row={row} busy={busy} onPress={onPress} openLabel="Open" />}
			>
				{/* The worker row's three lines — eyebrow, title, detail — so the
				    orchestrator reads as one of the rows below. */}
				<View style={styles.orchestratorEyebrow}>
					<OrchestratorIcon size={14} color={t.textSecondary} />
					<Text style={styles.orchestratorEyebrowText} numberOfLines={1}>
						{row.link?.harness || "Orchestrator"}
					</Text>
				</View>
				<Text style={styles.orchestratorTitle} numberOfLines={1}>
					Orchestrator
				</Text>
				<View style={styles.summaryRow}>
					<Dot color={status.color} size={6} breathing={status.breathing} />
					<Text style={[styles.summaryStrong, styles.statusLabel, { color: status.color }]} numberOfLines={1}>
						{status.label}
					</Text>
					<Text style={styles.summary} numberOfLines={1}>
						{` · ${row.detail}`}
					</Text>
				</View>
			</TrailingActionRow>
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		// A flat row like a worker row: no card, just the inset divider.
		row: { backgroundColor: t.bgBase },
		body: { paddingLeft: space.lg, paddingVertical: space.sm + space.hair, gap: space.hair },
		trailingRow: { flexDirection: "row", alignItems: "stretch" },
		trailingBody: { flex: 1, minWidth: 0, justifyContent: "center" },
		// Centred on the row whatever its height, and never narrower than its pill.
		trailingSlot: { flexShrink: 0, justifyContent: "center", paddingLeft: space.md, paddingRight: space.lg },
		// Inset to the text, as a worker row's is.
		divider: { marginLeft: space.lg, height: rowDividerWidth, backgroundColor: t.borderDefault },

		project: { fontFamily: type.headline.fontFamily, color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: type.headline.fontWeight },
		machineIdentity: { maxWidth: "55%", marginLeft: "auto" },
		projectMachineIdentity: { maxWidth: 132, flexShrink: 1 },

		summaryRow: { flexDirection: "row", alignItems: "center", minWidth: 0 },
		summaryStrong: { fontFamily: "DMSans_600SemiBold", flexShrink: 0, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		statusLabel: { marginLeft: space.xs },
		summary: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },

		blockerRow: { flexDirection: "row", alignItems: "center", minWidth: 0 },
		blocker: { fontFamily: "DMSans_400Regular", flexShrink: 0, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		blockerWorker: { fontFamily: "DMSans_600SemiBold", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },

		footerRow: { flexDirection: "row", alignItems: "center", gap: space.sm, minWidth: 0 },
		footerLabel: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		// A capsule, as both platforms draw a button; `overflow` keeps the Android
		// ripple inside it.
		pill: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: PILL_HEIGHT, paddingHorizontal: space.md, paddingVertical: space.xxs, borderRadius: radius.pill, overflow: "hidden" },
		pillSolid: { backgroundColor: t.bgElevated },
		pillOutline: { borderWidth: 1, borderColor: t.borderDefault },
		pillLabel: { fontFamily: "DMSans_600SemiBold", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },

		stats: { flexDirection: "row", marginHorizontal: space.lg, marginTop: space.xxs, borderTopWidth: rowDividerWidth, borderBottomWidth: rowDividerWidth, borderColor: t.borderSubtle },
		stat: { flex: 1, alignItems: "center", paddingVertical: space.sm, gap: space.none },
		statValue: { color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight, fontWeight: "600", fontFamily: t.fontMono, fontVariant: ["tabular-nums"] },
		statLabel: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		orchestratorRow: { minHeight: 76 },
		orchestratorBody: { paddingLeft: space.lg, paddingVertical: space.sm, gap: space.hair },
		orchestratorEyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: type.footnote.lineHeight },
		orchestratorEyebrowText: { fontFamily: "DMSans_500Medium", flex: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		orchestratorTitle: { fontFamily: type.headline.fontFamily, color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: type.headline.fontWeight },
	});
