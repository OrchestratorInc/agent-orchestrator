import { ChevronDown, GitPullRequest, Users } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSessionParticipants } from "../hooks/useSessionParticipants";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import { pendingMergeRequestFor, useMergeRequestActions, useProjectMergeRequests } from "../hooks/useProjectMergeRequests";
import type { CloudCpMergeRequest } from "../lib/cloud-cp";
import { sessionPRDisplaySummaries } from "../lib/pr-display";
import { cn } from "../lib/utils";
import { isOrchestratorSession, type WorkspaceSession } from "../types/workspace";
import { PRSummaryCard } from "./SessionInspector";
import { Button } from "./ui/button";

/**
 * The floating tile on a shared project's orchestrator: every pull request
 * the project's sessions opened, who opened it and in which session, and the
 * merge requests guests sent. Its owner approves (which merges) or rejects a
 * request here; a guest sees where theirs stands. Nothing shows for a project
 * nobody was given.
 */
export function SharedProjectPanel({ orchestrator, sessions, hostId }: {
	orchestrator: WorkspaceSession;
	sessions: readonly WorkspaceSession[];
	hostId?: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const orgId = orchestrator.cloud?.orgId;
	const projectId = orchestrator.workspaceId;
	const participants = useSessionParticipants([orchestrator]).get(orchestrator.id) ?? [];
	const shared = participants.length > 0;
	const requests = useProjectMergeRequests(orgId, projectId, shared, orchestrator.id);
	if (!orgId || !shared) return null;

	const guest = orchestrator.cloud?.shared === true;
	const ownerName = participants.find((person) => person.role === "owner")?.name;
	const workers = sessions.filter((session) => !isOrchestratorSession(session) && session.cloud);
	const prCount = workers.reduce((count, session) => count + session.prs.length, 0);
	const pending = (requests.data ?? []).filter((request) => request.status === "pending");
	const summary = [
		t("shareProject.prCount", { count: prCount }),
		...(pending.length > 0 ? [guest ? t("shareProject.waiting", { count: pending.length }) : t("shareProject.toApprove", { count: pending.length })] : []),
	].join(" · ");

	return (
		<div className="pointer-events-none absolute inset-y-3 right-3 z-chrome flex w-80 max-w-[calc(100%-24px)] flex-col items-end" data-shared-project-panel>
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((value) => !value)}
				className={cn(
					"pointer-events-auto inline-flex max-w-full items-center gap-2 rounded-lg border border-border bg-popover px-3 py-2 text-left text-xs text-popover-foreground shadow-md transition-colors hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
					pending.length > 0 && !guest && "border-status-in-review",
				)}
			>
				<Users aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />
				<span className="min-w-0">
					<span className="block font-medium text-foreground">{t("shareProject.title")}</span>
					<span className="block truncate text-muted-foreground">{summary}</span>
				</span>
				<ChevronDown aria-hidden="true" className={cn("size-icon-sm shrink-0 text-muted-foreground transition-transform", open && "rotate-180")} />
			</button>
			{open ? (
				<div className="pointer-events-auto mt-2 flex min-h-0 w-full flex-col overflow-y-auto rounded-lg border border-border bg-popover p-2 text-popover-foreground shadow-md" data-shared-project-list>
					{prCount === 0 ? (
						<p className="flex items-center gap-2 px-2 py-3 text-xs text-muted-foreground">
							<GitPullRequest aria-hidden="true" className="size-icon-sm" />
							{t("shareProject.noPRs")}
						</p>
					) : null}
					{workers.filter((session) => session.prs.length > 0).map((session) => (
						<SessionPullRequests
							key={session.id}
							liveSessionId={orchestrator.id}
							canDecide={!guest}
							guest={guest}
							hostId={hostId}
							openedBy={session.cloud?.requestedBy?.name || ownerName || t("chat.shared.someone")}
							orgId={orgId}
							requests={requests.data}
							session={session}
						/>
					))}
				</div>
			) : null}
		</div>
	);
}

function SessionPullRequests({ canDecide, guest, hostId, liveSessionId, openedBy, orgId, requests, session }: {
	canDecide: boolean;
	liveSessionId: string;
	guest: boolean;
	hostId?: string;
	openedBy: string;
	orgId: string;
	requests: readonly CloudCpMergeRequest[] | undefined;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const scm = useSessionScmSummary(session.id, true, orgId, session.autoInjectCI === true, hostId);
	const prs = sessionPRDisplaySummaries(session, scm.data?.prs);
	const actions = useMergeRequestActions(orgId, session.workspaceId);
	return (
		<section className="flex flex-col gap-1.5 py-1.5" data-shared-project-session={session.id}>
			<p className="truncate px-1 text-2xs text-muted-foreground">
				{t("shareProject.openedBy", { name: openedBy, session: session.title })}
			</p>
			{prs.map((pr) => {
				const request = pendingMergeRequestFor(requests, pr.url);
				const decision = actions.approve.error ?? actions.reject.error;
				return (
					<div key={pr.url || pr.number} className="flex flex-col gap-1">
						<PRSummaryCard
							canOpenReviews={false}
							cloudOrgId={orgId}
							guest={guest}
							hostId={hostId}
							onOpenReviews={() => undefined}
							pr={pr}
							projectId={session.workspaceId}
							liveSessionId={liveSessionId}
							sessionId={session.id}
						/>
						{request ? (
							<div className="flex min-w-0 items-center gap-2 rounded-md bg-muted/50 px-2 py-1.5 text-xs" data-merge-request={request.id}>
								<span className="min-w-0 flex-1 truncate text-foreground">
									{t("shareProject.wantsToMerge", { name: request.requestedBy.name || t("chat.shared.someone") })}
								</span>
								{canDecide ? (
									<>
										<Button
											type="button"
											size="sm"
											variant="ghost"
											disabled={actions.reject.isPending || actions.approve.isPending}
											onClick={() => actions.reject.mutate(request.id)}
										>
											{t("shareProject.reject")}
										</Button>
										<Button
											type="button"
											size="sm"
											className="bg-success text-on-foreground hover:bg-success/80"
											disabled={actions.approve.isPending || actions.reject.isPending || !pr.headSha}
											onClick={() => actions.approve.mutate({ requestId: request.id, expectedHeadSha: pr.headSha })}
										>
											{t("shareProject.approve")}
										</Button>
									</>
								) : (
									<span className="shrink-0 text-muted-foreground">{t("shareProject.waitingForOwner")}</span>
								)}
							</div>
						) : null}
						{request && decision instanceof Error ? (
							<p role="alert" className="px-1 text-2xs text-error">{decision.message}</p>
						) : null}
					</div>
				);
			})}
		</section>
	);
}
