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
import { statusVisual, type Theme } from "./theme";
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
 *
 * The name has the body's full width; everything else is one quiet line under
 * it, plus the blocker on a row that needs attention.
 */
export function ProjectCard({
	row,
	busy,
	showMachine = true,
	onOpenProject,
	onOrchestrator,
}: {
	row: HostedProjectRow;
	busy: boolean;
	/** Off when one machine is paired: it would repeat the same name on every row. */
	showMachine?: boolean;
	onOpenProject: (row: OrchestratorProjectRow) => void;
	onOrchestrator: (row: OrchestratorProjectRow) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const t = useTheme();
	const status = orchestratorStatus(t, row.link);
	const summary = projectCardSummary(row);
	const blocker = projectBlockerLine(row);
	const blockerColor = blocker ? statusVisual(t, blocker.status).color : undefined;
	return (
		<TrailingActionRow
			style={styles.row}
			bodyStyle={styles.body}
			accessibilityLabel={[
				row.project.name,
				showMachine ? `${row.project.hostName} machine` : null,
				`orchestrator ${status.label}`,
				summary.workers,
				blocker ? `${blocker.reason}: ${blocker.worker}` : null,
			].filter(Boolean).join(", ")}
			accessibilityHint="Opens the project"
			onPress={() => onOpenProject(row)}
			trailing={<OrchestratorPill row={row} busy={busy} onPress={onOrchestrator} />}
			footer={blocker ? (
				// Under the pill rather than beside it, so the worker's name has the
				// row's width. The reason comes first, in the colour the worker's own
				// row gives it, so it survives the ellipsis; the name is what gives way.
				<View style={styles.blockerRow}>
					<Text style={[styles.blockerReason, { color: blockerColor }]} numberOfLines={1}>
						{blocker.reason}
					</Text>
					<Text style={styles.blockerWorker} numberOfLines={1}>
						{` · ${blocker.worker}`}
					</Text>
					{blocker.age ? (
						<Text style={styles.blocker} numberOfLines={1}>
							{` · ${blocker.age}`}
						</Text>
					) : null}
				</View>
			) : undefined}
		>
			<Text style={styles.project} numberOfLines={1}>
				{row.project.name}
			</Text>

			{/* The orchestrator's state, the fleet's size, and where it runs. */}
			<View style={styles.summaryRow}>
				<Dot color={status.color} size={6} breathing={status.breathing} />
				<Text style={[styles.summaryStrong, styles.statusLabel, { color: status.color }]} numberOfLines={1}>
					{status.label}
				</Text>
				<Text style={styles.summary} numberOfLines={1}>
					{` · ${summary.workers}`}
				</Text>
				{showMachine ? (
					<MachineIdentity
						name={row.project.hostName}
						appearance={machineAppearance({ id: row.project.hostId, name: row.project.hostName, appearance: row.project.hostAppearance })}
						style={styles.machineIdentity}
					/>
				) : null}
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
 *
 * A footer runs the row's full width under both. It presses as the body does
 * but is hidden from a screen reader, whose body label already reads it out.
 */
function TrailingActionRow({
	style,
	bodyStyle,
	accessibilityLabel,
	accessibilityHint,
	disabled,
	onPress,
	trailing,
	footer,
	children,
}: {
	style: StyleProp<ViewStyle>;
	bodyStyle: StyleProp<ViewStyle>;
	accessibilityLabel: string;
	accessibilityHint?: string;
	disabled?: boolean;
	onPress: () => void;
	trailing: ReactNode;
	footer?: ReactNode;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [pressed, setPressed] = useState(false);
	// Only iOS draws a pressed style; Android's ripple needs no re-render.
	const onPressIn = feedback.pressed ? () => setPressed(true) : undefined;
	const onPressOut = feedback.pressed ? () => setPressed(false) : undefined;
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
					onPressIn={onPressIn}
					onPressOut={onPressOut}
					style={[styles.trailingBody, bodyStyle, footer ? styles.bodyAboveFooter : null]}
				>
					{children}
				</Pressable>
				{/* Moved down by what the body gave up, so the pill stays level with
				    the same two lines on every row. */}
				<View style={[styles.trailingSlot, footer ? styles.slotAboveFooter : null]}>{trailing}</View>
			</View>
			{footer ? (
				<Pressable
					accessible={false}
					accessibilityElementsHidden
					importantForAccessibility="no-hide-descendants"
					disabled={disabled}
					android_ripple={feedback.android_ripple}
					onPress={onPress}
					onPressIn={onPressIn}
					onPressOut={onPressOut}
					style={styles.footer}
				>
					{footer}
				</Pressable>
			) : null}
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
		body: { paddingLeft: space.lg, paddingVertical: space.md, gap: space.hair },
		bodyAboveFooter: { paddingBottom: space.hair },
		slotAboveFooter: { paddingTop: space.md - space.hair },
		footer: { paddingHorizontal: space.lg, paddingBottom: space.md },
		trailingRow: { flexDirection: "row", alignItems: "stretch" },
		trailingBody: { flex: 1, minWidth: 0, justifyContent: "center" },
		// Centred on the row whatever its height, and never narrower than its pill.
		trailingSlot: { flexShrink: 0, justifyContent: "center", paddingLeft: space.md, paddingRight: space.lg },
		// Inset to the text, as a worker row's is.
		divider: { marginLeft: space.lg, height: rowDividerWidth, backgroundColor: t.borderSubtle },

		project: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: 16, lineHeight: 21, fontWeight: "600", letterSpacing: -0.15 },
		// Pushed to the line's end, as on a worker row, and the first thing to
		// give way when the line runs out.
		machineIdentity: { marginLeft: "auto", paddingLeft: space.sm, maxWidth: "50%", flexShrink: 1 },
		projectMachineIdentity: { maxWidth: 132, flexShrink: 1 },

		summaryRow: { flexDirection: "row", alignItems: "center", minWidth: 0 },
		summaryStrong: { fontFamily: "DMSans_600SemiBold", flexShrink: 0, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
		statusLabel: { marginLeft: space.xs },
		summary: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },

		blockerRow: { flexDirection: "row", alignItems: "center", minWidth: 0 },
		blocker: { fontFamily: "DMSans_400Regular", flexShrink: 0, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		blockerReason: { fontFamily: "DMSans_500Medium", flexShrink: 0, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		blockerWorker: { fontFamily: "DMSans_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },

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
