import { useDeferredValue, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type UIEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
	GitMerge,
	GitPullRequest,
	GitPullRequestClosed,
	GitPullRequestDraft,
	LoaderCircle,
	RotateCcw,
	Trash2,
} from "@/components/app-icons";
import type { PRState, WorkspaceSession } from "../types/workspace";
import { agentLabel } from "../lib/agent-options";
import { formatTimeCompact } from "../lib/format-time";
import { sessionBranchPresentation } from "../lib/session-branch";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { AgentAvatar } from "./AgentAvatar";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { DeleteArchivedSessionDialog } from "./SessionsBoardAdapters";
import { Button } from "./ui/button";
import {
	CommandDialog,
	CommandEmpty,
	CommandFooter,
	CommandInput,
	CommandItem,
	CommandList,
	CommandShortcut,
} from "./ui/command";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const isMac = isMacPlatform();
const MOD_KEY = isMac ? "⌘" : "Ctrl";
/** Rows mounted at a time; scrolling or arrowing near the end mounts the next page. */
const PAGE_SIZE = 60;

const PR_STATE_ICON = {
	closed: GitPullRequestClosed,
	draft: GitPullRequestDraft,
	merged: GitMerge,
	open: GitPullRequest,
} as const satisfies Record<PRState, unknown>;

const PR_STATE_LABEL = {
	closed: "pr.state.closed",
	draft: "pr.state.draft",
	merged: "pr.state.merged",
	open: "pr.state.open",
} as const satisfies Record<PRState, string>;

const PR_STATE_CLASS: Record<PRState, string> = {
	closed: "text-error!",
	draft: "text-passive!",
	merged: "text-status-merged!",
	open: "text-success!",
};

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
 * The board's archive: a searchable list of ended sessions in the Cmd+K
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
	showProject,
}: {
	connected: boolean;
	hostId?: string;
	onOpenChange: (open: boolean) => void;
	onOpenSession: (session: WorkspaceSession) => void;
	open: boolean;
	/** The board scope; a change drops restore state. */
	resetKey: string;
	sessions: WorkspaceSession[];
	/** Name each row's project, for a board that spans projects. */
	showProject: boolean;
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const restoreSessionById = useRestoreSession();
	const [query, setQuery] = useState("");
	const deferredQuery = useDeferredValue(query);
	const [selected, setSelected] = useState("");
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
			setSelected("");
			setLimit(PAGE_SIZE);
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
	const shown = results.length > limit ? results.slice(0, limit) : results;
	// cmdk highlights the first row until the reader moves, and again after a
	// filter drops the selected one.
	const selectedSession = shown.find((session) => session.id === selected) ?? shown[0];
	const restoreAvailable = connected && restoringSessionId === undefined;
	const canDelete = (session: WorkspaceSession) =>
		// The control plane owns a cloud session's record; this daemon cannot delete it.
		!session.cloud && restoringSessionId !== session.id;

	const showMore = () => setLimit((current) => (current < results.length ? current + PAGE_SIZE : current));

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

	const onInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.nativeEvent.isComposing || !selectedSession) return;
		const mod = isMac ? event.metaKey : event.ctrlKey;
		if (!mod) return;
		if (event.key === "Enter") {
			event.preventDefault();
			event.stopPropagation();
			void restore(selectedSession);
		} else if (event.key === "Backspace" && canDelete(selectedSession)) {
			event.preventDefault();
			event.stopPropagation();
			setDeleteTarget(selectedSession);
		}
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
				commandProps={{
					shouldFilter: false,
					value: selected,
					onValueChange: (value) => {
						setSelected(value);
						// Arrowing onto the last mounted rows mounts the next page.
						const position = shown.findIndex((session) => session.id === value);
						if (position >= shown.length - 3) showMore();
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
						// A new query selects its own first row.
						setSelected("");
					}}
					onKeyDown={onInputKeyDown}
					placeholder={t("archiveModal.searchPlaceholder")}
				/>
				<CommandList onScroll={onListScroll}>
					<CommandEmpty>{sessions.length === 0 ? t("archiveModal.empty") : t("command.noResults")}</CommandEmpty>
					{shown.map((session) => (
						<ArchiveRow
							key={session.id}
							canDelete={canDelete(session)}
							canRestore={restoreAvailable}
							error={restoreErrors[session.id]}
							onDelete={() => setDeleteTarget(session)}
							onOpen={() => openSession(session)}
							onRestore={() => void restore(session)}
							restoring={restoringSessionId === session.id}
							session={session}
							showProject={showProject}
						/>
					))}
				</CommandList>
				<CommandFooter aria-hidden="true">
					<FooterHint keys={["↑", "↓"]} label={t("command.navigate")} />
					{selectedSession ? <FooterHint keys={["Enter"]} label={t("command.open")} /> : null}
					{selectedSession && restoreAvailable ? (
						<FooterHint keys={[MOD_KEY, "Enter"]} label={t("archiveModal.restore")} />
					) : null}
					{selectedSession && canDelete(selectedSession) ? (
						<FooterHint keys={[MOD_KEY, "⌫"]} label={t("archiveModal.delete")} />
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

function FooterHint({ keys, label }: { keys: readonly string[]; label: string }) {
	return (
		<span className="inline-flex shrink-0 items-center gap-1.5">
			<CommandShortcut keys={keys} variant="caps" />
			<span>{label}</span>
		</span>
	);
}

/**
 * One archived session: harness, title, a quiet line with branch, PRs and
 * project, and the time it ended. Restore and delete show on hover or when the
 * row is selected; the keyboard reaches them through ⌘↵ and ⌘⌫.
 */
function ArchiveRow({
	canDelete,
	canRestore,
	error,
	onDelete,
	onOpen,
	onRestore,
	restoring,
	session,
	showProject,
}: {
	canDelete: boolean;
	canRestore: boolean;
	error?: string;
	onDelete: () => void;
	onOpen: () => void;
	onRestore: () => void;
	restoring: boolean;
	session: WorkspaceSession;
	showProject: boolean;
}) {
	const { t } = useTranslation();
	const branch = sessionBranchPresentation(session).branch;
	const meta = branch || session.prs.length > 0 || showProject;
	return (
		<CommandItem value={session.id} onSelect={onOpen} data-testid="session-archive-row">
			<AgentAvatar provider={session.provider} className="size-icon-base" />
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="truncate">{session.title}</span>
				{error ? (
					<span className="truncate text-caption text-destructive" role="alert">{error}</span>
				) : meta ? (
					<span className="flex min-w-0 items-center gap-2 text-caption text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">
						{branch ? <span className="min-w-0 truncate font-mono">{branch}</span> : null}
						{session.prs.map((pr) => {
							const Icon = PR_STATE_ICON[pr.state];
							return (
								<span key={pr.url || pr.number} className="inline-flex shrink-0 items-center gap-1 tabular-nums">
									<Icon className={cn("size-icon-2xs!", PR_STATE_CLASS[pr.state])} aria-hidden="true" />
									#{pr.number}
									<span className="sr-only">{t(PR_STATE_LABEL[pr.state])}</span>
								</span>
							);
						})}
						{showProject ? <span className="min-w-0 shrink truncate">{session.workspaceName}</span> : null}
					</span>
				) : null}
			</span>
			<span
				className={cn(
					"flex shrink-0 items-center gap-0.5",
					restoring
						? "opacity-100"
						: "opacity-0 group-hover/command-item:opacity-100 group-data-[selected=true]/command-item:opacity-100",
				)}
			>
				<RowAction
					disabled={!canRestore}
					label={t("shell.restoreNamed", { title: session.title })}
					onClick={onRestore}
					shortcut="Meta+Enter"
					tooltip={restoring ? t("shell.restoringSession") : t("shell.restoreSession")}
					variant="quiet"
				>
					{restoring ? (
						<LoaderCircle className="size-icon-md! animate-spin text-current!" aria-hidden="true" />
					) : (
						<RotateCcw className="size-icon-md! text-current!" aria-hidden="true" />
					)}
				</RowAction>
				{session.cloud ? null : (
					<RowAction
						disabled={!canDelete}
						label={t("shell.deleteSessionNamed", { title: session.title })}
						onClick={onDelete}
						shortcut="Meta+Backspace"
						tooltip={t("shell.deleteSession")}
						variant="destructive-ghost"
					>
						<Trash2 className="size-icon-md! text-current!" aria-hidden="true" />
					</RowAction>
				)}
			</span>
			<span className="min-w-14 shrink-0 text-right text-caption tabular-nums text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75">
				{formatTimeCompact(session.updatedAt)}
			</span>
		</CommandItem>
	);
}

function RowAction({
	children,
	disabled,
	label,
	onClick,
	shortcut,
	tooltip,
	variant,
}: {
	children: ReactNode;
	disabled: boolean;
	label: string;
	onClick: () => void;
	shortcut: string;
	tooltip: string;
	variant: "quiet" | "destructive-ghost";
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex">
					<Button
						aria-keyshortcuts={shortcut}
						aria-label={label}
						disabled={disabled}
						// The search field keeps focus; the row's keys reach these actions.
						tabIndex={-1}
						onClick={(event) => {
							// The row opens the session on click; this action is not that.
							event.stopPropagation();
							onClick();
						}}
						size="icon-xs"
						type="button"
						variant={variant}
					>
						{children}
					</Button>
				</span>
			</TooltipTrigger>
			<TooltipContent side="top">{tooltip}</TooltipContent>
		</Tooltip>
	);
}
