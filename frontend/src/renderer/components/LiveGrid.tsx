import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, GripVertical, LayoutDashboard, Maximize2, Minimize2, RefreshCw } from "lucide-react";
import {
	memo,
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type PointerEvent as ReactPointerEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { useWorkspaceQuery, useRemoteProjectQuery } from "../hooks/useWorkspaceQuery";
import { useHostConnection } from "../hooks/useHostConnection";
import { projectNavigateTarget } from "../lib/navigate-to-session";
import { useConversation } from "../hooks/useConversation";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useShell } from "../lib/shell-context";
import { SessionViewportContext, sessionViewportState } from "../lib/session-viewport";
import {
	GRID_GAP,
	GRID_MIN_HEIGHT,
	GRID_CHAT_MIN_HEIGHT,
	GRID_MIN_WIDTH,
	gridCompleted,
	gridSessions,
	gridShape,
	gridTracks,
	readGridPreferences,
	resizeGridTracks,
	writeGridPreferences,
} from "../lib/live-grid-layout";
import { getAgentActivityView } from "../lib/session-presentation";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import { agentLabel } from "../lib/agent-options";
import { hidesShellTopbar } from "../lib/platform";
import { useResolvedTheme } from "../stores/ui-store";
import { type WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { StatusPill } from "./StatusPill";
import { ShellTopbar } from "./ShellTopbar";
import { TopbarButton } from "./TopbarButton";
import { TerminalPane } from "./TerminalPane";
import { RetainedSessionChatSurface } from "./chat/SessionChatCache";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";

const EMPTY_SESSIONS: WorkspaceSession[] = [];
type Drag = { axis: "columns" | "rows"; index: number; origin: number; tracks: number[] };

export function LiveGrid({ projectId, hostId }: { projectId: string; hostId?: string }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const localQuery = useWorkspaceQuery({ enabled: !hostId });
	const remoteQuery = useRemoteProjectQuery(hostId ?? "", projectId);
	const query = hostId ? remoteQuery : localQuery;
	const project = hostId ? remoteQuery.data : localQuery.data?.find((workspace) => workspace.id === projectId);
	const { baseUrl: remoteBase, label: hostLabel } = useHostConnection(hostId);
	const sessions = project?.sessions ?? EMPTY_SESSIONS;
	const { daemonStatus } = useShell();
	const theme = useResolvedTheme();
	const [preferences, setPreferences] = useState(() => readGridPreferences(projectId, hostId));
	const [maximized, setMaximized] = useState<string>();
	const [draggingId, setDraggingId] = useState<string>();
	const [bounds, setBounds] = useState({ width: GRID_MIN_WIDTH, height: GRID_MIN_HEIGHT });
	const viewport = useRef<HTMLDivElement>(null);
	const drag = useRef<Drag | undefined>(undefined);
	const selection = useMemo(() => gridSessions(sessions, preferences), [sessions, preferences]);
	const panels = [selection.orchestrator, ...selection.visible];
	// Chat's transcript and composer need more vertical room than an xterm.
	const minimumHeight = panels.some((session) => session?.mode === "chat") ? GRID_CHAT_MIN_HEIGHT : GRID_MIN_HEIGHT;
	const shape = gridShape(panels.length, bounds.width, bounds.height, minimumHeight);
	const shapeKey = `${shape.columns}x${shape.rows}`;
	const width = Math.max(bounds.width, shape.columns * GRID_MIN_WIDTH + (shape.columns - 1) * GRID_GAP);
	const height = Math.max(bounds.height, shape.rows * minimumHeight + (shape.rows - 1) * GRID_GAP);
	const storedSplit = preferences.splits[shapeKey];
	const columns = gridTracks(
		shape.columns,
		width - (shape.columns - 1) * GRID_GAP,
		GRID_MIN_WIDTH,
		storedSplit?.columns,
	);
	const rows = gridTracks(shape.rows, height - (shape.rows - 1) * GRID_GAP, minimumHeight, storedSplit?.rows);
	const offsets = (tracks: number[]) =>
		tracks.map((_, index) => tracks.slice(0, index).reduce((sum, value) => sum + value + GRID_GAP, 0));
	const left = offsets(columns);
	const top = offsets(rows);

	useEffect(() => {
		const order = selection.workers.map((session) => session.id);
		const orchestratorId = selection.orchestrator?.id ?? preferences.orchestratorId;
		if (
			order.length === preferences.order.length &&
			order.every((id, index) => preferences.order[index] === id) &&
			orchestratorId === preferences.orchestratorId
		)
			return;
		// A loading/offline snapshot must not overwrite the persisted ordering.
		if (!project || !query.isSuccess) return;
		setPreferences((value) => ({ ...value, order, orchestratorId }));
	}, [preferences.order, preferences.orchestratorId, project, query.isSuccess, selection]);
	const preferencesRef = useRef(preferences);
	preferencesRef.current = preferences;
	useEffect(() => {
		const timer = setTimeout(() => writeGridPreferences(projectId, preferences, hostId), 150);
		return () => clearTimeout(timer);
	}, [preferences, projectId, hostId]);
	useEffect(() => () => writeGridPreferences(projectId, preferencesRef.current, hostId), [projectId, hostId]);
	useEffect(() => {
		if (maximized && !panels.some((session) => session?.id === maximized)) setMaximized(undefined);
	}, [maximized, panels]);
	useLayoutEffect(() => {
		const element = viewport.current;
		if (!element) return;
		let frame = 0;
		const measure = () => {
			cancelAnimationFrame(frame);
			frame = requestAnimationFrame(() => {
				const { width, height } = element.getBoundingClientRect();
				if (width > 0 && height > 0)
					setBounds((current) => (current.width === width && current.height === height ? current : { width, height }));
			});
		};
		const observer = new ResizeObserver(measure);
		observer.observe(element);
		measure();
		return () => {
			observer.disconnect();
			cancelAnimationFrame(frame);
		};
	}, []);
	const toggleMaximized = useCallback((id: string) => setMaximized((current) => (current === id ? undefined : id)), []);
	const reorder = useCallback((source: string, target: string) => {
		setPreferences((current) => {
			const order = [...current.order];
			const from = order.indexOf(source);
			const to = order.indexOf(target);
			if (from < 0 || to < 0 || from === to) return current;
			order.splice(from, 1);
			order.splice(to, 0, source);
			return { ...current, order };
		});
	}, []);
	const saveTracks = (axis: Drag["axis"], next: number[]) => {
		setPreferences((current) => ({
			...current,
			splits: { ...current.splits, [shapeKey]: { ...(current.splits[shapeKey] ?? { columns, rows }), [axis]: next } },
		}));
	};
	const startResize = (event: ReactPointerEvent<HTMLDivElement>, axis: Drag["axis"], index: number) => {
		if (event.button !== 0) return;
		event.preventDefault();
		event.currentTarget.setPointerCapture(event.pointerId);
		drag.current = {
			axis,
			index,
			origin: axis === "columns" ? event.clientX : event.clientY,
			tracks: axis === "columns" ? columns : rows,
		};
	};
	const resize = (event: ReactPointerEvent<HTMLDivElement>) => {
		const current = drag.current;
		if (!current) return;
		const position = current.axis === "columns" ? event.clientX : event.clientY;
		saveTracks(
			current.axis,
			resizeGridTracks(
				current.tracks,
				current.index,
				position - current.origin,
				current.axis === "columns" ? GRID_MIN_WIDTH : minimumHeight,
			),
		);
	};

	return (
		<div
			className="flex h-full min-h-0 flex-col bg-background text-foreground"
			data-testid="live-grid"
			data-project-id={projectId}
			data-host-id={hostId}
		>
			{hidesShellTopbar() ? <ShellTopbar /> : null}
			<div
				className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-2"
				style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
			>
				<strong className="mr-auto text-sm">
					{t("grid.title")} <span className="text-muted-foreground">· {panels.length}</span>
					{hostId ? <span className="ml-2 text-muted-foreground">· {hostLabel ?? hostId}</span> : null}
				</strong>
				<label className="flex items-center gap-2 text-xs">
					<input
						type="checkbox"
						checked={preferences.showCompleted}
						onChange={(event) => setPreferences((value) => ({ ...value, showCompleted: event.target.checked }))}
					/>
					{t("grid.completed")}
				</label>
				<TopbarButton
					variant="feature"
					onClick={() => void navigate(projectNavigateTarget(projectId, hostId))}
				>
					<LayoutDashboard className="size-icon-md" />
					{t("shell.openKanban")}
				</TopbarButton>
				<TopbarButton
					aria-label={t("grid.reset")}
					variant="icon"
					onClick={() => setPreferences((value) => ({ ...value, splits: {} }))}
				>
					<RefreshCw className="size-icon-md" />
				</TopbarButton>
			</div>
			{query.isError || (hostId && !remoteBase) ? (
				<p role="alert" className="px-3 py-1 text-xs text-destructive">
					{t(hostId && !remoteBase ? "remote.hostOffline" : "grid.connectionError")}{" "}
					<button type="button" onClick={() => void query.refetch()}>
						{t("grid.retry")}
					</button>
				</p>
			) : null}
			<div ref={viewport} className="relative min-h-0 flex-1">
				<div className="absolute inset-0 overflow-auto overscroll-contain [overflow-anchor:none]">
					<div
						className="relative"
						data-testid="live-grid-layout"
						data-columns={shape.columns}
						data-rows={shape.rows}
						style={{ width: maximized ? bounds.width : width, height: maximized ? bounds.height : height }}
					>
						{panels.map((session, index) => {
							const column = index % shape.columns;
							const row = Math.floor(index / shape.columns);
							const visible = !maximized || maximized === session?.id;
							return (
								<section
									key={session?.id ?? "orchestrator-unavailable"}
									data-testid="grid-panel"
									data-session-id={session?.id}
									data-role={index === 0 ? "orchestrator" : "worker"}
									aria-label={index === 0 ? t("shell.orchestrator") : session?.title}
									inert={!visible}
									style={{
										position: "absolute",
										left: maximized && visible ? 0 : left[column],
										top: maximized && visible ? 0 : top[row],
										width: maximized && visible ? bounds.width : columns[column],
										height: maximized && visible ? bounds.height : rows[row],
										visibility: visible ? "visible" : "hidden",
										contentVisibility: visible ? "visible" : "hidden",
									}}
									className={`flex min-h-0 flex-col overflow-hidden rounded-md border bg-background ${session?.activity?.state === "waiting_input" || session?.status === "needs_input" ? "border-warning ring-1 ring-warning/40" : "border-border"}`}
									onDragOver={(event) => {
										if (index > 0 && draggingId) event.preventDefault();
									}}
									onDrop={(event) => {
										if (index > 0 && session && draggingId) {
											event.preventDefault();
											reorder(draggingId, session.id);
											setDraggingId(undefined);
										}
									}}
								>
									{session ? (
										<GridPanelHeader
											session={session}
											hostId={hostId}
											orchestrator={index === 0}
											maximized={maximized === session.id}
											onToggleMaximized={toggleMaximized}
											onDragStart={() => setDraggingId(session.id)}
											onDragEnd={() => setDraggingId(undefined)}
											previous={selection.visible[index - 2]?.id}
											next={selection.visible[index]?.id}
											onReorder={reorder}
										/>
									) : (
										<header className="border-b border-border px-3 py-2 text-xs font-semibold">
											{t("shell.orchestrator")}
										</header>
									)}
									<div className="min-h-0 flex-1 overflow-hidden">
										<SessionViewportContext.Provider value={sessionViewportState(true, visible)}>
											{session ? (
												<GridSessionSurface
													session={session}
													hostId={hostId}
													assetBaseUrl={remoteBase}
													daemonReady={hostId ? Boolean(remoteBase) : daemonStatus.state === "ready"}
													theme={theme}
													visible={visible}
													maximized={maximized === session.id}
													onToggleMaximized={toggleMaximized}
												/>
											) : (
												<div className="flex h-full flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground">
													<span>{t("grid.orchestratorUnavailable")}</span>
													<TopbarButton onClick={() => void query.refetch()}>{t("grid.retry")}</TopbarButton>
												</div>
											)}
										</SessionViewportContext.Provider>
									</div>
								</section>
							);
						})}
						{!maximized
							? (["columns", "rows"] as const).flatMap((axis) =>
									(axis === "columns" ? columns : rows).slice(0, -1).map((track, index) => (
										<div
											key={`${axis}-${index}`}
											role="separator"
											tabIndex={0}
											aria-label={t(axis === "columns" ? "grid.resizeColumns" : "grid.resizeRows")}
											aria-orientation={axis === "columns" ? "vertical" : "horizontal"}
											aria-valuenow={Math.round(track)}
											aria-valuemin={axis === "columns" ? GRID_MIN_WIDTH : minimumHeight}
											className={`absolute z-10 rounded-sm bg-border/50 hover:bg-accent/60 focus-visible:bg-accent/60 focus-visible:outline-none ${axis === "columns" ? "cursor-col-resize" : "cursor-row-resize"}`}
											style={
												axis === "columns"
													? { left: left[index] + track, top: 0, width: GRID_GAP, height, touchAction: "none" }
													: { top: top[index] + track, left: 0, height: GRID_GAP, width, touchAction: "none" }
											}
											onPointerDown={(event) => startResize(event, axis, index)}
											onPointerMove={resize}
											onPointerUp={() => {
												drag.current = undefined;
											}}
											onPointerCancel={() => {
												drag.current = undefined;
											}}
											onLostPointerCapture={() => {
												drag.current = undefined;
											}}
											onKeyDown={(event) => {
												const negative = axis === "columns" ? "ArrowLeft" : "ArrowUp";
												const positive = axis === "columns" ? "ArrowRight" : "ArrowDown";
												if (event.key !== negative && event.key !== positive) return;
												event.preventDefault();
												event.stopPropagation();
												saveTracks(
													axis,
													resizeGridTracks(
														axis === "columns" ? columns : rows,
														index,
														(event.key === negative ? -1 : 1) * (event.shiftKey ? 50 : 10),
														axis === "columns" ? GRID_MIN_WIDTH : minimumHeight,
													),
												);
											}}
										/>
									)),
								)
							: null}
					</div>
				</div>
			</div>
		</div>
	);
}

const GridSessionSurface = memo(function GridSessionSurface({
	session,
	hostId,
	assetBaseUrl,
	daemonReady,
	theme,
	visible,
	maximized,
	onToggleMaximized,
}: {
	session: WorkspaceSession;
	hostId?: string;
	assetBaseUrl?: string;
	daemonReady: boolean;
	theme: "dark" | "light";
	visible: boolean;
	maximized: boolean;
	onToggleMaximized: (id: string) => void;
}) {
	const [fontSize, setFontSize] = useState(initialTerminalFontSize);
	const updateFontSize = useCallback(
		(delta: number) =>
			setFontSize((current) => {
				const next = clampTerminalFontSize(current + delta);
				try {
					localStorage.setItem(terminalFontSizeStorageKey, String(next));
				} catch {
					/* Keep zoom usable without storage. */
				}
				return next;
			}),
		[],
	);
	const toggleFullscreen = useCallback(() => onToggleMaximized(session.id), [onToggleMaximized, session.id]);
	return session.mode === "chat" ? (
		<RetainedSessionChatSurface session={session} hostId={hostId} assetBaseUrl={assetBaseUrl} daemonReady={daemonReady} theme={theme} />
	) : (
		<TerminalPane
			session={session}
			theme={theme}
			daemonReady={daemonReady}
			fontSize={fontSize}
			onChangeFontSize={updateFontSize}
			onToggleFullscreen={toggleFullscreen}
			isFullscreen={maximized}
			focusRequested={false}
			isVisible={visible}
		/>
	);
});

const GridPanelHeader = memo(function GridPanelHeader({
	session,
	hostId,
	orchestrator,
	maximized,
	onToggleMaximized,
	onDragStart,
	onDragEnd,
	previous,
	next,
	onReorder,
}: {
	session: WorkspaceSession;
	hostId?: string;
	orchestrator: boolean;
	maximized: boolean;
	onToggleMaximized: (id: string) => void;
	onDragStart: () => void;
	onDragEnd: () => void;
	previous?: string;
	next?: string;
	onReorder: (source: string, target: string) => void;
}) {
	const { t } = useTranslation();
	const activity = getAgentActivityView(session.activity, t);
	const { snapshot } = useConversation(session.mode === "chat" ? session.id : undefined, hostId);
	const restore = useRestoreSession();
	const [restoring, setRestoring] = useState(false);
	const [restoreError, setRestoreError] = useState<string>();
	const [unavailable, setUnavailable] = useState(false);
	const model = snapshot?.modelReroute?.toModel ?? snapshot?.settings.model;
	return (
		<header
			className="flex min-w-0 shrink-0 items-center gap-2 border-b border-border bg-surface px-2 py-1.5"
			data-testid="grid-panel-header"
			onDoubleClick={(event) => {
				if (!(event.target as HTMLElement).closest("button")) onToggleMaximized(session.id);
			}}
		>
			{orchestrator ? (
				<AgentAvatar decorative provider={session.provider} className="size-4 shrink-0" />
			) : (
				<button
					type="button"
					draggable={!maximized}
					aria-label={t("grid.move")}
					className="cursor-grab text-muted-foreground"
					onDragStart={onDragStart}
					onDragEnd={onDragEnd}
				>
					<GripVertical className="size-4" />
				</button>
			)}
			<div className="min-w-0 flex-1" title={session.title}>
				<strong className="block truncate text-xs">
					{orchestrator ? t("shell.orchestrator") : agentLabel(session.provider)}{" "}
					<span className="font-normal text-muted-foreground">{model ? `· ${model}` : ""}</span>
				</strong>
				<span className="block truncate text-[11px] text-muted-foreground">{session.title}</span>
			</div>
			<StatusPill
				label={gridCompleted(session) ? t("grid.done") : (session.displayStatus ?? activity.label)}
				tone={gridCompleted(session) ? "var(--fg-muted)" : activity.tone}
				breathe={!gridCompleted(session) && activity.breathe}
				className="max-w-[120px] overflow-hidden text-[10px]"
			/>
			{!orchestrator && !maximized ? (
				<>
					<button
						type="button"
						aria-label={t("grid.movePrevious")}
						disabled={!previous}
						className="text-muted-foreground disabled:opacity-25"
						onClick={() => previous && onReorder(session.id, previous)}
					>
						<ArrowLeft className="size-3" />
					</button>
					<button
						type="button"
						aria-label={t("grid.moveNext")}
						disabled={!next}
						className="text-muted-foreground disabled:opacity-25"
						onClick={() => next && onReorder(session.id, next)}
					>
						<ArrowRight className="size-3" />
					</button>
				</>
			) : null}
			{session.isTerminated || session.status === "terminated" ? (
				<button
					type="button"
					disabled={restoring}
					aria-label={t("grid.restore")}
					title={restoreError}
					onClick={async () => {
						setRestoring(true);
						setRestoreError(undefined);
						try {
							const result = await restore(session.id, hostId);
							if (result.status === "not_resumable") setUnavailable(true);
							else if (result.status === "error") setRestoreError(result.message);
						} finally {
							setRestoring(false);
						}
					}}
				>
					<RefreshCw className={`size-3 ${restoring ? "animate-spin" : ""}`} />
				</button>
			) : null}
			<button
				type="button"
				aria-label={t(maximized ? "grid.restoreSize" : "grid.maximize")}
				className="text-muted-foreground hover:text-foreground"
				onClick={() => onToggleMaximized(session.id)}
			>
				{maximized ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
			</button>
			{restoreError ? (
				<span role="alert" className="text-xs text-destructive">
					{restoreError}
				</span>
			) : null}
			<RestoreUnavailableDialog
				open={unavailable}
				session={session}
				hostId={hostId}
				onOpenChange={setUnavailable}
				onRecreated={async () => {}}
			/>
		</header>
	);
});
