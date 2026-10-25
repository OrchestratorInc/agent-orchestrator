import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Archive, Plus } from "lucide-react";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { archivedStandaloneSessions } from "../lib/standalone-archive";
import { useRestoreSession } from "../hooks/useRestoreSession";
import {
	useWorkspaceQuery,
	workspaceQueryKey,
} from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { ArchivedSessionCardAdapter } from "./SessionsBoardAdapters";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { TopbarButton } from "./TopbarButton";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";


export function StandaloneArchiveView() {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
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
	const archivedSessions = useMemo(
		() => archivedStandaloneSessions(workspaces),
		[workspaces],
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
			<PageTopbar
				lead={
					<PageTopbarTitle data-testid="standalone-archive-title">
						{t("standalone.archive.heading", { count: archivedSessions.length })}
					</PageTopbarTitle>
				}
				actions={
					<TopbarButton
						className="topbar-control--labeled"
						onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
						type="button"
						variant="action"
					>
						<Plus className="size-icon-md" aria-hidden="true" />
						{t("standalone.archive.newAgent")}
					</TopbarButton>
				}
			/>

			{workspaceQuery.isError ? (
				<p className="py-10 text-center text-xs text-passive">
					{t("shell.couldNotLoadSessions")}
				</p>
			) : archivedSessions.length === 0 && workspaceQuery.isSuccess ? (
				<StandaloneArchiveEmpty />
			) : (
				<div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
					<div
						aria-label={t("shell.archivedSessions")}
						className="standalone-archive-grid"
						data-testid="standalone-archive-grid"
						role="list"
					>
						{archivedSessions.map((session) => (
							<ArchivedSessionCardAdapter
								hideTerminatedStatus
								isRestoreDisabled={restoringSessionId !== undefined}
								isRestoring={restoringSessionId === session.id}
								key={session.id}
								restoreAction={(event) =>
									void restoreArchivedSession(event, session)
								}
								restoreError={restoreErrors[session.id]}
								session={session}
							/>
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
			</div>
		</div>
	);
}
