import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowUpFromLine, ChevronDown, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, Loader2, RefreshCw, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
import type { SessionGitActionKind } from "../stores/session-git-action-store";
import type { BranchGitStep } from "./SessionBranchSummary";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

type PRMergeMethod = "squash" | "merge" | "rebase";
type PRStateAction = "close" | "reopen" | "ready" | "draft";

const MERGE_METHODS: PRMergeMethod[] = ["squash", "merge", "rebase"];

/** The merge method picked per repository; outlives the card like GitHub's own choice. */
const chosenMethod = new Map<string, PRMergeMethod>();

export function resetPRMergeMethods() {
	chosenMethod.clear();
}

const gitIcon: Record<SessionGitActionKind, LucideIcon> = {
	commitPush: ArrowUpFromLine,
	push: ArrowUpFromLine,
	commit: GitCommitHorizontal,
	updateBase: RefreshCw,
	resolveConflicts: GitMerge,
	createPR: GitPullRequest,
	createDraftPR: GitPullRequestDraft,
};

/** Refreshes what the inspector shows of the PR after a write to the provider. */
function useRefreshPR(sessionId: string, hostId?: string) {
	const queryClient = useQueryClient();
	return () => Promise.all([
		queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) }),
		queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
	]);
}

/**
 * The PR card's one action button, at its foot, as in other agent apps: it
 * does the next step, the branch's work first (commit and push, update from
 * main), then what the PR is waiting for (Ready for review on a draft; Reopen
 * when closed; merge with the chosen method), and its menu holds everything
 * else: the other branch steps, the merge method and Close. Converting to a
 * draft sits under it as small text. Merge stays visible while the PR isn't
 * ready, greyed out with the reason.
 */
export function PRActionButton({ canMerge, git, hostId, pr, sessionId }: {
	/** Readiness allows merging, fenced to the head commit the card shows. */
	canMerge: boolean;
	/** The branch's git step, when this is the PR the branch updates. */
	git?: BranchGitStep;
	hostId?: string;
	pr: SessionPRSummary;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const repoKey = pr.repo || pr.url;
	const [method, setMethod] = useState<PRMergeMethod>(() => chosenMethod.get(repoKey) ?? "squash");
	const [confirmClose, setConfirmClose] = useState(false);
	const refresh = useRefreshPR(sessionId, hostId);

	const merge = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return undefined;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, expectedHeadSha: pr.headSha, method },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.merge.failed", { number: pr.number })));
			return data;
		},
		onSuccess: async (result) => {
			// The card turns merged at once; the next sync confirms it.
			if (result?.state === "merged") {
				queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: unknown[] }>(
					sessionScmSummaryQueryKey(sessionId, hostId),
					(current) => current ? {
						...current,
						prs: current.prs.map((cached) => cached.url === pr.url ? { ...cached, state: result.state, stateChangedAt: result.stateChangedAt, updatedAt: result.stateChangedAt } : cached),
					} : current,
				);
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				return;
			}
			await refresh();
		},
	});
	const change = useMutation({
		mutationFn: async (action: PRStateAction) => {
			if (usePreviewData) return;
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/state", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, action },
			});
			if (error) throw new Error(apiErrorMessage(error, t(`pr.state.failed.${action}`, { number: pr.number })));
		},
		onSuccess: async () => {
			setConfirmClose(false);
			await refresh();
		},
	});
	if (pr.state === "merged" || !pr.url) return null;
	const busy = merge.isPending || change.isPending;
	const open = pr.state === "open" || pr.state === "draft";
	const blocker = canMerge ? undefined : t(mergeBlocker(pr));
	const methodLabel = t(`pr.merge.method.${method}`);
	const choose = (next: string) => {
		const value = next as PRMergeMethod;
		chosenMethod.set(repoKey, value);
		setMethod(value);
	};

	// The next step: the branch's work first, then what the PR is waiting for.
	const gitPrimary = git?.pending
		? { label: git.pending, aria: git.pending, icon: Loader2, run: () => {}, disabled: true, tone: "secondary" as const, spinning: true }
		: git?.primary
			? { label: git.primary.label, aria: git.primary.label, icon: gitIcon[git.primary.kind], run: () => git.run(git.primary!.kind), disabled: busy, tone: "secondary" as const, spinning: false }
			: undefined;
	const prPrimary = pr.state === "draft"
		? { label: t("pr.state.action.ready"), aria: t("pr.state.action.ready"), icon: GitPullRequest, run: () => change.mutate("ready"), disabled: busy, tone: "secondary" as const }
		: pr.state === "closed"
			? { label: t("pr.state.action.reopen"), aria: t("pr.state.action.reopen"), icon: GitPullRequest, run: () => change.mutate("reopen"), disabled: busy, tone: "secondary" as const }
			: { label: merge.isPending ? t("pr.merge.merging") : methodLabel, aria: t("pr.merge.methodFor", { method: methodLabel, number: pr.number }), icon: GitMerge, run: () => merge.mutate(), disabled: busy || !canMerge, tone: canMerge ? "go" as const : "secondary" as const };
	const primary = gitPrimary ?? { ...prPrimary, spinning: false };
	const gitMenu = (git?.menu ?? []).filter((step) => step.kind !== git?.primary?.kind);
	// Small text under the button: draft ⇄ ready when the button isn't already doing it.
	const quiet = [
		...(pr.state === "open" ? [{ key: "draft", label: t("pr.actions.toDraft"), icon: GitPullRequestDraft, run: () => change.mutate("draft") }] : []),
		...(pr.state === "draft" && gitPrimary ? [{ key: "ready", label: t("pr.state.action.ready"), icon: GitPullRequest, run: () => change.mutate("ready") }] : []),
	];
	const errorText = [merge.error, change.error].find((value): value is Error => value instanceof Error && !confirmClose)?.message ?? git?.error ?? null;

	return (
		<div className="flex min-w-0 flex-col items-stretch gap-1" data-testid="pr-action-button">
			<div className="flex w-full items-stretch">
				<Button
					aria-label={primary.aria}
					className={cn(
						"gap-1 rounded-r-none px-2 text-xs",
						"min-w-0 flex-1",
						primary.tone === "go" ? "bg-success text-on-foreground hover:bg-success/80" : "",
					)}
					disabled={primary.disabled}
					onClick={primary.run}
					size="sm"
					title={!gitPrimary && pr.state === "open" && blocker ? blocker : undefined}
					type="button"
					variant={primary.tone === "go" ? "primary" : "secondary"}
				>
					{primary.spinning || (!gitPrimary && busy && (merge.isPending || change.variables === "ready" || change.variables === "reopen")) ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <primary.icon aria-hidden="true" className="size-icon-sm" />}
					<span className="truncate">{primary.label}</span>
				</Button>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("pr.actions.menu", { number: pr.number })}
							className={cn(
								"rounded-l-none border-l px-1",
								"w-control-md px-0",
								primary.tone === "go" ? "border-on-foreground/25 bg-success text-on-foreground hover:bg-success/80" : "border-border",
							)}
							disabled={busy || Boolean(git?.pending)}
							size="sm"
							type="button"
							variant={primary.tone === "go" ? "primary" : "secondary"}
						>
							<ChevronDown aria-hidden="true" className="size-icon-sm" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="w-64 max-w-[calc(100vw-2rem)]">
						{gitMenu.length > 0 ? (
							<>
								<DropdownMenuLabel className="text-micro font-medium text-muted-foreground">{t("pr.actions.branch")}</DropdownMenuLabel>
								{gitMenu.map((step) => {
									const Icon = gitIcon[step.kind];
									return <DropdownMenuItem key={step.kind} onSelect={() => git?.run(step.kind)}><Icon aria-hidden="true" />{step.label}</DropdownMenuItem>;
								})}
								<DropdownMenuSeparator />
							</>
						) : null}
						{open ? (
							<>
								<DropdownMenuLabel className="text-micro font-medium text-muted-foreground">{t("pr.merge.methodTitle")}</DropdownMenuLabel>
								<DropdownMenuRadioGroup onValueChange={choose} value={method}>
									{MERGE_METHODS.map((value) => (
										<DropdownMenuRadioItem key={value} value={value}>{t(`pr.merge.method.${value}`)}</DropdownMenuRadioItem>
									))}
								</DropdownMenuRadioGroup>
								{/* With branch work on the button, merging is still one pick away. */}
								{gitPrimary && pr.state === "open" ? (
									<DropdownMenuItem disabled={!canMerge || busy} onSelect={() => merge.mutate()} title={blocker}>
										<GitMerge aria-hidden="true" />
										{methodLabel}
									</DropdownMenuItem>
								) : null}
								<DropdownMenuSeparator />
								<DropdownMenuItem className="text-error focus:text-error" onSelect={() => setConfirmClose(true)}>
									<GitPullRequestClosed aria-hidden="true" />
									{t("pr.actions.close")}
								</DropdownMenuItem>
							</>
						) : (
							<DropdownMenuItem onSelect={() => change.mutate("reopen")}>
								<GitPullRequest aria-hidden="true" />
								{t("pr.state.action.reopen")}
							</DropdownMenuItem>
						)}
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			{quiet.length > 0 ? (
				// The less common steps sit under the button as small text on the right, not in its menu.
				<div className="mt-1 flex min-w-0 flex-wrap items-center justify-end gap-x-1.5 text-micro text-muted-foreground" data-testid="pr-quiet-actions">
					{quiet.map((action, index) => (
						<span className="inline-flex items-center gap-1.5" key={action.key}>
							{index > 0 ? <span aria-hidden="true" className="text-passive">·</span> : null}
							<button className="inline-flex items-center gap-1 rounded-sm transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" disabled={busy} onClick={action.run} type="button">
								<action.icon aria-hidden="true" className="size-icon-2xs" />
								{action.label}
							</button>
						</span>
					))}
				</div>
			) : null}
			{errorText ? <p className="m-0 text-xs leading-normal text-error" role="status">{errorText}</p> : null}
			<ConfirmDialog
				busy={change.isPending}
				confirmLabel={t("pr.actions.close")}
				description={t("pr.state.confirmCloseDescription", { number: pr.number })}
				destructive
				error={change.error instanceof Error && confirmClose ? change.error.message : null}
				onConfirm={() => change.mutate("close")}
				onOpenChange={(next) => { setConfirmClose(next); if (!next) change.reset(); }}
				open={confirmClose}
				title={t("pr.state.confirmCloseTitle", { number: pr.number })}
			/>
		</div>
	);
}

/** Why Merge is greyed out, as the key of one plain line. */
function mergeBlocker(pr: SessionPRSummary) {
	if (pr.state === "draft") return "pr.actions.blockedDraft" as const;
	if (pr.mergeability.state === "conflicting") return "pr.actions.blockedConflict" as const;
	if (pr.ci.state === "failing") return "pr.actions.blockedChecks" as const;
	if (pr.ci.state === "pending" || pr.ci.state === "unknown" || pr.mergeability.state === "unknown") return "pr.actions.blockedChecking" as const;
	if (pr.review.decision === "changes_requested") return "pr.actions.blockedChanges" as const;
	if (pr.review.decision === "review_required") return "pr.actions.blockedReview" as const;
	if (pr.review.hasUnresolvedHumanComments) return "pr.actions.blockedThreads" as const;
	return "pr.actions.blockedProvider" as const;
}
