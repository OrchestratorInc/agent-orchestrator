import {
	memo,
	startTransition,
	useDeferredValue,
	useEffect,
	useId,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type KeyboardEvent,
	type MouseEvent,
	type PointerEvent,
	type UIEvent,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Search } from "@/components/app-icons";
import type { WorkspaceSession } from "../types/workspace";
import { agentLabel } from "../lib/agent-options";
import {
	boundedGridTarget,
	gridNavigationTarget,
	isGridNavigationKey,
	measureGridColumns,
	measureVisibleRows,
	mountLimitForTarget,
	type GridNavigationKey,
} from "../lib/grid-navigation";
import { sessionBranchPresentation } from "../lib/session-branch";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { isMacPlatform } from "../lib/platform";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { CloseButton } from "./ui/close-button";
import { ArchivedSessionCardAdapter, DeleteArchivedSessionDialog } from "./SessionsBoardAdapters";
import { CommandBar, CommandDialog, CommandFooter, CommandShortcut, commandInputClassName } from "./ui/command";

const isMac = isMacPlatform();
const MOD_KEY = isMac ? "⌘" : "Ctrl";
const BACKSPACE_KEY = isMac ? "⌫" : "Backspace";
/** Cards mounted when the modal opens or the search changes. */
const PAGE_SIZE = 24;
/**
 * The rest mount a few at a time while the page is idle, up to this many, so
 * scrolling never waits on a mount. Past it, scrolling or arrowing near the end
 * mounts the next page.
 */
const IDLE_BATCH = 6;
const IDLE_LIMIT = 120;
/**
 * The most cards one key press may mount past those already mounted. End in a
 * huge archive stops on the furthest card within it, and pressing End again
 * goes on, so no single update mounts the whole archive.
 */
const MAX_MOUNT_PER_MOVE = 96;
/** What Tab can reach inside a card: its own buttons and links. */
const CARD_CONTROLS = "a[href], button:not(:disabled), input:not(:disabled), [tabindex]";

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
 * The archive: a searchable grid of ended-session cards in the Cmd+K surface,
 * opened over whatever page is showing (see ArchiveModalHost). Sessions arrive
 * newest first. Restore state lives here, and in-flight restores are dropped
 * when the scope changes, the modal closes or this unmounts, so a late
 * completion cannot navigate after the user left.
 *
 * Keys: the search field keeps focus while the arrows move one selection
 * through the grid as it is laid out (Left/Right one card, Up/Down one row in
 * the same column), Enter opens, Mod+Enter restores, Mod+Backspace asks to
 * delete, and Tab walks the selected card's own buttons and links. The field
 * is a combobox pointing at the selected card (aria-activedescendant).
 */
export function SessionArchiveModal({
	connected,
	hostId,
	initialQuery,
	onCloseAutoFocus,
	onOpenChange,
	onOpenSession,
	open,
	requestId,
	resetKey,
	sessions,
}: {
	connected: boolean;
	hostId?: string;
	/** The search each request starts from. */
	initialQuery?: string;
	onCloseAutoFocus?: (event: Event) => void;
	onOpenChange: (open: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
	open: boolean;
	/** Changes with every request to open, so each starts from its own search. */
	requestId: number;
	/** The scope; a change, like a new request, drops restore state. */
	resetKey: string;
	sessions: WorkspaceSession[];
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	const baseId = useId();
	const listboxId = `${baseId}-cards`;
	const cellId = (session: WorkspaceSession) => `${baseId}-card-${session.id}`;
	const [query, setQuery] = useState("");
	const deferredQuery = useDeferredValue(query);
	// One selection, drawn on one card: the arrow keys move it, and so does the
	// pointer, but only when it actually moves (a card scrolling under a still
	// pointer does not take it).
	// Held by session, so a poll that adds or drops cards elsewhere leaves it on
	// the same card; by position only when that card is gone (deleted), so it
	// stays in the same place.
	const [active, setActive] = useState<{ id?: string; index: number }>({ index: 0 });
	const [limit, setLimit] = useState(PAGE_SIZE);
	const [restoringSessionId, setRestoringSessionId] = useState<string | undefined>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>({});
	const [restoreUnavailableSession, setRestoreUnavailableSession] = useState<WorkspaceSession | undefined>();
	const [deleteTarget, setDeleteTarget] = useState<WorkspaceSession | undefined>();
	const restoreGenerationRef = useRef(0);
	const resetKeyRef = useRef(resetKey);
	resetKeyRef.current = resetKey;
	const bodyRef = useRef<HTMLDivElement>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	const closeRef = useRef<HTMLButtonElement>(null);
	const scrollerRef = useRef<HTMLDivElement>(null);
	const gridRef = useRef<HTMLDivElement>(null);
	const scrollToActiveRef = useRef(false);
	const pointerRef = useRef<{ x: number; y: number } | null>(null);

	// Each request starts from its own search (usually empty) at the newest
	// session, including one that retargets the modal while it is open.
	const [seenRequest, setSeenRequest] = useState<number | null>(null);
	const currentRequest = open ? requestId : null;
	if (currentRequest !== seenRequest) {
		setSeenRequest(currentRequest);
		if (currentRequest !== null) {
			setQuery(initialQuery ?? "");
			setLimit(PAGE_SIZE);
			setActive({ index: 0 });
		}
	}

	// A new scope or a new request (even for the same project) starts clean: a
	// restore begun under the last one must not close or navigate this one.
	useEffect(() => {
		setRestoringSessionId(undefined);
		setRestoreErrors({});
		setRestoreUnavailableSession(undefined);
		setDeleteTarget(undefined);
		restoreGenerationRef.current += 1;
	}, [requestId, resetKey]);

	// Closed, the modal no longer stands for where the user is: a restore that
	// finishes later must not take them to its session.
	useEffect(() => {
		if (open) return;
		restoreGenerationRef.current += 1;
		setRestoringSessionId(undefined);
	}, [open]);

	useEffect(() => () => {
		restoreGenerationRef.current += 1;
	}, []);

	// Built only while open: the board re-renders on every workspace poll.
	const index = useMemo(
		() => (open ? sessions.map((session) => ({ session, text: searchText(session) })) : []),
		[open, sessions],
	);
	// A new search mounts its first page again, but only once its results
	// arrive: resetting on the keystroke itself would unmount the mounted cards
	// in the urgent render and then remount them a few at a time.
	const [pagedQuery, setPagedQuery] = useState(deferredQuery);
	if (pagedQuery !== deferredQuery) {
		setPagedQuery(deferredQuery);
		setLimit(PAGE_SIZE);
	}
	const results = useMemo(() => {
		const tokens = deferredQuery.trim().toLowerCase().split(/\s+/).filter(Boolean);
		if (tokens.length === 0) return sessions;
		return index.filter((entry) => tokens.every((token) => entry.text.includes(token))).map((entry) => entry.session);
	}, [deferredQuery, index, sessions]);
	// The first page renders with the modal, so it never opens empty.
	const shown = useMemo(() => (results.length > limit ? results.slice(0, limit) : results), [limit, results]);
	const shownRef = useRef(shown);
	shownRef.current = shown;
	// Looked up in all results, not just the mounted ones: a poll that adds
	// sessions above it can push the selected card past the mounted window, and
	// it must stay selected (and get mounted), not hand the selection to
	// whichever card took its place.
	const heldPosition = active.id === undefined ? -1 : results.findIndex((session) => session.id === active.id);
	if (heldPosition >= limit) setLimit(heldPosition + 1 + PAGE_SIZE);
	// An index into `results`; mounted, as `shown` is their first `limit`.
	const activePosition = heldPosition >= 0 ? heldPosition : shown.length === 0 ? -1 : Math.min(active.index, shown.length - 1);
	const activeSession = activePosition >= 0 ? results[activePosition] : undefined;
	// Once the held session is gone from the archive (deleted), the card that
	// took its place becomes the held one, so a later poll that adds sessions
	// above it cannot move the selection. A session the search only hides is
	// still held, and is selected again when the search clears.
	if (heldPosition < 0 && active.id !== undefined && !sessions.some((session) => session.id === active.id)) {
		setActive(activeSession ? { id: activeSession.id, index: activePosition } : { index: active.index });
	}
	const restoreAvailable = connected && restoringSessionId === undefined;
	const canDelete = (session: WorkspaceSession) =>
		// The control plane owns a cloud session's record; this daemon cannot delete it.
		!session.cloud && restoringSessionId !== session.id;
	const activeDeletable = activeSession !== undefined && restoreAvailable && canDelete(activeSession);

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

	// A key that moved the selection brings its card into view, the least
	// distance and at once (no smooth scroll to wait out between presses).
	useLayoutEffect(() => {
		if (!scrollToActiveRef.current || !activeSession) return;
		const card = document.getElementById(cellId(activeSession));
		if (!card) return;
		scrollToActiveRef.current = false;
		card.scrollIntoView({ block: "nearest", inline: "nearest" });
	});

	// A card that held focus can go away (deleted, or filtered out by a poll);
	// focus then falls to the page or the dialog itself, and typing would stop
	// reaching the search. Hand it back to the field.
	useEffect(() => {
		if (!open || deleteTarget || restoreUnavailableSession) return;
		const body = bodyRef.current;
		const focused = document.activeElement;
		if (!body) return;
		if (!focused || focused === document.body || (focused !== body && focused.contains(body))) inputRef.current?.focus();
	}, [deleteTarget, open, restoreUnavailableSession, shown]);

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

	const selectSession = (session: WorkspaceSession) => {
		const position = shownRef.current.findIndex((candidate) => candidate.id === session.id);
		if (position >= 0) setActive({ id: session.id, index: position });
	};

	// Cells get one stable set of callbacks that always reach the latest state,
	// so a card re-renders only when its own data or its selection changes.
	const actionsRef = useRef({ openSession, restore, requestDelete: setDeleteTarget, select: selectSession });
	actionsRef.current = { openSession, restore, requestDelete: setDeleteTarget, select: selectSession };
	const actions = useMemo<CardActions>(() => ({
		open: (session) => actionsRef.current.openSession(session),
		restore: (session) => void actionsRef.current.restore(session),
		requestDelete: (session) => actionsRef.current.requestDelete(session),
		select: (session) => actionsRef.current.select(session),
	}), []);

	/** Moves the selection through all results, mounting what the move reaches. */
	const move = (key: GridNavigationKey) => {
		if (activePosition < 0) return;
		const grid = gridRef.current;
		const columns = measureGridColumns(grid);
		// Only a page key needs the rows' laid-out heights.
		const pageRows = key === "PageUp" || key === "PageDown"
			? measureVisibleRows(scrollerRef.current, grid, activePosition, columns, key === "PageUp" ? -1 : 1)
			: 1;
		const target = boundedGridTarget(
			gridNavigationTarget(key, activePosition, results.length, columns, pageRows),
			shown.length,
			MAX_MOUNT_PER_MOVE,
		);
		if (target < 0) return;
		// Onto or near the last mounted row: mount up to the target and a page
		// past it in this same render, so the selection never waits for a card;
		// the page past it fits the same per-press budget.
		if (target >= shown.length - columns) {
			const mountLimit = mountLimitForTarget(target, shown.length, PAGE_SIZE, MAX_MOUNT_PER_MOVE);
			setLimit((current) => Math.max(current, mountLimit));
		}
		scrollToActiveRef.current = true;
		setActive({ id: results[target]?.id, index: target });
	};

	/**
	 * Tab goes from the search into the selected card's controls (restore,
	 * delete, copy branch, PR links), on to the close button and back to the
	 * search; Shift+Tab walks it backwards.
	 */
	const cycleFocus = (event: KeyboardEvent<HTMLDivElement>, target: HTMLElement) => {
		const input = inputRef.current;
		const close = closeRef.current;
		if (!input) return;
		const card = target.closest<HTMLElement>("[data-archive-session]")
			?? (activeSession ? document.getElementById(cellId(activeSession)) : null);
		const controls = card
			? Array.from(card.querySelectorAll<HTMLElement>(CARD_CONTROLS)).filter((element) => element.tabIndex >= 0)
			: [];
		const order = [input, ...controls, ...(close ? [close] : [])];
		const position = order.indexOf(target);
		if (position < 0) return;
		// The dialog's focus trap also handles Tab, ignoring preventDefault: on
		// the dialog's last (or first) tabbable element it would wrap focus away
		// from where this put it.
		event.preventDefault();
		event.stopPropagation();
		const step = event.shiftKey ? -1 : 1;
		order[(position + step + order.length) % order.length]?.focus();
	};

	const onBodyKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const target = event.target;
		// A card's menus and dialogs portal out but bubble here through React;
		// their keys are their own.
		if (!(target instanceof HTMLElement) || !event.currentTarget.contains(target)) return;
		if (event.nativeEvent.isComposing || event.keyCode === 229) return;
		const input = inputRef.current;
		const fromInput = target === input;
		const fromCard = !fromInput && Boolean(gridRef.current?.contains(target));
		const mod = isMac ? event.metaKey : event.ctrlKey;
		const plain = !event.altKey && !event.ctrlKey && !event.metaKey;

		if (event.key === "Tab") {
			if (plain) cycleFocus(event, target);
			return;
		}
		if (isGridNavigationKey(event.key)) {
			// Modified arrows and Home/End stay the field's text editing.
			if (!plain || event.shiftKey || !(fromInput || fromCard)) return;
			event.preventDefault();
			move(event.key);
			if (fromCard) input?.focus();
			return;
		}
		if (event.key === "Enter") {
			// On a card's own button, Enter is that button's.
			if (!fromInput || !activeSession || event.shiftKey || event.altKey) return;
			event.preventDefault();
			if (mod) void restore(activeSession);
			else openSession(activeSession);
			return;
		}
		if (event.key === "Backspace" && mod && !event.shiftKey && !event.altKey) {
			if (!(fromInput || fromCard) || !activeSession || !activeDeletable) return;
			// In the search it is still text editing while there is text before
			// the caret to delete; it asks to delete the card only from there on.
			if (fromInput && input && (input.selectionStart !== 0 || input.selectionEnd !== 0)) return;
			event.preventDefault();
			setDeleteTarget(activeSession);
			return;
		}
		// Typing while a card's control has focus goes back to the search.
		if (fromCard && plain && event.key.length === 1 && event.key !== " ") input?.focus();
	};

	const onGridPointerMove = (event: PointerEvent<HTMLDivElement>) => {
		const last = pointerRef.current;
		if (last && last.x === event.clientX && last.y === event.clientY) return;
		pointerRef.current = { x: event.clientX, y: event.clientY };
		if (!(event.target instanceof Element) || !event.currentTarget.contains(event.target)) return;
		const sessionId = event.target.closest<HTMLElement>("[data-archive-session]")?.dataset.archiveSession;
		if (!sessionId || sessionId === activeSession?.id) return;
		const position = shown.findIndex((session) => session.id === sessionId);
		if (position >= 0) setActive({ id: sessionId, index: position });
	};

	// A press on a card's body (not its controls) keeps focus in the search, so
	// typing still filters after a click that does not open anything.
	const onGridMouseDown = (event: MouseEvent<HTMLDivElement>) => {
		if (event.button !== 0 || !(event.target instanceof Element) || !event.currentTarget.contains(event.target)) return;
		if (event.target.closest("a, button, input")) return;
		event.preventDefault();
	};

	const onListScroll = (event: UIEvent<HTMLDivElement>) => {
		const list = event.currentTarget;
		if (list.scrollTop + list.clientHeight >= list.scrollHeight - 240) showMore();
	};

	return (
		<>
			<CommandDialog
				plain
				open={open}
				onOpenChange={onOpenChange}
				title={t("shell.archivedSessions")}
				description={t("archiveModal.searchPlaceholder")}
				// Wider and taller than the palette so cards sit at least three to a
				// row. `!` because tailwind-merge does not know the palette's sizes.
				className="top-[6vh]! w-[min(1200px,calc(100vw-4rem))]!"
				contentProps={onCloseAutoFocus ? { onCloseAutoFocus } : undefined}
			>
				<div className="contents" onKeyDown={onBodyKeyDown} ref={bodyRef}>
					<CommandBar>
						<Search className="size-icon-base shrink-0 text-muted-foreground" aria-hidden="true" />
						<input
							aria-activedescendant={activeSession ? cellId(activeSession) : undefined}
							aria-autocomplete="list"
							aria-controls={listboxId}
							aria-expanded={true}
							aria-haspopup="grid"
							aria-label={t("archiveModal.searchPlaceholder")}
							autoComplete="off"
							autoCorrect="off"
							autoFocus
							className={commandInputClassName}
							data-slot="command-input"
							onChange={(event) => {
								setQuery(event.target.value);
								// A new search starts at its first result.
								setActive({ index: 0 });
								scrollToActiveRef.current = true;
							}}
							placeholder={t("archiveModal.searchPlaceholder")}
							ref={inputRef}
							role="combobox"
							spellCheck={false}
							type="text"
							value={query}
						/>
						<CloseButton
							aria-label={t("command.close")}
							className="-my-1 -mr-1.5"
							onClick={() => onOpenChange(false)}
							ref={closeRef}
							size="icon-sm"
						/>
					</CommandBar>
					<div
						className="h-[calc(88vh-6rem)] max-h-[calc(88vh-6rem)] scrollbar-none scroll-py-1.5 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pt-1 pb-3 outline-none"
						data-slot="command-list"
						onScroll={onListScroll}
						ref={scrollerRef}
					>
						{shown.length === 0 ? (
							<div className="px-[var(--size-command-pad-x)] py-8 text-center text-control text-muted-foreground" role="status">
								{sessions.length === 0 ? t("archiveModal.empty") : t("command.noResults")}
							</div>
						) : null}
						{/* Always full height, however few cards there are, so the modal
						    keeps one size while searching. A row is as tall as its tallest
						    card, and every card in it fills that height. */}
						<div
							aria-label={t("shell.archivedSessions")}
							className="grid grid-cols-[repeat(auto-fill,minmax(min(17rem,calc((100%-1rem)/3)),1fr))] gap-2"
							id={listboxId}
							onMouseDown={onGridMouseDown}
							onPointerMove={onGridPointerMove}
							ref={gridRef}
							role="grid"
						>
							{shown.map((session) => (
								<ArchiveCardCell
									key={session.id}
									actions={actions}
									active={session.id === activeSession?.id}
									deletable={canDelete(session)}
									id={cellId(session)}
									restoreDisabled={!restoreAvailable}
									restoreError={restoreErrors[session.id]}
									restoring={restoringSessionId === session.id}
									session={session}
								/>
							))}
						</div>
					</div>
					<CommandFooter aria-hidden="true">
						<FooterHint keys={["←↑↓→"]} label={t("command.navigate")} />
						{activeSession ? <FooterHint keys={["Enter"]} label={t("command.open")} /> : null}
						{activeSession && restoreAvailable ? (
							<FooterHint keys={[MOD_KEY, "Enter"]} label={t("archiveModal.restore")} />
						) : null}
						{activeDeletable ? <FooterHint keys={[MOD_KEY, BACKSPACE_KEY]} label={t("deleteSession.confirm")} /> : null}
						<FooterHint keys={["Esc"]} label={t("command.close")} />
					</CommandFooter>
				</div>
			</CommandDialog>
			{/* Outside the grid: portals bubble React events up the React tree, and
			    these dialogs' keys must not reach the grid's handler. */}
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
	select: (session: WorkspaceSession) => void;
};

/**
 * One card in the grid. Memoized on its own data and selection, so the
 * selection moving re-renders two cards, and typing, workspace polls and other
 * cards' restores leave it alone.
 */
const ArchiveCardCell = memo(function ArchiveCardCell({
	actions,
	active,
	deletable,
	id,
	restoreDisabled,
	restoreError,
	restoring,
	session,
}: {
	actions: CardActions;
	active: boolean;
	deletable: boolean;
	id: string;
	restoreDisabled: boolean;
	restoreError?: string;
	restoring: boolean;
	session: WorkspaceSession;
}) {
	return (
		// A grid, not a listbox: each card holds its own buttons and links. Each
		// card is a row of one cell, so a column change never regroups (and
		// remounts) cards; the arrow keys follow the visual columns regardless.
		<div className="min-w-0" role="row">
			<div
				aria-label={session.title}
				aria-selected={active}
				// The card, inside its context-menu trigger, fills the row's height.
				className="h-full rounded-lg [&>div]:h-full"
				data-archive-session={session.id}
				data-testid="session-archive-card"
				id={id}
				// Tabbing onto one of its controls selects the card, so the keys and
				// the highlight follow focus.
				onFocus={() => actions.select(session)}
				role="gridcell"
			>
				<ArchivedSessionCardAdapter
					compact
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
					selected={active}
					session={session}
				/>
			</div>
		</div>
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
