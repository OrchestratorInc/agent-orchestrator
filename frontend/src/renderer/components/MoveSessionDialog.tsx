import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Check, CircleAlert, Loader2 } from "@/components/app-icons";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { cloudSessionsQueryKey, useCloudProjectsQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { CLOUD_AGENT_PROVIDERS } from "../lib/cloud-agents";
import { clientForSessionHost, labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { MoveError, moveSession, type MoveResult, type MoveStep, type MoveTarget } from "../lib/move-session";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { repoKey } from "../lib/repo-key";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

/** The session the Move to AO Cloud dialog is open for, mounted once in the shell. */
export function MoveSessionDialog() {
	const session = useUiStore((state) => state.moveSessionTarget);
	const close = useUiStore((state) => state.closeMoveSession);
	if (!session) return null;
	return <MoveSessionDialogBody key={session.id} session={session} onClose={close} />;
}

/** The session-move feature AO Cloud advertises once createSession takes a branch. */
const CLOUD_BRANCH_FEATURE = "session.branch";

const STEPS: MoveStep[] = ["stop", "export", "import", "complete"];

function MoveSessionDialogBody({ session, onClose }: { session: WorkspaceSession; onClose: () => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const { client: cloudClient } = useCloudCp();
	const { org } = useCloudOrg();
	const offering = useCloudOffering();
	const cloudProjects = useCloudProjectsQuery();
	const sourceHost: HostId = session.hostId ?? LOCAL_HOST;
	const [step, setStep] = useState<MoveStep>();
	const [result, setResult] = useState<MoveResult>();
	const [error, setError] = useState<MoveError | Error>();
	const moving = step !== undefined && !result && !error;

	const sourceRepo = useQuery({
		queryKey: ["move-source-repo", sourceHost, session.workspaceId],
		queryFn: async () => {
			const project = await clientForSessionHost(sourceHost).GET("/api/v1/projects/{id}", { params: { path: { id: session.workspaceId } } });
			return repoKey(projectRepo(project.data?.project));
		},
	});
	const signedIn = Boolean(cloudClient && org);
	const loading = signedIn && (sourceRepo.isPending || cloudProjects.isPending);
	const project = sourceRepo.data
		? (cloudProjects.data ?? []).find((candidate) => repoKey(candidate.repositoryUrl) === sourceRepo.data)
		: undefined;
	const harness = session.provider;
	const unavailable = !signedIn
		? t("move.signedOut")
		: loading
			? undefined
			: !project
				? t("move.noCloudProject")
				: !offering.features.includes(CLOUD_BRANCH_FEATURE)
					? t("move.cloudNeedsUpdate")
					: !(CLOUD_AGENT_PROVIDERS as readonly string[]).includes(harness)
						? t("move.cloudAgentUnsupported", { agent: harness })
						: undefined;
	const target: MoveTarget | undefined =
		org && project && !loading && !unavailable ? { orgId: org.id, projectId: project.id, harness } : undefined;
	const cloudLabel = t("move.cloud");

	const start = async () => {
		if (!target || !cloudClient) return;
		setError(undefined);
		try {
			const moved = await moveSession(
				{ sessionId: session.id, target, ...(sourceHost !== LOCAL_HOST ? { sourceLabel: labelForHost(sourceHost) } : {}) },
				{ source: clientForSessionHost(sourceHost), cloud: cloudClient, onStep: setStep },
			);
			setResult(moved);
		} catch (failure) {
			setError(failure instanceof Error ? failure : new Error(String(failure)));
		} finally {
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(sourceHost) });
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		}
	};

	const openMoved = () => {
		if (!result || !target) return;
		onClose();
		navigateToSession(target.projectId, result.sessionId);
	};

	const stepState = (candidate: MoveStep): "done" | "running" | "failed" | "pending" => {
		if (step === undefined) return "pending";
		const current = STEPS.indexOf(step);
		const index = STEPS.indexOf(candidate);
		if (error instanceof MoveError && error.step === candidate) return "failed";
		if (result && candidate === "complete" && result.completeError) return "failed";
		if (index < current || (result && index <= current)) return "done";
		return index === current && !error ? "running" : "pending";
	};

	return (
		<Dialog open onOpenChange={(open) => !open && !moving && onClose()}>
			<DialogContent
				className={settingsDialogContentClass}
				data-testid="move-session-dialog"
				showCloseButton={!moving}
				onEscapeKeyDown={(event) => moving && event.preventDefault()}
				onInteractOutside={(event) => moving && event.preventDefault()}
			>
				<div className={cn(settingsDialogHeaderClass, "py-4")}>
					<DialogTitle>{t("move.title", { title: session.title })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3 pt-4")}>
					{step === undefined ? (
						loading ? (
							<p role="status" className="flex items-center gap-2 text-sm text-settings-muted">
								<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
								{t("move.loading")}
							</p>
						) : (
							<p className="text-sm text-settings-muted">{unavailable ?? t("move.cloudPublishes")}</p>
						)
					) : (
						<ol className="flex flex-col gap-2" aria-live="polite">
							{STEPS.map((candidate) => {
								const state = stepState(candidate);
								return (
									<li key={candidate} className="flex items-center gap-2.5 text-sm">
										{state === "done" ? (
											<Check className="size-icon-sm text-success" aria-hidden="true" />
										) : state === "running" ? (
											<Loader2 className="size-icon-sm animate-spin text-muted-foreground" aria-hidden="true" />
										) : state === "failed" ? (
											<CircleAlert className="size-icon-sm text-destructive" aria-hidden="true" />
										) : (
											<span className="size-icon-sm" aria-hidden="true" />
										)}
										<span className={state === "pending" ? "text-settings-muted" : "text-settings-label"}>
											{t(`move.step.${candidate}`, { target: cloudLabel })}
										</span>
									</li>
								);
							})}
						</ol>
					)}
					{error ? (
						<div role="alert" className="space-y-1 text-sm text-destructive">
							<p>{error.message}</p>
							<p className="text-settings-muted">
								{error instanceof MoveError && !error.rolledBack ? t("move.failedNotRolledBack") : t("move.failedRolledBack")}
							</p>
						</div>
					) : null}
					{result ? (
						<p role="status" className="text-sm text-settings-label">
							{result.completeError ? t("move.completeFailed", { target: cloudLabel }) : t("move.done", { target: cloudLabel })}
						</p>
					) : null}
				</div>
				<div className={cn(settingsDialogFooterClass, "py-4")}>
					{result ? (
						<Button type="button" variant="primary" size="sm" onClick={openMoved}>
							{t("move.open")}
						</Button>
					) : (
						<>
							<Button type="button" variant="outline" size="sm" onClick={onClose} disabled={moving}>
								{t("confirm.cancel")}
							</Button>
							<Button type="button" variant="primary" size="sm" onClick={() => void start()} disabled={!target || moving}>
								{moving ? t("move.moving") : t("move.submit")}
							</Button>
						</>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}

/** A project's origin URL; a project the daemon could not resolve has none. */
function projectRepo(project: object | undefined): string {
	return project && "repo" in project && typeof project.repo === "string" ? project.repo : "";
}
