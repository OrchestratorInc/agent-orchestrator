import { Fragment, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Archive, Folder, FolderOpen, FolderPlus, MoreVertical, Pencil, Pin, PinOff, Plus, Settings, Trash2 } from "lucide-react";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { usePinSession, useUnpinSession } from "../hooks/usePinSession";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../hooks/useSessionRename";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { formatTimeCompact, formatTimeTerse } from "../lib/format-time";
import { getSessionStatusDotView } from "../lib/session-presentation";
import { cn } from "../lib/utils";
import { newestActiveOrchestrator, openPRs, STANDALONE_WORKSPACE_ID, sortedWorkerSessions, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { OrchestratorIcon } from "./icons";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { SessionArchiveDialog } from "./SessionArchiveDialog";
import { Badge } from "./ui/badge";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "./ui/context-menu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { SidebarMenuButton, SidebarMenuItem, SidebarMenuSub, SidebarMenuSubItem } from "./ui/sidebar";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Props = {
	hosts: RemoteHost[];
	workspaces: WorkspaceSummary[];
	failedHostIds?: string[];
	loadedProjectHostIds?: string[];
	activeHostId?: string;
	activeProjectId?: string;
	activeSessionId?: string;
	onOpenSession: (hostId: string, projectId: string, sessionId: string) => void;
	onOpenProject: (hostId: string, projectId: string) => void;
	onOpenHome: () => void;
	onNewTask: (hostId: string, projectId: string) => void;
	onOrchestrator: (hostId: string, projectId: string) => void;
	onConfigure: (hostId: string, projectId: string) => void;
	onAddProject: (hostId: string) => void;
	onRemoveProject: (hostId: string, projectId: string) => Promise<void>;
	onRetry: () => void;
};

const ACTION_CLASS = "sidebar-icon-action grid size-5 shrink-0 place-items-center rounded-md !bg-transparent text-passive hover:!bg-transparent focus:!bg-transparent focus-visible:!bg-transparent active:!bg-transparent data-[state=open]:!bg-transparent hover:text-foreground disabled:pointer-events-none disabled:opacity-50 data-[state=open]:text-foreground [&_svg]:size-icon-lg";

function RemoteSessionRow({ hostId, workspace, session, active, onOpenSession, onOpenProject, onOpenHome }: {
	hostId: string;
	workspace: WorkspaceSummary;
	session: WorkspaceSession;
	active: boolean;
	onOpenSession: Props["onOpenSession"];
	onOpenProject: Props["onOpenProject"];
	onOpenHome: Props["onOpenHome"];
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const hostedSession = session.hostId === hostId ? session : { ...session, hostId };
	const rename = useSessionRename(hostedSession, () => queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) }));
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	const [confirmArchiveOpen, setConfirmArchiveOpen] = useState(false);
	const { mutate: terminateSession, isPending: isKilling } = useTerminateSession({
		onOptimistic: (killed) => {
			if (!active) return;
			const next = sortedWorkerSessions(workspace.sessions).find((item) => item.id !== killed.id && !item.isTerminated);
			if (next) onOpenSession(hostId, workspace.id, next.id);
			else if (workspace.id === STANDALONE_WORKSPACE_ID) onOpenHome();
			else onOpenProject(hostId, workspace.id);
		},
	});
	const dot = getSessionStatusDotView(session);
	const beginRename = () => rename.begin();
	const statusDot = <span aria-hidden="true" className="relative z-[1] inline-flex shrink-0 px-1.5"><span className={cn("size-2 rounded-full", dot.className, dot.breathe && "animate-status-pulse")} /></span>;
	if (rename.isEditing) return <SidebarMenuSubItem className="pl-0.5" data-session-row=""><div className="group/nav-row relative flex h-8 w-full items-center gap-1.5 rounded-lg py-0 pl-1.5 pr-1">
		<NavRowHighlight active={active} />
		{statusDot}
		<input
			aria-label={t("shell.renameSession", { title: session.title })}
			autoFocus
			className="relative z-[1] h-full min-w-0 flex-1 border-0 bg-transparent! p-0 text-sm text-foreground outline-none"
			maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
			onBlur={() => void rename.commit()}
			onChange={(event) => rename.setDraft(event.target.value)}
			onFocus={(event) => event.currentTarget.select()}
			onKeyDown={(event) => {
				if (event.key === "Enter") { event.preventDefault(); event.currentTarget.blur(); }
				else if (event.key === "Escape") { event.preventDefault(); rename.cancel(); }
			}}
			value={rename.draft}
		/>
	</div></SidebarMenuSubItem>;
	return <ContextMenu>
		<ContextMenuTrigger asChild>
		<SidebarMenuSubItem className="pl-0.5" data-session-row="">
			<div className="group/session-row group/nav-row relative flex h-8 w-full items-center rounded-lg hover:text-foreground">
				<SidebarMenuButton
					aria-current={active ? "page" : undefined}
					aria-keyshortcuts="F2"
					aria-label={t("shell.openSession", { title: session.title })}
					className={cn("h-8 min-w-0 flex-1 gap-1.5 rounded-lg pl-1.5 pr-2.5 text-left text-sm group-hover/session-row:pr-[50px] group-focus-within/session-row:pr-[50px]", session.lastUserMessageAt && "pr-[36px]", NAV_ROW_HIGHLIGHT_HOST_CLASS)}
					data-testid="remote-session-row"
					isActive={active}
					onClick={(event) => { if (event.detail < 2) onOpenSession(hostId, workspace.id, session.id); }}
					onDoubleClick={(event) => { event.preventDefault(); event.stopPropagation(); beginRename(); }}
					onKeyDown={(event) => { if (event.key === "F2") { event.preventDefault(); beginRename(); } }}
				>
					<NavRowHighlight active={active} />
					{statusDot}
					<span className="relative z-[1] min-w-0 flex-1 truncate">{session.title}</span>
				</SidebarMenuButton>
				<div className="pointer-events-none absolute inset-y-0 right-0 z-chrome flex items-center gap-px opacity-0 group-hover/session-row:pointer-events-auto group-hover/session-row:opacity-100 group-focus-within/session-row:pointer-events-auto group-focus-within/session-row:opacity-100">
					<button aria-label={session.isPinned ? t("shell.unpinSession") : t("shell.pinSession")} className={cn(ACTION_CLASS, "[&_svg]:size-3!")} onClick={() => session.isPinned ? unpinSession(hostedSession) : pinSession(hostedSession)} type="button">{session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />}</button>
					<SessionArchiveDialog
						onConfirm={() => { setConfirmArchiveOpen(false); terminateSession(hostedSession); }}
						onOpenChange={setConfirmArchiveOpen}
						open={confirmArchiveOpen}
						session={hostedSession}
						trigger={<button aria-label={t("shell.archiveSession")} className={cn(ACTION_CLASS, "[&_svg]:size-3!")} disabled={isKilling} onClick={() => setConfirmArchiveOpen(true)} type="button"><Archive aria-hidden="true" /></button>}
					/>
				</div>
				{session.lastUserMessageAt ? <time className="pointer-events-none absolute inset-y-0 right-1.5 z-[1] flex items-center text-micro tabular-nums text-passive group-hover/session-row:opacity-0 group-focus-within/session-row:opacity-0" dateTime={session.lastUserMessageAt} title={t("shell.lastMessageAt", { time: formatTimeCompact(session.lastUserMessageAt) })}>{formatTimeTerse(session.lastUserMessageAt)}</time> : null}
			</div>
		</SidebarMenuSubItem>
		</ContextMenuTrigger>
		<ContextMenuContent className="min-w-44"><ContextMenuItem aria-label={t("shell.renameSession", { title: session.title })} onSelect={beginRename}><Pencil aria-hidden="true" />{t("shell.rename")}</ContextMenuItem></ContextMenuContent>
	</ContextMenu>;
}

function RemoteProjectRow({ host, workspace, activeProjectId, activeSessionId, onOpenSession, onOpenProject, onOpenHome, onNewTask, onOrchestrator, onConfigure, onRemoveProject }: {
	host: RemoteHost;
	workspace: WorkspaceSummary;
	activeProjectId?: string;
	activeSessionId?: string;
	onOpenSession: Props["onOpenSession"];
	onOpenProject: Props["onOpenProject"];
	onOpenHome: Props["onOpenHome"];
	onNewTask: Props["onNewTask"];
	onOrchestrator: Props["onOrchestrator"];
	onConfigure: Props["onConfigure"];
	onRemoveProject: Props["onRemoveProject"];
}) {
	const { t } = useTranslation();
	const active = activeProjectId === workspace.id || (workspace.id === STANDALONE_WORKSPACE_ID && workspace.sessions.some((session) => session.id === activeSessionId));
	const [expanded, setExpanded] = useState(true);
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [isRemoving, setIsRemoving] = useState(false);
	const [removeError, setRemoveError] = useState<string | null>(null);
	const nameWithHost = `${workspace.name} · ${host.label}`;
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	const orchestratorActive = active && activeSessionId === orchestrator?.id;
	const dashboardActive = active && !activeSessionId;
	const sessions = sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true);
	const openPullRequestCount = new Set(workspace.sessions.flatMap((session) => openPRs(session).map((pr) => pr.url))).size;
	const confirmRemove = async () => {
		setConfirmOpen(false);
		setIsRemoving(true);
		setRemoveError(null);
		try {
			await onRemoveProject(host.hostId, workspace.id);
		} catch (error) {
			setRemoveError(error instanceof Error ? error.message : t("shell.couldNotRemoveProject"));
		} finally {
			setIsRemoving(false);
		}
	};
	const openProject = () => {
		if (workspace.id === STANDALONE_WORKSPACE_ID || (active && expanded && dashboardActive)) {
			setExpanded((open) => !open);
			return;
		}
		if (!expanded) setExpanded(true);
		onOpenProject(host.hostId, workspace.id);
	};
	return <SidebarMenuItem data-remote-project-row="" data-host-id={host.hostId} data-project-id={workspace.id}>
		<ContextMenu>
		<ContextMenuTrigger asChild>
		<div className="relative flex items-center">
			<SidebarMenuButton
				aria-current={active && !activeSessionId ? "page" : undefined}
				aria-expanded={workspace.id === STANDALONE_WORKSPACE_ID ? expanded : undefined}
				aria-label={workspace.id === STANDALONE_WORKSPACE_ID
					? t("shell.toggleProject", { name: nameWithHost })
					: t("shell.openProjectDashboard", { name: nameWithHost })}
				className={cn("h-9 min-w-0 flex-1 gap-2 rounded-lg px-2.5 text-sm font-medium text-muted-foreground group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:p-0! [&_svg]:size-icon-md", NAV_ROW_HIGHLIGHT_HOST_CLASS)}
				isActive={dashboardActive || orchestratorActive}
				onClick={openProject}
				tooltip={nameWithHost}
			>
				<NavRowHighlight active={active && (!activeSessionId || orchestratorActive)} />
				<span className="relative z-[1] inline-flex size-icon-md shrink-0 items-center justify-center">{expanded ? <FolderOpen aria-hidden="true" strokeWidth={1.75} /> : <Folder aria-hidden="true" strokeWidth={1.75} />}</span>
				<span className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 truncate group-data-[collapsible=icon]:hidden">{workspace.name}</span>
				<Badge variant="outline" className="sidebar-expanded-chrome relative z-[1] h-4 shrink-0 px-1.5 text-2xs group-data-[collapsible=icon]:hidden">{host.label}</Badge>
			</SidebarMenuButton>
			{workspace.id !== STANDALONE_WORKSPACE_ID && <button
				aria-expanded={expanded}
				aria-label={t("shell.toggleProject", { name: nameWithHost })}
				className="sidebar-expanded-chrome absolute inset-y-0 left-0 z-10 w-9 cursor-pointer bg-transparent group-data-[collapsible=icon]:hidden"
				data-project-folder=""
				onClick={() => setExpanded((open) => !open)}
				type="button"
			/>}
			{workspace.id !== STANDALONE_WORKSPACE_ID && <div className="sidebar-expanded-chrome flex h-control-form shrink-0 items-center gap-px pr-0.5 group-data-[collapsible=icon]:hidden">
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							aria-current={orchestratorActive ? "page" : undefined}
							aria-label={orchestrator ? t("shell.openProjectOrchestrator", { name: nameWithHost }) : t("shell.spawnProjectOrchestrator", { name: nameWithHost })}
							className={cn(ACTION_CLASS, orchestratorActive && "text-foreground")}
							disabled={isRemoving}
							onClick={() => onOrchestrator(host.hostId, workspace.id)}
							type="button"
						>
							<OrchestratorIcon aria-hidden="true" strokeWidth={orchestratorActive ? 2.5 : 2} />
						</button>
					</TooltipTrigger>
					<TooltipContent>{orchestrator ? t("shell.orchestrator") : t("shell.spawnOrchestratorLower")}</TooltipContent>
				</Tooltip>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<button
							aria-label={t("remote.projectActions", { name: workspace.name, label: host.label, defaultValue: "Project actions for {{name}} on {{label}}" })}
							className={ACTION_CLASS}
							disabled={isRemoving}
							type="button"
						>
							<MoreVertical aria-hidden="true" />
						</button>
					</DropdownMenuTrigger>
					<DropdownMenuContent side="right" align="start" className="min-w-44">
						<DropdownMenuItem disabled={isRemoving} onSelect={() => onNewTask(host.hostId, workspace.id)}>
							<Plus aria-hidden="true" />
							{t("shell.newTask")}
						</DropdownMenuItem>
						<DropdownMenuItem disabled={isRemoving} onSelect={() => onConfigure(host.hostId, workspace.id)}>
							<Settings aria-hidden="true" />
							{t("shell.projectSettings")}
						</DropdownMenuItem>
						<DropdownMenuItem
							className="text-destructive focus:text-destructive [&_svg]:text-destructive"
							disabled={isRemoving}
							onSelect={() => { setRemoveError(null); setConfirmOpen(true); }}
						>
							<Trash2 aria-hidden="true" />
							{t("shell.removeProjectTitle")}
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>}
		</div>
		</ContextMenuTrigger>
		<ContextMenuContent className="min-w-44">
			<ContextMenuItem disabled={isRemoving} onSelect={() => onNewTask(host.hostId, workspace.id)}><Plus aria-hidden="true" />{t("shell.newTask")}</ContextMenuItem>
			<ContextMenuItem disabled={isRemoving} onSelect={() => onConfigure(host.hostId, workspace.id)}><Settings aria-hidden="true" />{t("shell.projectSettings")}</ContextMenuItem>
			<ContextMenuItem className="text-destructive focus:text-destructive [&_svg]:text-destructive" disabled={isRemoving} onSelect={() => { setRemoveError(null); setConfirmOpen(true); }}><Trash2 aria-hidden="true" />{t("shell.removeProjectTitle")}</ContextMenuItem>
		</ContextMenuContent>
		</ContextMenu>
		{isRemoving ? <div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-muted-foreground" role="status">{t("shell.removingNamed", { name: workspace.name })}</div> : null}
		{removeError ? <div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-destructive" role="alert">{removeError}</div> : null}
		{expanded && sessions.length > 0 && <SidebarMenuSub className="sidebar-expanded-chrome mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0 py-1 group-data-[collapsible=icon]:hidden">
			{sessions.map((session) => <RemoteSessionRow key={session.id} hostId={host.hostId} workspace={workspace} session={session} active={active && activeSessionId === session.id} onOpenSession={onOpenSession} onOpenProject={onOpenProject} onOpenHome={onOpenHome} />)}
		</SidebarMenuSub>}
		<ConfirmDialog
			open={confirmOpen}
			onOpenChange={setConfirmOpen}
			title={t("shell.removeProjectTitle")}
			description={<>
				<p className="text-sm font-medium text-foreground">{t("remote.removeProjectLead", { name: workspace.name, label: host.label, defaultValue: "Remove {{name}} from {{label}}?" })}</p>
				<p className="mt-1 text-xs text-muted-foreground text-pretty">{t("shell.removeProjectBody")}</p>
				{openPullRequestCount > 0 ? <p className="mt-2 text-xs font-medium text-error">{t("shell.removeProjectOpenPrWarning", { count: openPullRequestCount })}</p> : null}
			</>}
			confirmLabel={t("shell.remove")}
			destructive
			onConfirm={() => { void confirmRemove(); }}
		/>
	</SidebarMenuItem>;
}

export function RemoteHostsSection({ hosts, workspaces, failedHostIds = [], loadedProjectHostIds = [], activeHostId, activeProjectId, activeSessionId, onOpenSession, onOpenProject, onOpenHome, onNewTask, onOrchestrator, onConfigure, onAddProject, onRemoveProject, onRetry }: Props) {
	const { t } = useTranslation();
	if (hosts.length === 0) return null;
	return <>
		{hosts.map((host) => {
			if (host.status !== "connected") return <SidebarMenuItem key={host.url} data-host-id={host.hostId}>
				<SidebarMenuButton
					aria-label={host.status === "offline" ? t("remote.retryHost", { label: host.label }) : undefined}
					className="h-9 gap-2 rounded-lg px-2.5 text-sm text-muted-foreground hover:bg-interactive-hover [&_svg]:size-icon-md"
					disabled={host.status === "connecting"}
					onClick={onRetry}
					title={host.status === "offline" ? t("remote.hostOffline") : undefined}
				>
					{host.status === "offline" ? <AlertTriangle aria-hidden="true" /> : <Folder aria-hidden="true" />}
					<span className="truncate">{host.label}</span>
					<span className="ml-auto text-xs">{host.status === "connecting" ? t("terminal.connecting") : host.failureReason === "unauthorized" ? t("remote.passwordRejected") : t("remote.retryHost", { label: host.label })}</span>
				</SidebarMenuButton>
			</SidebarMenuItem>;
			const projects = workspaces.filter((workspace) => workspace.hostId === host.hostId);
			const projectCount = projects.filter((workspace) => workspace.id !== STANDALONE_WORKSPACE_ID).length;
			return <Fragment key={host.url}>
				{projects.map((workspace) => <RemoteProjectRow
					key={`${host.hostId}:${workspace.id}`}
					host={host}
					workspace={workspace}
					activeProjectId={activeHostId === host.hostId ? activeProjectId : undefined}
					activeSessionId={activeHostId === host.hostId ? activeSessionId : undefined}
					onOpenSession={onOpenSession}
					onOpenProject={onOpenProject}
					onOpenHome={onOpenHome}
					onNewTask={onNewTask}
					onOrchestrator={onOrchestrator}
					onConfigure={onConfigure}
					onRemoveProject={onRemoveProject}
				/>)}
				<SidebarMenuItem>
					<SidebarMenuButton
						aria-label={t("remote.addProjectOnHost", { label: host.label, defaultValue: "Add project on {{label}}" })}
						className="h-8 gap-2 rounded-lg px-2.5 text-sm text-muted-foreground hover:bg-interactive-hover hover:text-foreground [&_svg]:size-icon-md"
						onClick={() => onAddProject(host.hostId)}
					>
						<FolderPlus aria-hidden="true" />
						<span className="truncate">{t("remote.addProjectOnHost", { label: host.label, defaultValue: "Add project on {{label}}" })}</span>
						{loadedProjectHostIds.includes(host.hostId) && <span className="ml-auto shrink-0 text-xs tabular-nums">{t("remote.projectCount", { count: projectCount, defaultValue: projectCount === 1 ? "{{count}} project" : "{{count}} projects" })}</span>}
					</SidebarMenuButton>
				</SidebarMenuItem>
				{failedHostIds.includes(host.hostId) && <SidebarMenuItem>
					<SidebarMenuButton className="h-8 rounded-lg px-2.5 text-left text-xs text-destructive hover:bg-interactive-hover" onClick={onRetry}>{t("remoteHosts.loadFailed")}</SidebarMenuButton>
				</SidebarMenuItem>}
			</Fragment>;
		})}
	</>;
}
