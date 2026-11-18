import type { ReactNode } from "react";
import { Pressable, StyleSheet, Text, View, type StyleProp, type ViewStyle } from "react-native";
import Svg, { Path } from "react-native-svg";
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

/** The trailing button's side. */
const PILL_SIZE = 40;
/** The glyph inside it. */
const GLYPH_SIZE = 20;
/** Reaches the platform's touch target around a button drawn smaller than it. */
const PILL_HIT_SLOP = (touchTarget - PILL_SIZE) / 2;

/**
 * One project on the Projects page: who it is, how its fleet is doing, and the
 * way into its orchestrator.
 *
 * A flat row with a divider, like a worker row, rather than a card: the two
 * lists read as one app. Pressed anywhere, it opens the project page. At its
 * end a round button starts or resumes the orchestrator (play), or opens a
 * running one (the orchestrator's mark): a labelled pill outweighed the
 * project's name.
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
			trailingAction={{ label: orchestratorButtonCopy(row, busy).label, onPress: () => onOrchestrator(row) }}
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
 * A row that is one control, with a second control at its trailing end. The
 * whole row presses, ripple and highlight included: with only the text block
 * pressable, a tap beside the button or below the text did nothing. The button
 * is nested inside and takes its own touches; a screen reader, which hears the
 * row as one element, reaches it as the row's custom action.
 *
 * The trailing slot takes only the width its control needs: at a large text
 * size the body narrows instead of running under it. A footer runs the row's
 * full width under both.
 */
function TrailingActionRow({
	style,
	bodyStyle,
	accessibilityLabel,
	accessibilityHint,
	disabled,
	onPress,
	trailing,
	trailingAction,
	footer,
	children,
}: {
	style: StyleProp<ViewStyle>;
	bodyStyle: StyleProp<ViewStyle>;
	accessibilityLabel: string;
	accessibilityHint?: string;
	disabled?: boolean;
	onPress: () => void;
	trailing?: ReactNode;
	/** What the trailing control does, for a screen reader. */
	trailingAction?: { label: string; onPress(): void };
	footer?: ReactNode;
	children: ReactNode;
}) {
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={accessibilityLabel}
			accessibilityHint={accessibilityHint}
			accessibilityState={disabled ? { disabled } : undefined}
			accessibilityActions={trailingAction ? [{ name: "activate" }, { name: "trailing", label: trailingAction.label }] : undefined}
			onAccessibilityAction={(event) => {
				if (event.nativeEvent.actionName === "trailing") trailingAction?.onPress();
				else if (!disabled) onPress();
			}}
			disabled={disabled}
			android_ripple={feedback.android_ripple}
			onPress={onPress}
			style={({ pressed }) => [style, pressed && feedback.pressed]}
		>
			<View style={styles.trailingRow}>
				<View style={[styles.trailingBody, bodyStyle, footer ? styles.bodyAboveFooter : null]}>{children}</View>
				{/* Moved down by what the body gave up, so the button stays level with
				    the same two lines on every row. */}
				{trailing ? <View style={[styles.trailingSlot, footer ? styles.slotAboveFooter : null]}>{trailing}</View> : null}
			</View>
			{footer ? <View style={styles.footer}>{footer}</View> : null}
			<View style={styles.divider} />
		</Pressable>
	);
}

const PLAY_PATH = "M7.5 5.9v12.2a1.6 1.6 0 0 0 2.45 1.36l9.6-6.1a1.6 1.6 0 0 0 0-2.72l-9.6-6.1A1.6 1.6 0 0 0 7.5 5.9Z";

/**
 * The orchestrator's round button at a row's end: a play button to start or
 * resume it, and the orchestrator's own mark once it runs and opens. Not a
 * chevron: at a row's end a chevron reads as "open this row", and the row
 * opens the project.
 */
function OrchestratorPill({
	row,
	busy,
	onPress,
}: {
	row: OrchestratorProjectRow;
	busy: boolean;
	onPress: (row: OrchestratorProjectRow) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const copy = orchestratorButtonCopy(row, busy);
	const live = copy.running;
	// One quiet fill for both faces: a solid accent disc outweighed the name.
	const ink = t.textPrimary;
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
			style={({ pressed }) => [styles.pill, pressed && feedback.pressed]}
		>
			{busy
				? <Spinner size={iconSize.sm} color={ink} />
				: <OrchestratorGlyph live={live} color={ink} />}
		</Pressable>
	);
}

/**
 * The button's glyph, drawn rather than taken from the symbol font: Material's
 * play came out thin and sharp at this size, so it is a solid play with rounded
 * corners. A running orchestrator shows the mark it has on the desktop.
 */
function OrchestratorGlyph({ live, color }: { live: boolean; color: string }) {
	if (live) return <OrchestratorIcon size={GLYPH_SIZE} color={color} />;
	return (
		<Svg width={GLYPH_SIZE} height={GLYPH_SIZE} viewBox="0 0 24 24">
			<Path d={PLAY_PATH} fill={color} />
		</Svg>
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
 * orchestrator — its state and what it reports — as a card above the workers.
 * Drawn flat like the rows beneath it, nothing said it opened anything; the
 * card sets it apart as the one thing to open, and ends in a chevron, or in the
 * pill that starts or resumes it.
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
	// A strip of zeros says nothing: past the worker count, only what is there.
	].filter((tile, index) => index === 0 || tile.value > 0);
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

			{/* The whole card opens the orchestrator; the pill starts or resumes one. */}
			<TrailingActionRow
				style={styles.orchestratorCard}
				bodyStyle={styles.orchestratorBody}
				accessibilityLabel={`${orchestratorButtonCopy(row, busy).label}, ${status.label}, ${row.detail}`}
				disabled={busy}
				onPress={() => onPress(row)}
				trailing={<OrchestratorPill row={row} busy={busy} onPress={onPress} />}
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

		// A soft filled circle on the page, as the old labelled pill was filled;
		// `overflow` keeps the Android ripple inside it.
		pill: { width: PILL_SIZE, height: PILL_SIZE, alignItems: "center", justifyContent: "center", borderRadius: radius.pill, overflow: "hidden", backgroundColor: t.bgElevated },

		stats: { flexDirection: "row", marginHorizontal: space.lg, marginTop: space.xxs, borderTopWidth: rowDividerWidth, borderBottomWidth: rowDividerWidth, borderColor: t.borderSubtle },
		stat: { flex: 1, alignItems: "center", paddingVertical: space.sm, gap: space.none },
		statValue: { color: t.textPrimary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight, fontWeight: "600", fontFamily: t.fontMono, fontVariant: ["tabular-nums"] },
		statLabel: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		// A filled card with no outline or shadow, as a Settings group is
		// (DESIGN.md); `overflow` keeps the Android ripple inside its corners.
		orchestratorCard: { minHeight: 76, marginHorizontal: space.lg, marginTop: space.md, marginBottom: space.sm, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden", backgroundColor: t.bgElevated },
		orchestratorBody: { paddingLeft: space.lg, paddingVertical: space.sm, gap: space.hair },
		orchestratorEyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: type.footnote.lineHeight },
		orchestratorEyebrowText: { fontFamily: "DMSans_500Medium", flex: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		orchestratorTitle: { fontFamily: type.headline.fontFamily, color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: type.headline.fontWeight },
	});
