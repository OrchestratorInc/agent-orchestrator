import { Feather } from "./icons";
import { useRouter } from "expo-router";
import { memo, useCallback, useEffect, useRef, useState } from "react";
import { Alert, BackHandler, Keyboard, StyleSheet, Text, TextInput, View } from "react-native";
import type { DashboardSession } from "./api";
import { AgentLogo } from "./AgentLogo";
import { haptics } from "./haptics";
import { sessionHostId } from "./hostedRows";
import { prLine, workerRowPresentation, workerStatusGlyph, type WorkerStatusGlyph } from "./agentsView";
import { AppSymbol } from "./AppSymbol";
import type { AppSymbolName, PlatformSymbol } from "./symbols";
import { toneColor } from "./prView";
import { statusVisual, type Theme } from "./theme";
import { rowDividerWidth } from "./divider";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { textSelectionColors } from "./textSelection";
import { workerContextActions, type WorkerActionId } from "./worker-action-model";
import { WorkerRowActions } from "./worker-row-actions";
import { WorkerRowInteraction } from "./worker-row-interaction";
import { Spinner } from "./Spinner";
import { normalizeConversationTitle } from "./chat/conversationMenuModel";
import { useOpenPage } from "./pageNavigation";
import { iconSize, space, type } from "./tokens";
import { userFacingError } from "./connectionError";
import { reviewRouteForSession } from "./reviewView";
import { MachineIdentity } from "./MachineIdentity";
import type { MachineAppearance } from "./machineAppearance";

export const WorkerListRow = memo(
	function WorkerListRow({
	session,
	hostId,
	rowKey,
	projectName,
	machine,
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
	const prs = prLine(session);
	// The second line is the pull request when there is one, and the branch when
	// there is not. The branch used to lead unconditionally, which put a worktree
	// path (`ao/dev/<project>-N/root`) under every title — the same string the
	// session header already shows. Then it went missing entirely for a row with no
	// PR, which left the line blank on exactly the sessions that had no other way to
	// name their branch. `workerRowPresentation` has already dropped a branch that
	// only restates the title, so whatever reaches here says something new.
	const details = prs?.text ?? row.branch ?? "";
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
				onDelete={() => { closeActionRailRef.current(); onDelete(); }}
			/>
		),
		[onDelete, onSetPinned, row.title, session.isPinned],
	);
	const openSession = useCallback(() => {
		router.push({
			pathname: "/session/[id]",
			params: { id: session.id, projectId: session.projectId, hostId: sessionHostId(session) ?? hostId },
		});
	}, [router, session, hostId]);

	const reviewRoute = reviewRouteForSession(session, sessionHostId(session) ?? hostId);
	const terminated = session.isTerminated === true || session.status === "terminated";
	const contextActions = workerContextActions({
		pinned: Boolean(session.isPinned),
		terminated,
		// A live session whose agent has stopped: exited or crashed, but the AO
		// session around it is still intact, so resuming is the lighter fix.
		stopped: !terminated && (session.status === "exited" || session.status === "errored"),
		hasPr: Boolean(reviewRoute),
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
			accessibilityLabel={`${row.title}. ${visual.label}. ${row.project}.`}
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
					details={details}
					prsTone={prs?.tone}
					harness={session.harness}
					machine={machine}
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
					details={details}
					prsTone={prs?.tone}
					harness={session.harness}
					machine={machine}
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
	details,
	prsTone,
	harness,
	machine,
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
	details: string;
	prsTone?: Parameters<typeof toneColor>[1];
	harness: DashboardSession["harness"];
	machine?: { name: string; appearance: MachineAppearance };
	isRenaming?: boolean;
	renameTitle?: string;
	renameSaving?: boolean;
	onRenameTitleChange?(title: string): void;
	onRenameCancel?(): void;
	onRenameCommit?(): void;
	styles: ReturnType<typeof makeStyles>;
	t: Theme;
}) {
	return (
		<>
			<View style={styles.eyebrow}>
				<AgentLogo harness={harness} size={14} />
				<Text style={styles.project} numberOfLines={1}>
					{row.project}
				</Text>
				{/* Paired with the tinted label so status reads by shape as well as
				    colour. Only shown alongside a real status — when the row is
				    showing an elapsed time instead, there is no state to depict.
				    A working or starting row shows the system's spinner: a still
				    circle reads as stuck, and a pulse says "waiting", which is what
				    the row above it says for a different state. */}
				{glyph && row.trailingKind === "status" ? (
					visual.breathing || glyph === "loader"
						? <Spinner size={iconSize.xs} color={visual.color} />
						: <StatusGlyph glyph={glyph} color={visual.color} />
				) : null}
				<Text
					style={[styles.trailing, { color: row.trailingKind === "status" ? visual.color : t.textTertiary }]}
					numberOfLines={1}
				>
					{row.trailing}
				</Text>
			</View>

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
				<Text style={styles.title} numberOfLines={1}>
					{row.title}
				</Text>
			)}

			{details || machine ? (
				<View style={styles.metadata}>
					{details ? (
						<Text style={[styles.details, prsTone && { color: toneColor(t, prsTone) }]} numberOfLines={1}>
							{details}
						</Text>
					) : <View style={styles.metadataSpacer} />}
					{machine ? <MachineIdentity testID="worker-machine-identity" name={machine.name} appearance={machine.appearance} style={styles.machineIdentity} /> : null}
				</View>
			) : null}
		</>
	);
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
			minHeight: 76,
			overflow: "hidden",
		},
		foreground: { backgroundColor: t.bgBase },
		row: {
			minHeight: 76,
			paddingHorizontal: space.lg,
			paddingVertical: space.sm + space.hair,
			gap: space.hair,
		},
		// Inset to the text, as T3 Code's list rows are, and drawn inside the row
		// so it travels with a swipe instead of staying behind on the rail.
		divider: { position: "absolute", left: space.lg, right: 0, bottom: 0, height: rowDividerWidth, backgroundColor: t.borderDefault },
		titleEditor: { minHeight: 32, flexDirection: "row", alignItems: "center", gap: space.xs },
		renameInput: { fontFamily: type.headline.fontFamily, flex: 1, minWidth: 0, minHeight: 32, paddingHorizontal: space.none, paddingVertical: space.none, borderWidth: 0, backgroundColor: "transparent", color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: type.headline.fontWeight, includeFontPadding: false, textAlignVertical: "center" },
		eyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: type.footnote.lineHeight },
		project: { fontFamily: "DMSans_500Medium", flex: 1, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
		trailing: { fontFamily: "DMSans_600SemiBold", flexShrink: 0, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600", fontVariant: ["tabular-nums"] },
		title: { fontFamily: type.headline.fontFamily, color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: type.headline.fontWeight },
		metadata: { minWidth: 0, flexDirection: "row", alignItems: "center", gap: space.sm },
		metadataSpacer: { flex: 1 },
		details: { flex: 1, minWidth: 0, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontFamily: t.fontMono },
		machineIdentity: { maxWidth: "52%", marginLeft: "auto" },
	});
