import { memo, startTransition, useDeferredValue, useEffect, useMemo, useRef, useState, type KeyboardEvent, type SyntheticEvent, type UIEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Command as CommandPrimitive } from "cmdk";
import type { WorkspaceSession } from "../types/workspace";
import { agentLabel } from "../lib/agent-options";
import { sessionBranchPresentation } from "../lib/session-branch";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { isMacPlatform } from "../lib/platform";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { CloseButton } from "./ui/close-button";
import { ArchivedSessionCardAdapter, DeleteArchivedSessionDialog } from "./SessionsBoardAdapters";
import {
	CommandDialog,
	CommandEmpty,
	CommandFooter,
	CommandInput,
	CommandList,
	CommandShortcut,
} from "./ui/command";

const isMac = isMacPlatform();
const MOD_KEY = isMac ? "⌘" : "Ctrl";
/** Cards mounted when the modal opens or the search changes. */
const PAGE_SIZE = 24;
/**
 * The rest mount a few at a time while the page is idle, up to this many, so
 * scrolling never waits on a mount. Past it, scrolling or arrowing near the end
 * mounts the next page.
 */
const IDLE_BATCH = 6;
const IDLE_LIMIT = 120;

/** Everything a search can match, lowercased once per session. */
function searchText(session: WorkspaceSession): string {
	return [
		session.title,
		sessionBranchPresentation(session).branch,
		session.workspaceName,
		agentLabel(session.provider),
		session.provider,
		...session.prs.map((pr) => `#${pr.number}`),
	]
		.filter(Boolean)
		.join("\n")
		.toLowerCase();
}

/**
 * The board's archive: a searchable grid of ended-session cards in the Cmd+K
 * surface. Sessions arrive newest first. Restore state lives here, and
 * in-flight restores are dropped when the board scope changes or this
 * unmounts so a late completion cannot navigate after the user left.
 */
export function SessionArchiveModal({
	connected,
	hostId,
	onOpenChange,
	onOpenSession,
	open,
	resetKey,
	sessions,
}: {
	connected: boolean;
	hostId?: string;
	onOpenChange: (open: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
	open: boolean;
	/** The board scope; a change drops restore state. */
	resetKey: string;
	sessions: WorkspaceSession[];
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	const [query, setQuery] = useState("");
	const deferredQuery = useDeferredValue(query);
	// The pointer's hover is the card's own CSS, as on the board. Selection is
	// only drawn while the arrow keys move it, and clears once the pointer moves.
	// cmdk keeps the selection itself: tracking it here re-rendered every card
	// each time the pointer crossed into another one.
	const [keyboardSelecting, setKeyboardSelecting] = useState(false);
	const [limit, setLimit] = useState(PAGE_SIZE);
	const [restoringSessionId, setRestoringSessionId] = useState<string | undefined>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>({});
	const [restoreUnavailableSession, setRestoreUnavailableSession] = useState<WorkspaceSession | undefined>();
	const [deleteTarget, setDeleteTarget] = useState<WorkspaceSession | undefined>();
	const restoreGenerationRef = useRef(0);
	const resetKeyRef = useRef(resetKey);
	resetKeyRef.current = resetKey;

	// Each opening starts from an empty search at the newest session.
	const [wasOpen, setWasOpen] = useState(open);
	if (open !== wasOpen) {
		setWasOpen(open);
		if (open) {
			setQuery("");
			setLimit(PAGE_SIZE);
			setKeyboardSelecting(false);
		}
	}

	useEffect(() => {
		setRestoringSessionId(undefined);
		setRestoreErrors({});
		setRestoreUnavailableSession(undefined);
		setDeleteTarget(undefined);
		restoreGenerationRef.current += 1;
	}, [resetKey]);

	useEffect(() => () => {
		restoreGenerationRef.current += 1;
	}, []);

	// Built only while open: the board re-renders on every workspace poll.
	const index = useMemo(
		() => (open ? sessions.map((session) => ({ session, text: searchText(session) })) : []),
		[open, sessions],
	);
	const results = useMemo(() => {
		const tokens = deferredQuery.trim().toLowerCase().split(/\s+/).filter(Boolean);
		if (tokens.length === 0) return sessions;
		return index.filter((entry) => tokens.every((token) => entry.text.includes(token))).map((entry) => entry.session);
	}, [deferredQuery, index, sessions]);
	// The first page renders with the modal, so it never opens empty.
	const shown = useMemo(() => (results.length > limit ? results.slice(0, limit) : results), [limit, results]);
	const shownRef = useRef(shown);
	shownRef.current = shown;
	const restoreAvailable = connected && restoringSessionId === undefined;
	const canDelete = (session: WorkspaceSession) =>
		// The control plane owns a cloud session's record; this daemon cannot delete it.
		!session.cloud && restoringSessionId !== session.id;

	// One page per render: a burst of scroll events must not mount several.
	const growingRef = useRef(false);
	useEffect(() => {
		growingRef.current = false;
	}, [limit, results]);
	const showMore = () => {
		if (growingRef.current || limit >= results.length) return;
		growingRef.current = true;
		startTransition(() => setLimit((current) => current + PAGE_SIZE));
	};

	useEffect(() => {
		if (!open || limit >= Math.min(results.length, IDLE_LIMIT)) return;
		const grow = () => setLimit((current) => current + IDLE_BATCH);
		if (typeof requestIdleCallback !== "function") {
			const timer = setTimeout(grow, 50);
			return () => clearTimeout(timer);
		}
		const handle = requestIdleCallback(grow, { timeout: 500 });
		return () => cancelIdleCallback(handle);
	}, [limit, open, results.length]);

	const openSession = (session: WorkspaceSession) => {
		onOpenChange(false);
		onOpenSession(session);
	};

	const restore = async (session: WorkspaceSession) => {
		if (!restoreAvailable) return;
		const scope = resetKeyRef.current;
		const generation = restoreGenerationRef.current;
		const stillHere = () => generation === restoreGenerationRef.current && resetKeyRef.current === scope;
		setRestoringSessionId(session.id);
		setRestoreErrors((current) => {
			const next = { ...current };
			delete next[session.id];
			return next;
		});
		try {
			const result = await restoreSessionById(session.id, hostId);
			if (!stillHere()) return;
			if (result.status === "success") {
				onOpenChange(false);
				void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
				return;
			}
			if (result.status === "not_resumable") {
				setRestoreUnavailableSession(session);
				return;
			}
			setRestoreErrors((current) => ({ ...current, [session.id]: result.message }));
		} finally {
			if (stillHere()) setRestoringSessionId(undefined);
		}
	};

	// Cells get one stable set of callbacks that always reach the latest state,
	// so a card re-renders only when its own data changes.
	const actionsRef = useRef({ openSession, restore, requestDelete: setDeleteTarget });
	actionsRef.current = { openSession, restore, requestDelete: setDeleteTarget };
	const actions = useMemo<CardActions>(() => ({
		open: (session) => actionsRef.current.openSession(session),
		restore: (session) => void actionsRef.current.restore(session),
		requestDelete: (session) => actionsRef.current.requestDelete(session),
	}), []);

	const onInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (["ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown"].includes(event.key)) setKeyboardSelecting(true);
		if (event.nativeEvent.isComposing) return;
		const mod = isMac ? event.metaKey : event.ctrlKey;
		if (!mod || event.key !== "Enter") return;
		// cmdk owns the selection; ask the list which card it has.
		const selectedId = event.currentTarget.closest("[cmdk-root]")
			?.querySelector("[cmdk-item][aria-selected='true']")?.getAttribute("data-value");
		const selectedSession = shown.find((session) => session.id === selectedId) ?? shown[0];
		if (!selectedSession) return;
		event.preventDefault();
		event.stopPropagation();
		void restore(selectedSession);
	};

	const onListScroll = (event: UIEvent<HTMLDivElement>) => {
		const list = event.currentTarget;
		if (list.scrollTop + list.clientHeight >= list.scrollHeight - 240) showMore();
	};

	return (
		<>
			<CommandDialog
				open={open}
				onOpenChange={onOpenChange}
				title={t("shell.archivedSessions")}
				description={t("archiveModal.searchPlaceholder")}
				// Wider and taller than the palette so cards sit at least three to a
				// row. `!` because tailwind-merge does not know the palette's sizes.
				className="top-[6vh]! w-[min(1200px,calc(100vw-4rem))]!"
				commandProps={{
					shouldFilter: false,
					onValueChange: (value) => {
						// Arrowing onto the last mounted rows mounts the next page.
						const current = shownRef.current;
						const position = current.findIndex((session) => session.id === value);
						if (position >= current.length - 3) showMore();
					},
					label: t("shell.archivedSessions"),
				}}
			>
				<CommandInput
					autoFocus
					value={query}
					onValueChange={(next) => {
						setQuery(next);
						setLimit(PAGE_SIZE);
					}}
					onKeyDown={onInputKeyDown}
					placeholder={t("archiveModal.searchPlaceholder")}
					trailing={
						<CloseButton
							aria-label={t("command.close")}
							className="-my-1 -mr-1.5"
							onClick={() => onOpenChange(false)}
							// Its keys stay its own: the list would take Enter as "open the
							// selected card". Esc still closes, from the dialog's own listener.
							onKeyDown={(event) => event.stopPropagation()}
							size="icon-sm"
						/>
					}
				/>
				<CommandList
					onScroll={onListScroll}
					onPointerMove={() => {
						if (keyboardSelecting) setKeyboardSelecting(false);
					}}
					data-keyboard={keyboardSelecting || undefined}
					// The arrow-key selection looks like the board card's own hover.
					// Hover and selection switch instantly, without the card's fade.
					// Always full height, however few cards there are, so the modal
					// keeps one size while searching.
					className="[&[data-keyboard]_[cmdk-item][data-selected=true]_[data-testid=board-session-card]]:bg-interactive-hover [&_[data-testid=board-session-card]]:transition-none h-[calc(88vh-6rem)] max-h-[calc(88vh-6rem)]! px-3 pt-1 pb-3 [&_[cmdk-list-sizer]]:grid [&_[cmdk-list-sizer]]:grid-cols-[repeat(auto-fill,minmax(min(17rem,calc((100%-1rem)/3)),1fr))] [&_[cmdk-list-sizer]]:auto-rows-fr [&_[cmdk-list-sizer]]:gap-2"
				>
					<CommandEmpty className="col-span-full px-[var(--size-command-pad-x)] py-8 text-center text-control text-muted-foreground">
						{sessions.length === 0 ? t("archiveModal.empty") : t("command.noResults")}
					</CommandEmpty>
					{shown.map((session) => (
						<ArchiveCardCell
							key={session.id}
							actions={actions}
							deletable={canDelete(session)}
							restoreDisabled={!restoreAvailable}
							restoreError={restoreErrors[session.id]}
							restoring={restoringSessionId === session.id}
							session={session}
						/>
					))}
				</CommandList>
				<CommandFooter aria-hidden="true">
					<FooterHint keys={["↑", "↓"]} label={t("command.navigate")} />
					{shown.length > 0 ? <FooterHint keys={["Enter"]} label={t("command.open")} /> : null}
					{shown.length > 0 && restoreAvailable ? (
						<FooterHint keys={[MOD_KEY, "Enter"]} label={t("archiveModal.restore")} />
					) : null}
					<FooterHint keys={["Esc"]} label={t("command.close")} />
				</CommandFooter>
			</CommandDialog>
			{/* Outside the list: portals bubble React events up the React tree, and
			    the list's own Enter handling would swallow these dialogs' keys. */}
			{deleteTarget ? (
				<DeleteArchivedSessionDialog
					onOpenChange={(next) => {
						if (!next) setDeleteTarget(undefined);
					}}
					open={true}
					session={deleteTarget}
				/>
			) : null}
			{restoreUnavailableSession ? (
				<RestoreUnavailableDialog
					open={true}
					session={restoreUnavailableSession}
					hostId={hostId}
					onOpenChange={(next) => {
						if (!next) setRestoreUnavailableSession(undefined);
					}}
					onRecreated={async () => {
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
					}}
				/>
			) : null}
		</>
	);
}

type CardActions = {
	open: (session: WorkspaceSession) => void;
	restore: (session: WorkspaceSession) => void;
	requestDelete: (session: WorkspaceSession) => void;
};

/**
 * One card in the grid. Memoized on its own data, so the pointer moving,
 * typing, workspace polls and other cards' restores leave it alone.
 */
const ArchiveCardCell = memo(function ArchiveCardCell({
	actions,
	deletable,
	restoreDisabled,
	restoreError,
	restoring,
	session,
}: {
	actions: CardActions;
	deletable: boolean;
	restoreDisabled: boolean;
	restoreError?: string;
	restoring: boolean;
	session: WorkspaceSession;
}) {
	return (
		<CommandPrimitive.Item
			value={session.id}
			onSelect={() => actions.open(session)}
			data-testid="session-archive-card"
			// Every row is as tall as the tallest card: each card fills its cell,
			// with its time line pinned to the bottom.
			className="rounded-lg outline-none [&>div>div]:h-full [&_[data-testid=board-session-card]]:flex [&_[data-testid=board-session-card]]:h-full [&_[data-testid=board-session-card]]:flex-col [&_[data-testid=board-session-card]>div.grid]:mt-auto"
		>
			{/* The card opens itself on click, so clicks stop here; keys from its
			    controls and from its menus and dialogs (portals bubble through
			    React) must not reach the list either. */}
			<div className="h-full" onClick={(event) => event.stopPropagation()} onKeyDown={keepInsideCard}>
				<ArchivedSessionCardAdapter
					hidePullRequestProgress
					hideTerminatedStatus
					isRestoreDisabled={restoreDisabled}
					isRestoring={restoring}
					onDelete={deletable ? () => actions.requestDelete(session) : undefined}
					onOpen={() => actions.open(session)}
					restoreAction={(event) => {
						event.stopPropagation();
						actions.restore(session);
					}}
					restoreError={restoreError}
					session={session}
				/>
			</div>
		</CommandPrimitive.Item>
	);
});

function FooterHint({ keys, label }: { keys: readonly string[]; label: string }) {
	return (
		<span className="inline-flex shrink-0 items-center gap-1.5">
			<CommandShortcut keys={keys} variant="caps" />
			<span>{label}</span>
		</span>
	);
}

function keepInsideCard(event: SyntheticEvent<HTMLElement>) {
	const target = event.target;
	const fromPortal = !(target instanceof Node) || !event.currentTarget.contains(target);
	if (fromPortal || (target instanceof Element && target.closest("a, button"))) event.stopPropagation();
}
