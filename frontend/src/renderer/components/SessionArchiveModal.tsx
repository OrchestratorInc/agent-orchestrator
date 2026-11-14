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
const PAGE_SIZE = 24;
const IDLE_BATCH = 6;
const IDLE_LIMIT = 120;
const MAX_MOUNT_PER_MOVE = 96;
const CARD_CONTROLS = "a[href], button:not(:disabled), input:not(:disabled), [tabindex]";

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
	initialQuery?: string;
	onCloseAutoFocus?: (event: Event) => void;
	onOpenChange: (open: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
	open: boolean;
	requestId: number;
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

	useEffect(() => {
		setRestoringSessionId(undefined);
		setRestoreErrors({});
		setRestoreUnavailableSession(undefined);
		setDeleteTarget(undefined);
		restoreGenerationRef.current += 1;
	}, [requestId, resetKey]);

	useEffect(() => {
		if (open) return;
		restoreGenerationRef.current += 1;
		setRestoringSessionId(undefined);
	}, [open]);

	useEffect(() => () => {
		restoreGenerationRef.current += 1;
	}, []);

	const index = useMemo(
		() => (open ? sessions.map((session) => ({ session, text: searchText(session) })) : []),
		[open, sessions],
	);
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
	const shown = useMemo(() => (results.length > limit ? results.slice(0, limit) : results), [limit, results]);
	const shownRef = useRef(shown);
	shownRef.current = shown;
	const heldPosition = active.id === undefined ? -1 : results.findIndex((session) => session.id === active.id);
	if (heldPosition >= limit) setLimit(heldPosition + 1 + PAGE_SIZE);
	const activePosition = heldPosition >= 0 ? heldPosition : shown.length === 0 ? -1 : Math.min(active.index, shown.length - 1);
	const activeSession = activePosition >= 0 ? results[activePosition] : undefined;
	if (heldPosition < 0 && active.id !== undefined && !sessions.some((session) => session.id === active.id)) {
		setActive(activeSession ? { id: activeSession.id, index: activePosition } : { index: active.index });
	}
	const restoreAvailable = connected && restoringSessionId === undefined;
	const canDelete = (session: WorkspaceSession) =>
		!session.cloud && restoringSessionId !== session.id;
	const activeDeletable = activeSession !== undefined && restoreAvailable && canDelete(activeSession);

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

	useLayoutEffect(() => {
		if (!scrollToActiveRef.current || !activeSession) return;
		const card = document.getElementById(cellId(activeSession));
		if (!card) return;
		scrollToActiveRef.current = false;
		card.scrollIntoView({ block: "nearest", inline: "nearest" });
	});

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

	const actionsRef = useRef({ openSession, restore, requestDelete: setDeleteTarget, select: selectSession });
	actionsRef.current = { openSession, restore, requestDelete: setDeleteTarget, select: selectSession };
	const actions = useMemo<CardActions>(() => ({
		open: (session) => actionsRef.current.openSession(session),
		restore: (session) => void actionsRef.current.restore(session),
		requestDelete: (session) => actionsRef.current.requestDelete(session),
		select: (session) => actionsRef.current.select(session),
	}), []);

	const move = (key: GridNavigationKey) => {
		if (activePosition < 0) return;
		const grid = gridRef.current;
		const columns = measureGridColumns(grid);
		const pageRows = key === "PageUp" || key === "PageDown"
			? measureVisibleRows(scrollerRef.current, grid, activePosition, columns, key === "PageUp" ? -1 : 1)
			: 1;
		const target = boundedGridTarget(
			gridNavigationTarget(key, activePosition, results.length, columns, pageRows),
			shown.length,
			MAX_MOUNT_PER_MOVE,
		);
		if (target < 0) return;
		if (target >= shown.length - columns) {
			const mountLimit = mountLimitForTarget(target, shown.length, PAGE_SIZE, MAX_MOUNT_PER_MOVE);
			setLimit((current) => Math.max(current, mountLimit));
		}
		scrollToActiveRef.current = true;
		setActive({ id: results[target]?.id, index: target });
	};

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
		event.preventDefault();
		event.stopPropagation();
		const step = event.shiftKey ? -1 : 1;
		order[(position + step + order.length) % order.length]?.focus();
	};

	const onBodyKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const target = event.target;
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
			if (!plain || event.shiftKey || !(fromInput || fromCard)) return;
			event.preventDefault();
			move(event.key);
			if (fromCard) input?.focus();
			return;
		}
		if (event.key === "Enter") {
			if (!fromInput || !activeSession || event.shiftKey || event.altKey) return;
			event.preventDefault();
			if (mod) void restore(activeSession);
			else openSession(activeSession);
			return;
		}
		if (event.key === "Backspace" && mod && !event.shiftKey && !event.altKey) {
			if (!(fromInput || fromCard) || !activeSession || !activeDeletable) return;
			if (fromInput && input && (input.selectionStart !== 0 || input.selectionEnd !== 0)) return;
			event.preventDefault();
			setDeleteTarget(activeSession);
			return;
		}
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
		<div className="min-w-0" role="row">
			<div
				aria-label={session.title}
				aria-selected={active}
				className="h-full rounded-lg [&>div]:h-full"
				data-archive-session={session.id}
				data-testid="session-archive-card"
				id={id}
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
