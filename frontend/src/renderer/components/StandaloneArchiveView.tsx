import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Archive, Plus } from "lucide-react";
import {
	STANDALONE_WORKSPACE_ID,
	workerSessions,
	type WorkspaceSession,
} from "../types/workspace";
import { useRestoreSession } from "../hooks/useRestoreSession";
import {
	useSessionUsageSummaries,
	type SessionUsageSummary,
} from "../hooks/useSessionUsageSummaries";
import {
	useWorkspaceQuery,
	workspaceQueryKey,
} from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import { isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { ArchivedSessionCardAdapter } from "./SessionsBoardAdapters";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { topbarProjectLabelClass } from "./TopbarButton";

function isArchivedSession(session: WorkspaceSession): boolean {
	return (
		session.kanbanColumn === "archive" ||
		session.isTerminated === true ||
		session.status === "terminated"
	);
}

const isMac = isMacPlatform();
const dragStyle = isMac
	? ({ WebkitAppRegion: "drag" } as React.CSSProperties)
	: undefined;
const noDragStyle = isMac
	? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties)
	: undefined;
const emptyUsageBySession: ReadonlyMap<string, SessionUsageSummary> = new Map();

export function StandaloneArchiveView() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const usageBySession =
		useSessionUsageSummaries(undefined).data ?? emptyUsageBySession;
	const restoreSessionById = useRestoreSession();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const [restoringSessionId, setRestoringSessionId] = useState<
		string | undefined
	>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>(
		{},
	);
	const [restoreUnavailableSession, setRestoreUnavailableSession] = useState<
		WorkspaceSession | undefined
	>();
	const restoreGenerationRef = useRef(0);
	const workspaces = workspaceQuery.data ?? [];
	const standaloneWorkspace = workspaces.find(
		(workspace) => workspace.id === STANDALONE_WORKSPACE_ID,
	);
	const archivedSessions = useMemo(
		() =>
			workerSessions(standaloneWorkspace?.sessions ?? [])
				.filter(isArchivedSession)
				.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)),
		[standaloneWorkspace?.sessions],
	);
	const showStartup = !workspaceQuery.isSuccess && !workspaceQuery.isError;

	useEffect(() => {
		return () => {
			restoreGenerationRef.current += 1;
		};
	}, []);

	const restoreArchivedSession = async (
		event: MouseEvent<HTMLButtonElement>,
		session: WorkspaceSession,
	) => {
		event.stopPropagation();
		if (restoringSessionId) return;
		const generation = restoreGenerationRef.current;
		const isStillMounted = () => generation === restoreGenerationRef.current;
		setRestoringSessionId(session.id);
		setRestoreErrors((current) => {
			const next = { ...current };
			delete next[session.id];
			return next;
		});
		try {
			const result = await restoreSessionById(session.id);
			if (!isStillMounted()) return;
			if (result.status === "success") {
				void navigate({
					to: "/sessions/$sessionId",
					params: { sessionId: session.id },
				});
				return;
			}
			if (result.status === "not_resumable") {
				setRestoreUnavailableSession(session);
				return;
			}
			setRestoreErrors((current) => ({
				...current,
				[session.id]: result.message,
			}));
		} finally {
			if (isStillMounted()) setRestoringSessionId(undefined);
		}
	};

	return (
		<div
			className="relative flex h-full min-h-0 flex-col bg-background text-foreground"
			data-testid="standalone-archive-view"
		>
			<div
				className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-2 border-b border-border-strong pr-2"
				style={dragStyle}
			>
				<span
					className={cn(
						topbarProjectLabelClass,
						"inline-flex items-center gap-1.5",
					)}
					data-testid="standalone-archive-title"
				>
					<Archive aria-hidden="true" className="size-icon-md" />
					{t("standalone.archive.title")}
				</span>
				<span className="rounded-full bg-surface px-2 py-0.5 text-xs text-muted-foreground">
					{t("standalone.archive.count", { count: archivedSessions.length })}
				</span>
				<div className="min-w-0 flex-1" />
				<button
					className="inline-flex h-control-md items-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
					style={noDragStyle}
					type="button"
				>
					<Plus className="size-icon-sm" aria-hidden="true" />
					{t("home.newStandaloneAgent")}
				</button>
			</div>

			{workspaceQuery.isError ? (
				<p className="py-10 text-center text-xs text-passive">
					{t("shell.couldNotLoadSessions")}
				</p>
			) : archivedSessions.length === 0 && workspaceQuery.isSuccess ? (
				<StandaloneArchiveEmpty />
			) : (
				<div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">
					<div
						className="mx-auto flex w-full max-w-3xl flex-col gap-3"
						role="list"
						aria-label={t("shell.archivedSessions")}
					>
						{archivedSessions.map((session) => (
							<div key={session.id} role="listitem">
								<ArchivedSessionCardAdapter
									isRestoreDisabled={restoringSessionId !== undefined}
									isRestoring={restoringSessionId === session.id}
									restoreAction={(event) =>
										void restoreArchivedSession(event, session)
									}
									restoreError={restoreErrors[session.id]}
									session={session}
									usage={usageBySession.get(session.id)}
								/>
							</div>
						))}
					</div>
				</div>
			)}

			{restoreUnavailableSession ? (
				<RestoreUnavailableDialog
					open={true}
					session={restoreUnavailableSession}
					onOpenChange={(open) => {
						if (!open) setRestoreUnavailableSession(undefined);
					}}
					onRecreated={async () => {
						await queryClient.invalidateQueries({
							queryKey: workspaceQueryKey,
						});
					}}
				/>
			) : null}
			{showStartup ? <DaemonStartupLoader /> : null}
		</div>
	);
}

function StandaloneArchiveEmpty() {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	return (
		<div
			className="flex h-full min-h-0 items-center justify-center overflow-y-auto"
			data-testid="standalone-archive-empty"
		>
			<div className="flex w-full max-w-preview-content flex-col items-center px-6 pb-empty-offset-y text-center">
				<div className="mb-4 grid size-12 place-items-center rounded-full border border-border bg-surface text-muted-foreground">
					<Archive className="size-icon-lg" aria-hidden="true" />
				</div>
				<h2 className="text-subtitle font-semibold tracking-tight text-foreground">
					{t("standalone.archive.empty.title")}
				</h2>
				<p className="mt-2 text-md-sm leading-relaxed text-muted-foreground">
					{t("standalone.archive.empty.body")}
				</p>
				<button
					className="mt-5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
					type="button"
				>
					{t("home.newStandaloneAgent")}
				</button>
			</div>
		</div>
	);
}
