import { useRouter } from "expo-router";
import { memo, useCallback, useEffect, useRef, useState } from "react";
import { Alert, BackHandler, Keyboard, StyleSheet, Text, TextInput, View } from "react-native";
import type { DashboardSession } from "./api";
import { AgentLogo } from "./AgentLogo";
import { haptics } from "./haptics";
import { sessionHostId } from "./hostedRows";
import { eventAtOf, workerRowPresentation, workerStatusGlyph, type WorkerStatusGlyph } from "./agentsView";
import { relativeTime } from "./notificationView";
import { Feather } from "./icons";
import { AppSymbol } from "./AppSymbol";
import type { AppSymbolName, PlatformSymbol } from "./symbols";
import { statusVisual, type Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { statusMarkColor } from "./statusMark";
import { textSelectionColors } from "./textSelection";
import { workerContextActions, type WorkerActionId } from "./worker-action-model";
import { WorkerRowActions } from "./worker-row-actions";
import { WorkerRowInteraction } from "./worker-row-interaction";
import { Spinner } from "./Spinner";
import { LatticeLoader } from "./chat/LatticeLoader";
import { normalizeConversationTitle } from "./chat/conversationMenuModel";
import { useOpenPage } from "./pageNavigation";
import { iconSize, space, type } from "./tokens";
import { userFacingError } from "./connectionError";
import { reviewRouteForSession } from "./reviewView";
import { isCloudHost } from "./cloud/mapping";
import { MachineAvatar } from "./MachineIdentity";
import type { MachineAppearance } from "./machineAppearance";

export const WorkerListRow = memo(
	function WorkerListRow({
	session,
	hostId,
	rowKey,
	projectName,
	machine,
	unreachable = false,
	isRenaming,
	activeSwipeId,
	nowBucket,
	onSwipeOpen,
	onSwipeClose,
	onRenameStart,
	onRenameCancel,
	onRename,
	onSetPinned,
	onDelete,
	onResume,
	onRestore,
}: {
	session: DashboardSession;
	/** Fallback for primary-only rows; hosted sessions carry their own host ID. */
	hostId?: string;
	rowKey?: string;
	projectName?: string;
	machine?: { name: string; appearance: MachineAppearance };
	/**
	 * The row's machine can't be reached, so what it shows is the last thing
	 * heard. Dimmed rather than hidden: the rows are still worth reading.
	 */
	unreachable?: boolean;
	isRenaming: boolean;
	activeSwipeId?: string;
	/**
	 * The current minute, passed in only so a row re-renders when its relative
	 * timestamp would read differently — the memo below compares session *values*,
	 * so nothing else would wake a row whose data has not changed.
	 */
	nowBucket: number;
	onSwipeOpen(id: string, close: () => void): void;
	onSwipeClose(id: string): void;
	onRenameStart(): void;
	onRenameCancel(): void;
	onRename(title: string): Promise<void>;
	onSetPinned(pinned: boolean): Promise<void>;
	onDelete(): void;
	/** Restart a stopped agent without resurrecting a terminated session. */
	onResume(): void;
	/** Bring a terminated session back. */
	onRestore(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const openPage = useOpenPage();
	const closeActionRailRef = useRef<() => void>(() => {});
	const [renameTitle, setRenameTitle] = useState("");
	const [renameSaving, setRenameSaving] = useState(false);
	// One way out of each rename: Return and the blur that follows it, or a
	// cancel and the blur the dismissed keyboard sends, must not both act.
	const renameSettled = useRef(false);
	const renaming = useRef(isRenaming);
	renaming.current = isRenaming;
	const row = workerRowPresentation(t, session, projectName);
	const visual = statusVisual(t, session.status);
	const glyph = workerStatusGlyph(session.status);
	// The subtitle is project and machine alone (no branch: a long worktree path
	// the session's header still shows). The pull request goes in a pill on the
	// row's end: its number, in the status's colour and glyph.
	const prLabel = pullRequestLabel(session);
	// When the worker last changed, the time the board orders it by; `nowBucket`
	// re-renders the row as the minute turns.
	const updated = relativeTime(eventAtOf(session));
	useEffect(() => {
		if (isRenaming) renameSettled.current = false;
	}, [isRenaming]);
	useEffect(() => {
		if (isRenaming) return;
		setRenameTitle(row.title);
		setRenameSaving(false);
	}, [isRenaming, row.title]);
	const cancelRename = useCallback(() => {
		if (renameSettled.current) return;
		renameSettled.current = true;
		Keyboard.dismiss();
		onRenameCancel();
	}, [onRenameCancel]);
	// An inline rename commits the way each platform's own lists do: Return, or
	// leaving the field. Android's back and VoiceOver's escape scrub cancel.
	const commitRename = useCallback(async () => {
		if (renameSettled.current) return;
		renameSettled.current = true;
		Keyboard.dismiss();
		const nextTitle = normalizeConversationTitle(renameTitle);
		// Left empty or unchanged, the worker keeps its name.
		if (!nextTitle || nextTitle === row.title) {
			onRenameCancel();
			return;
		}
		setRenameSaving(true);
		try {
			await onRename(nextTitle);
			haptics.success();
		} catch (cause) {
			// With no Cancel button, a field kept open on a failure would have no
			// way out, so the rename ends and says why.
			haptics.error();
			Alert.alert("Couldn't rename this worker", userFacingError(cause, "Please try again."));
		}
		// The board may have moved on to renaming another row meanwhile.
		if (renaming.current) onRenameCancel();
	}, [onRename, onRenameCancel, renameTitle, row.title]);
	useEffect(() => {
		if (!isRenaming) return;
		const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
			cancelRename();
			return true;
		});
		return () => subscription.remove();
	}, [cancelRename, isRenaming]);
	// No haptics on these presses: the pin's result and Archive's confirmation
	// each give their own, and opening a row is navigation.
	const renderRightActions = useCallback(
		() => (
			<WorkerRowActions
				title={row.title}
				pinned={Boolean(session.isPinned)}
				onSetPinned={(pinned) => {
					closeActionRailRef.current();
					void onSetPinned(pinned);
				}}
			/>
		),
		[onSetPinned, row.title, session.isPinned],
	);
	const openSession = useCallback(() => {
		router.push({
			pathname: "/session/[id]",
			params: { id: session.id, projectId: session.projectId, hostId: sessionHostId(session) ?? hostId },
		});
	}, [router, session, hostId]);

	const cloudRow = isCloudHost(sessionHostId(session) ?? hostId);
	// The PR page reads the worker's own host: its machine's daemon, or Orchestrator Cloud.
	const reviewRoute = reviewRouteForSession(session, sessionHostId(session) ?? hostId);
	const terminated = session.isTerminated === true || session.status === "terminated";
	const contextActions = workerContextActions({
		pinned: Boolean(session.isPinned),
		terminated,
		// A live session whose agent has stopped: exited or crashed, but the AO
		// session around it is still intact, so resuming is the lighter fix.
		stopped: !terminated && (session.status === "exited" || session.status === "errored"),
		hasPr: Boolean(reviewRoute),
		cloud: cloudRow,
	});

	const runAction = useCallback((id: WorkerActionId) => {
		switch (id) {
			case "open":
				return openSession();
			case "pin":
				return void onSetPinned(true);
			case "unpin":
				return void onSetPinned(false);
			case "rename":
				setRenameTitle(row.title);
				return onRenameStart();
			case "resume":
				return onResume();
			case "restore":
				return onRestore();
			case "openPr":
				if (reviewRoute) openPage(reviewRoute);
				return;
			default:
				return onDelete();
		}
	}, [onDelete, onRenameStart, onResume, onRestore, onSetPinned, openPage, reviewRoute, openSession, row.title]);

	return (
		<WorkerRowInteraction
			sessionId={rowKey ?? session.id}
			enabled={!isRenaming}
			activeSwipeId={activeSwipeId}
			rightActions={renderRightActions()}
			shellStyle={styles.shell}
			foregroundStyle={styles.foreground}
			rowStyle={styles.row}
			// No gesture hint: a screen reader reaches pin, rename and archive as
			// the row's actions, and announces that it has them.
			accessibilityLabel={`${row.title}. ${visual.label}. ${row.project}.${unreachable ? " Last known; its machine can't be reached." : ""}`}
			onPress={openSession}
			actions={contextActions}
			onAction={runAction}
			onSwipeOpen={onSwipeOpen}
			onSwipeClose={onSwipeClose}
			onReady={(close) => { closeActionRailRef.current = close; }}
		>
			{isRenaming ? (
				<WorkerRowContents
					row={row}
					visual={visual}
					glyph={glyph}
					prLabel={prLabel}
					updated={updated}
					harness={session.harness}
					machine={machine}
					unreachable={unreachable}
					isRenaming
					renameTitle={renameTitle}
					renameSaving={renameSaving}
					onRenameTitleChange={setRenameTitle}
					onRenameCancel={cancelRename}
					onRenameCommit={() => void commitRename()}
					styles={styles}
					t={t}
				/>
			) : (
				<WorkerRowContents
					row={row}
					visual={visual}
					glyph={glyph}
					prLabel={prLabel}
					updated={updated}
					harness={session.harness}
					machine={machine}
					unreachable={unreachable}
					styles={styles}
					t={t}
				/>
			)}
		</WorkerRowInteraction>
	);
},
	// Polls retain unchanged session objects. Bound handlers are stable too, but
	// must be compared so a changed connection never leaves stale actions behind.
	(prev, next) =>
		prev.nowBucket === next.nowBucket &&
		prev.rowKey === next.rowKey &&
		prev.projectName === next.projectName &&
		prev.hostId === next.hostId &&
		prev.machine?.name === next.machine?.name &&
		prev.machine?.appearance.symbol === next.machine?.appearance.symbol &&
		prev.unreachable === next.unreachable &&
		prev.isRenaming === next.isRenaming &&
		prev.activeSwipeId === next.activeSwipeId &&
		prev.session === next.session &&
		prev.onSwipeOpen === next.onSwipeOpen && prev.onSwipeClose === next.onSwipeClose &&
		prev.onRenameStart === next.onRenameStart && prev.onRenameCancel === next.onRenameCancel &&
		prev.onRename === next.onRename && prev.onSetPinned === next.onSetPinned &&
		prev.onDelete === next.onDelete && prev.onResume === next.onResume && prev.onRestore === next.onRestore,
);

function WorkerRowContents({
	row,
	visual,
	glyph,
	prLabel,
	updated,
	harness,
	machine,
	unreachable = false,
	isRenaming = false,
	renameTitle = "",
	renameSaving = false,
	onRenameTitleChange,
	onRenameCancel,
	onRenameCommit,
	styles,
	t,
}: {
	row: ReturnType<typeof workerRowPresentation>;
	visual: ReturnType<typeof statusVisual>;
	glyph: ReturnType<typeof workerStatusGlyph>;
	prLabel: string | null;
	updated: string;
	harness: DashboardSession["harness"];
	machine?: { name: string; appearance: MachineAppearance };
	unreachable?: boolean;
	isRenaming?: boolean;
	renameTitle?: string;
	renameSaving?: boolean;
	onRenameTitleChange?(title: string): void;
	onRenameCancel?(): void;
	onRenameCommit?(): void;
	styles: ReturnType<typeof makeStyles>;
	t: Theme;
}) {
	// One quiet line under the title: project, then the pull request, then the
	// machine. The state is the lead mark's alone; the row's accessibility label
	// still says it in words.
	const statusShown = row.trailingKind === "status";
	// The mark is a filled shape, so it takes the bright fill, not the text hue.
	const mark = statusMarkColor(t, visual.color, useThemeState().scheme);
	const working = statusShown && (visual.breathing || glyph === "loader");
	return (
		// The mark leads the title's line; the subtitle runs out to the row's edge
		// under it rather than indented under the title, so the line has room.
		<View style={[styles.stack, unreachable && styles.lastKnown]}>
			<View style={styles.line}>
				{/* The row's state at a glance: the desktop's lattice while the agent
				    works (a still mark reads as stuck), otherwise a rounded square in the
				    status's colour. */}
				<View style={styles.lead}>
					{working ? <LatticeLoader color={mark} />
					: <View testID="worker-status-dot" style={[styles.dot, { backgroundColor: statusShown ? mark : t.textFaint }]} />}
				</View>
				<View style={styles.body}>
					{isRenaming ? (
						<View style={styles.titleEditor}>
							<TextInput
								autoFocus
								accessibilityLabel="Worker name"
								accessibilityHint="Saves when you press Return or leave the field."
								// VoiceOver's escape scrub backs out of the rename.
								onAccessibilityEscape={onRenameCancel}
								value={renameTitle}
								onChangeText={onRenameTitleChange}
								editable={!renameSaving}
								placeholder="Worker name"
								placeholderTextColor={t.textFaint}
								{...textSelectionColors(t)}
								maxLength={120}
								returnKeyType="done"
								onSubmitEditing={onRenameCommit}
								onBlur={onRenameCommit}
								style={styles.renameInput}
							/>
							{renameSaving ? <Spinner size={iconSize.md} accessibilityLabel="Saving worker name" /> : null}
						</View>
					) : (
						<View style={styles.titleLine}>
							<Text style={[styles.title, styles.shrink]} numberOfLines={1}>
								{row.title}
							</Text>
							{/* When it last changed, quiet at the line's end, as a mail list dates its rows. */}
							{updated ? <Text testID="worker-updated" style={styles.updated} numberOfLines={1}>{updated}</Text> : null}
						</View>
					)}
				</View>
		</View>
			<View style={styles.subtitle}>
				<AgentLogo harness={harness} size={12} />
				<Text style={styles.meta} numberOfLines={1}>{row.project}</Text>
				{/* The pull request right after its project: number, glyph and colour. */}
				{prLabel ? <PullRequestPill label={prLabel} color={visual.color} glyph={pillGlyph(glyph)} t={t} styles={styles} /> : null}
				{/* The machine as a faint chip, its glyph leading: set apart from the
				    project and pull request without a separator. */}
				{machine ? (
					<View testID="worker-machine-identity" accessibilityLabel={`Machine ${machine.name}`} style={styles.machineChip}>
						<View style={[StyleSheet.absoluteFill, styles.machineChipWash]} />
						<MachineAvatar testID="machine-identity-symbol" appearance={machine.appearance} size={14} color={t.textTertiary} />
						<Text testID="machine-identity-name" style={[styles.meta, styles.shrink]} numberOfLines={1}>{machine.name}</Text>
					</View>
				) : null}
			</View>
		</View>
	);
}

/** `#1253`, or `#1253 +1` for a worker with more than one; null with none. */
export function pullRequestLabel(session: DashboardSession): string | null {
	const list = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const real = list.filter((pr) => pr?.number > 0);
	if (real.length === 0) return null;
	return real.length > 1 ? `#${real[0].number} +${real.length - 1}` : `#${real[0].number}`;
}

/**
 * The pill's glyph: the status's own when it speaks to the pull request
 * (approved, CI failed, needs input), the pull request mark otherwise, such as
 * while the agent works or idles.
 */
function pillGlyph(glyph: WorkerStatusGlyph | null): Exclude<WorkerStatusGlyph, "loader"> {
	return glyph && glyph !== "loader" && glyph !== "moon" ? glyph : "git-pull-request";
}

/**
 * The worker's pull request as an icon-and-number pill: the status's glyph and
 * the PR's number in the status's hue over the matching tint, so a mergeable,
 * failing or merged pull request reads by colour and shape at a glance.
 */
function PullRequestPill({ label, color, glyph, t, styles }: {
	label: string;
	color: string;
	glyph: Exclude<WorkerStatusGlyph, "loader">;
	t: Theme;
	styles: ReturnType<typeof makeStyles>;
}) {
	return (
		<View testID="worker-pr-pill" style={[styles.pill, { backgroundColor: statusTint(t, color) }]}>
			<StatusGlyph glyph={glyph} color={color} />
			<Text style={[styles.pillText, { color }]} numberOfLines={1}>{label}</Text>
		</View>
	);
}

/** The tint that goes under a status hue; a neutral surface for the grey states. */
function statusTint(t: Theme, color: string): string {
	if (color === t.amber) return t.tintAmber;
	if (color === t.green) return t.tintGreen;
	if (color === t.red) return t.tintRed;
	if (color === t.orange) return t.tintOrange;
	if (color === t.purple) return t.tintPurple;
	if (color === t.blue) return t.tintBlue;
	return t.bgElevated;
}

/**
 * Status glyphs are chrome, so each draws as its platform's own symbol. The
 * pull request keeps its Lucide glyph: a domain mark with no system symbol.
 */
const STATUS_SYMBOLS: Record<Exclude<WorkerStatusGlyph, "git-pull-request" | "loader">, AppSymbolName | PlatformSymbol> = {
	"alert-circle": "error",
	"message-square": "chat",
	"x-octagon": { ios: "xmark.octagon", android: "dangerous" },
	"check-circle": "checkCircle",
	moon: "idle",
};

function StatusGlyph({ glyph, color }: { glyph: Exclude<WorkerStatusGlyph, "loader">; color: string }) {
	if (glyph === "git-pull-request") return <Feather name={glyph} size={iconSize.xs} color={color} />;
	return <AppSymbol name={STATUS_SYMBOLS[glyph]} size={iconSize.xs} color={color} />;
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		shell: {
			minHeight: 64,
			overflow: "hidden",
		},
		foreground: { backgroundColor: t.bgBase },
		row: {
			minHeight: 64,
			paddingHorizontal: space.lg,
			paddingVertical: space.md,
			justifyContent: "center",
		},
		stack: { gap: 4 },
		line: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
		// As tall as the title's line, so the dot or spinner sits level with it.
		lead: { alignSelf: "flex-start", width: 16, height: 22, alignItems: "center", justifyContent: "center" },
		// A rounded square, about the lattice's size, so a row's mark keeps its shape
		// when the agent stops working: amber when it needs you, green when done.
		dot: { width: 11, height: 11, borderRadius: 3, borderCurve: "continuous" },
		body: { flex: 1, minWidth: 0 },
		titleEditor: { minHeight: 22, flexDirection: "row", alignItems: "center", gap: space.xs },
		renameInput: { fontFamily: "DMSans_500Medium", flex: 1, minWidth: 0, minHeight: 22, paddingHorizontal: space.none, paddingVertical: space.none, borderWidth: 0, backgroundColor: "transparent", color: t.textPrimary, fontSize: 16, lineHeight: 22, fontWeight: "500", letterSpacing: -0.15, includeFontPadding: false, textAlignVertical: "center" },
		titleLine: { flexDirection: "row", alignItems: "baseline", gap: space.sm },
		updated: { fontFamily: "DMSans_400Regular", flexShrink: 0, marginLeft: "auto", color: t.textFaint, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontVariant: ["tabular-nums"] },
		title: { fontFamily: "DMSans_500Medium", color: t.textPrimary, fontSize: 16, lineHeight: 22, fontWeight: "500", letterSpacing: -0.15 },
		subtitle: { flexDirection: "row", alignItems: "center", gap: space.xs, minWidth: 0, overflow: "hidden" },
		meta: { fontFamily: "DMSans_400Regular", flexShrink: 0, color: t.textTertiary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontVariant: ["tabular-nums"] },
		separator: { fontFamily: "DMSans_400Regular", color: t.textFaint, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		// Gives way before the project and pull request when the line runs short.
		machineChip: { flexDirection: "row", alignItems: "center", gap: 4, flexShrink: 1, minWidth: 0, height: 20, paddingHorizontal: 7, borderRadius: 10, overflow: "hidden" },
		// The text colour at a whisper, so the chip reads on any theme.
		machineChipWash: { backgroundColor: t.textPrimary, opacity: 0.06 },
		shrink: { flexShrink: 1 },
		pill: { flexDirection: "row", alignItems: "center", gap: 3, flexShrink: 0, height: 20, paddingHorizontal: 7, borderRadius: 10 },
		pillText: { fontFamily: "DMSans_500Medium", fontSize: 12, lineHeight: 15, fontWeight: "500", fontVariant: ["tabular-nums"] },
		// A row whose machine can't be reached shows the last thing heard, so it
		// must not read as live: the spinner and status stay, at a lower voice.
		lastKnown: { opacity: 0.5 },
	});
