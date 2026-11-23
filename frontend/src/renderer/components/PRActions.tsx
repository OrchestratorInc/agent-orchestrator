import { apiErrorException } from "../lib/api-error-exception";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowUpFromLine, ChevronDown, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, ListEnd, ListX, Loader2, RefreshCw, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { prStackQueryKey, usePRStack } from "../hooks/usePRStack";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { clientForSessionHost } from "../lib/host-clients";
import { prMergeQueueDetail, prMergeQueueEntry, prSummaryBlockReason, type PRBlockReason, type PRMergeQueueEntry } from "../lib/pr-display";
import { stackBlocker, stackLayersToLand, type StackBlockReason } from "../lib/pr-stack";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
import type { SessionGitActionKind } from "../stores/session-git-action-store";
import type { BranchGitStep } from "./SessionBranchSummary";
import { ConfirmDialog } from "./ConfirmDialog";
import { prStateIcon } from "./PRPills";
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

/**
 * A Cloud PR card's git step: the control plane merges from the card itself,
 * so the step stands alone rather than sharing PRActionButton, whose merge,
 * close and stack actions go to the local daemon.
 */
export function CloudPRGitStepButton({ git, messages }: {
	git: BranchGitStep;
	/** Where its error goes when the button sits in the card's title row; see PRActionButton. */
	messages?: HTMLElement | null;
}) {
	const primary = git.primary;
	if (!git.pending && !primary) return null;
	const Icon = git.pending ? Loader2 : gitIcon[primary!.kind];
	const label = git.pending ?? primary!.label;
	const error = git.error ? <p className="m-0 text-xs leading-normal text-error" role="status">{git.error}</p> : null;
	return (
		<div className={cn("flex min-w-0 flex-col gap-1", messages === undefined ? "items-stretch" : "max-w-[60%] shrink-0 items-end")} data-testid="pr-git-step">
			<Button
				aria-busy={git.pending ? "true" : undefined}
				className="min-w-0 gap-1 px-2 text-xs"
				disabled={Boolean(git.pending)}
				onClick={() => primary && git.run(primary.kind)}
				size="sm"
				type="button"
				variant="secondary"
			>
				<Icon aria-hidden="true" className={cn("size-icon-sm", git.pending && "animate-spin")} />
				<span className="truncate">{label}</span>
			</Button>
			{messages === undefined ? error : messages && error ? createPortal(error, messages) : null}
		</div>
	);
}

/** Refreshes what the inspector shows of the PR after a write to the provider. */
function useRefreshPR(sessionId: string, hostId?: string) {
	const queryClient = useQueryClient();
	return () => Promise.all([
		queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) }),
		queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
	]);
}

/**
 * Changes a PR's state on the provider. The UI shows the confirmed state at once;
 * the refetch reconciles the rest (the workspace refetch alone can take several
 * seconds) without holding the control.
 */
function usePRStateChange(pr: SessionPRSummary, sessionId: string, hostId?: string, onChanged?: () => void) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const refresh = useRefreshPR(sessionId, hostId);
	const showState = (state: SessionPRSummary["state"], changedAt: string) => {
		queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: unknown[] }>(
			sessionScmSummaryQueryKey(sessionId, hostId),
			(current) => current ? {
				...current,
				prs: current.prs.map((cached) => cached.url === pr.url ? { ...cached, state, stateChangedAt: changedAt, updatedAt: changedAt } : cached),
			} : current,
		);
	};
	const change = useMutation({
		mutationFn: async (action: PRStateAction) => {
			if (usePreviewData) return undefined;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/state", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, action },
			});
			if (error) throw apiErrorException(error, t(`pr.state.failed.${action}`, { number: pr.number }));
			return data;
		},
		onSuccess: (result) => {
			onChanged?.();
			if (result?.state) showState(result.state, result.stateChangedAt);
			void refresh();
		},
	});
	/** Shows the PR's confirmed place in its merge queue, or that it left, before the next sync. */
	const showQueue = (entry: PRMergeQueueEntry | undefined) => {
		queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: unknown[] }>(
			sessionScmSummaryQueryKey(sessionId, hostId),
			(current) => current ? {
				...current,
				prs: current.prs.map((cached) => cached.url === pr.url ? { ...cached, mergeQueueRequired: true, mergeQueue: entry } : cached),
			} : current,
		);
	};
	return { change, refresh, showQueue, showState };
}

/**
 * A PR's one action button, as in other agent apps: it does the next step,
 * the branch's work first (commit and push, update from main), then what the
 * PR is waiting for (Ready for review on a draft; Reopen when closed; merge
 * with the chosen method, or join the merge queue where the base branch has
 * one, which sets the method itself; or, on a stacked PR, the layers up to it
 * together, since merging one alone would land it on its parent's branch), and its menu
 * holds everything else: the other
 * branch steps, the merge method and Close. Merge stays visible while the PR
 * isn't ready, greyed out with the reason.
 */
export function PRActionButton({ canMerge, git, hostId, messages, pr, sessionId }: {
	/** Readiness allows merging, fenced to the head commit the card shows. */
	canMerge: boolean;
	/**
	 * Where the button's status lines go. The button sits in the card's title
	 * row, so its errors and queue notes render under the card instead, in
	 * this element; without one they follow the button.
	 */
	messages?: HTMLElement | null;
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
	const [confirmStack, setConfirmStack] = useState(false);
	const { change, refresh, showQueue, showState } = usePRStateChange(pr, sessionId, hostId, () => setConfirmClose(false));
	const queued = prMergeQueueEntry(pr);
	// The base branch merges through a queue: merging means joining it.
	const viaQueue = pr.mergeQueueRequired === true;
	const stackQuery = usePRStack(pr, hostId, pr.state === "open" || pr.state === "draft");
	const stack = stackQuery.data?.stack;
	const landing = stackLayersToLand(stack, pr.number);

	const merge = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return undefined;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(pr.number) } },
				// The PR view merges a PR nothing checks; the daemon confirms it first.
				body: { prUrl: pr.url, expectedHeadSha: pr.headSha, method, allowNoChecks: true },
			});
			if (error) throw apiErrorException(error, t("pr.merge.failed", { number: pr.number }));
			return data;
		},
		onSuccess: async (result) => {
			// The card turns merged at once; the next sync confirms it.
			if (result?.state === "merged") {
				showState(result.state, result.stateChangedAt);
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				return;
			}
			// The base branch has a merge queue: the PR joined it and stays open.
			if (result?.queue) showQueue(result.queue);
			await refresh();
		},
	});
	const dequeue = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return undefined;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/dequeue", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url },
			});
			if (error) throw apiErrorException(error, t("pr.queue.removeFailed", { number: pr.number }));
			return data;
		},
		onSuccess: async () => {
			showQueue(undefined);
			await refresh();
		},
		// It may have merged or left the queue meanwhile; show what's true now.
		onError: () => { void refresh(); },
	});
	const mergeStack = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return undefined;
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/stack/merge", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, method, allowNoChecks: true, expectedHeads: (landing ?? []).map(({ headSha, number }) => ({ headSha, number })) },
			});
			if (error) throw apiErrorException(error, t("pr.stack.mergeFailed"));
			return data;
		},
		onSuccess: async (result) => {
			setConfirmStack(false);
			if (result?.status === "merged") showState("merged", result.stateChangedAt);
			void queryClient.invalidateQueries({ queryKey: prStackQueryKey(pr.url, hostId) });
			await refresh();
		},
	});
	if (pr.state === "merged" || !pr.url) return null;
	const busy = merge.isPending || mergeStack.isPending || change.isPending || dequeue.isPending;
	const open = pr.state === "open" || pr.state === "draft";
	const lowerBlock = landing ? stackBlocker(landing, pr.number) : undefined;
	const blocker = lowerBlock
		? t("pr.stack.blocked", { number: lowerBlock.number, reason: t(stackBlockReasonLabel[lowerBlock.reason]) })
		: canMerge || queued ? undefined : t(blockedSentence[prSummaryBlockReason(pr) ?? "provider"]);
	// Until the stack is known, a merge can't say what it would land. A failed read
	// doesn't hold it up: the daemon re-checks before merging.
	const mergeAllowed = canMerge && !lowerBlock && !stackQuery.isLoading;
	const severalLayers = landing && landing.length > 1 ? landing : undefined;
	const mergeNow = severalLayers ? () => setConfirmStack(true) : landing ? () => mergeStack.mutate() : () => merge.mutate();
	const merging = merge.isPending || mergeStack.isPending;
	const mergeNotice = mergeStack.data?.status === "enqueued" ? t("pr.stack.queued") : mergeStack.data?.status === "pending" ? t("pr.stack.pending") : null;
	const methodLabel = t(`pr.merge.method.${method}`);
	const mergeLabel = severalLayers ? t("pr.stack.mergeThrough", { layers: severalLayers.length, number: pr.number }) : viaQueue ? t("pr.queue.add") : methodLabel;
	const mergeAria = severalLayers ? mergeLabel : viaQueue ? t("pr.queue.addFor", { number: pr.number }) : t("pr.merge.methodFor", { method: methodLabel, number: pr.number });
	const queuedLabel = queued ? (queued.position > 0 ? t("pr.queue.queuedAt", { position: queued.position }) : t("pr.queue.queued")) : "";
	const choose = (next: string) => {
		const value = next as PRMergeMethod;
		chosenMethod.set(repoKey, value);
		setMethod(value);
	};

	// Being behind the base doesn't hold up a merge the provider allows: merging
	// leads then, and the update waits in the menu.
	const updateYields = !git?.pending && git?.primary?.kind === "updateBase" && pr.state === "open" && mergeAllowed;
	const stackPending = stackQuery.isLoading && canMerge && pr.state === "open" && git?.primary?.kind === "updateBase";
	// The next step: the branch's work first, then what the PR is waiting for.
	const gitPrimary = updateYields ? undefined : git?.pending
		? { label: git.pending, aria: git.pending, icon: Loader2, run: () => {}, disabled: true, tone: "secondary" as const, spinning: true }
		: git?.primary
			? { label: git.primary.label, aria: git.primary.label, icon: gitIcon[git.primary.kind], run: () => git.run(git.primary!.kind), disabled: busy || stackPending, tone: "secondary" as const, spinning: false }
			: undefined;
	const prPrimary = pr.state === "draft"
		? { label: t("pr.state.action.ready"), aria: t("pr.state.action.ready"), icon: GitPullRequest, run: () => change.mutate("ready"), disabled: busy, tone: "secondary" as const }
		: pr.state === "closed"
			? { label: t("pr.state.action.reopen"), aria: t("pr.state.action.reopen"), icon: GitPullRequest, run: () => change.mutate("reopen"), disabled: busy, tone: "secondary" as const }
			: queued
				// Already in the queue: the button says where, and the menu takes it out.
				? { label: queuedLabel, aria: t("pr.queue.queuedFor", { number: pr.number, status: queuedLabel }), icon: ListEnd, run: () => {}, disabled: true, tone: "secondary" as const }
				: { label: merging ? t(viaQueue && !severalLayers ? "pr.queue.adding" : "pr.merge.merging") : mergeLabel, aria: mergeAria, icon: viaQueue && !severalLayers ? ListEnd : GitMerge, run: mergeNow, disabled: busy || !mergeAllowed, tone: mergeAllowed ? "go" as const : "secondary" as const };
	const primary = gitPrimary ?? { ...prPrimary, spinning: false };
	const gitMenu = updateYields && git?.primary ? [git.primary, ...git.menu] : (git?.menu ?? []).filter((step) => step.kind !== git?.primary?.kind);
	// Ready for review, when branch work holds the button on a draft.
	const stateSteps = pr.state === "draft" && gitPrimary ? [{ key: "ready" as const, label: t("pr.state.action.ready"), icon: GitPullRequest }] : [];
	const errorText = [merge.error, dequeue.error, change.error, confirmStack ? null : mergeStack.error].find((value): value is Error => value instanceof Error && !confirmClose)?.message ?? git?.error ?? null;

	const statusLines = (errorText || (mergeNotice && !queued) || (queued && !gitPrimary)) ? (
		<div className="flex min-w-0 flex-col gap-1" data-testid="pr-action-status">
			{errorText ? <p className="m-0 text-xs leading-normal text-error" role="status">{errorText}</p> : null}
			{mergeNotice && !queued ? <p className="m-0 text-xs leading-normal text-muted-foreground" role="status">{mergeNotice}</p> : null}
			{queued && !gitPrimary ? <p className="m-0 text-xs leading-normal text-muted-foreground" data-testid="pr-queue-status" role="status">{prMergeQueueDetail(queued)}</p> : null}
		</div>
	) : null;

	return (
		<div className={cn("flex min-w-0 flex-col gap-1", messages === undefined ? "items-stretch" : "max-w-[60%] shrink-0 items-end")} data-testid="pr-action-button">
			<div className={cn("flex min-w-0 items-stretch", messages === undefined && "w-full")}>
				<Button
					aria-label={primary.aria}
					className={cn(
						"gap-1 rounded-r-none px-2 text-xs",
						"min-w-0 flex-1",
						// The raised button draws its face, edge, hover and press from
						// --button-color, so the merge green sets that, not just the fill.
						primary.tone === "go" ? "[--button-color:var(--color-success)] text-on-foreground" : "",
					)}
					disabled={primary.disabled}
					key={gitPrimary ? "git" : "pr"}
					onClick={primary.run}
					size="sm"
					title={!gitPrimary && pr.state === "open" && blocker ? blocker : undefined}
					type="button"
					variant={primary.tone === "go" ? "primary" : "secondary"}
				>
					{primary.spinning || (!gitPrimary && busy && (merging || dequeue.isPending || change.variables === "ready" || change.variables === "reopen")) ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <primary.icon aria-hidden="true" className="size-icon-sm" />}
					<span className="truncate">{primary.label}</span>
				</Button>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("pr.actions.menu", { number: pr.number })}
							className={cn(
								"rounded-l-none border-l px-1",
								"w-control-md px-0",
								primary.tone === "go" ? "[--button-color:var(--color-success)] border-l-on-foreground/25 text-on-foreground" : "border-border",
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
								{queued ? (
									<DropdownMenuItem disabled={busy} onSelect={() => dequeue.mutate()}>
										<ListX aria-hidden="true" />
										{t("pr.queue.remove")}
									</DropdownMenuItem>
								) : viaQueue && pr.state === "open" ? (
									// The queue's own settings pick how its entries merge.
									<DropdownMenuLabel className="text-micro font-normal text-muted-foreground">{t("pr.queue.methodNote")}</DropdownMenuLabel>
								) : (
									<>
										<DropdownMenuLabel className="text-micro font-medium text-muted-foreground">{t("pr.merge.methodTitle")}</DropdownMenuLabel>
										<DropdownMenuRadioGroup onValueChange={choose} value={method}>
											{MERGE_METHODS.map((value) => (
												<DropdownMenuRadioItem key={value} value={value}>{t(`pr.merge.method.${value}`)}</DropdownMenuRadioItem>
											))}
										</DropdownMenuRadioGroup>
									</>
								)}
								{/* With branch work on the button, merging is still one pick away. */}
								{gitPrimary && pr.state === "open" && !queued ? (
									<DropdownMenuItem disabled={!mergeAllowed || busy} onSelect={mergeNow} title={blocker}>
										{viaQueue && !severalLayers ? <ListEnd aria-hidden="true" /> : <GitMerge aria-hidden="true" />}
										{mergeLabel}
									</DropdownMenuItem>
								) : null}
								<DropdownMenuSeparator />
								{stateSteps.map((step) => (
									<DropdownMenuItem disabled={busy} key={step.key} onSelect={() => change.mutate(step.key)}>
										<step.icon aria-hidden="true" />
										{step.label}
									</DropdownMenuItem>
								))}
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
			{messages === undefined ? statusLines : messages && statusLines ? createPortal(statusLines, messages) : null}
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
			{severalLayers ? (
				<ConfirmDialog
					busy={mergeStack.isPending}
					confirmLabel={t("pr.stack.confirmAction")}
					description={t("pr.stack.confirmDescription", {
						branch: stack?.baseBranch ?? pr.targetBranch,
						method: methodLabel,
						numbers: severalLayers.map(({ number }) => `#${number}`).join(", "),
					})}
					error={mergeStack.error instanceof Error ? mergeStack.error.message : null}
					onConfirm={() => mergeStack.mutate()}
					onOpenChange={(next) => { setConfirmStack(next); if (!next) mergeStack.reset(); }}
					open={confirmStack}
					title={t("pr.stack.confirmTitle", { layers: severalLayers.length })}
				/>
			) : null}
		</div>
	);
}

/** The tint behind each state's pill; its text takes the state's own colour. */
const stateTint: Record<SessionPRSummary["state"], string> = {
	draft: "bg-muted",
	open: "bg-success/15",
	merged: "bg-status-merged/15",
	closed: "bg-error/15",
};

const stateLabel = {
	draft: "pr.tab.state.draft",
	open: "pr.tab.state.open",
	merged: "pr.tab.state.merged",
	closed: "pr.tab.state.closed",
} as const;

/**
 * The state pill at the top of a PR's tab, and the place to change it, in
 * GitHub's words: an open PR can convert to a draft or close, a draft can be
 * marked ready for review or close, and a closed one can reopen. Closing asks
 * first, as it does on the card. A merged PR is done, so its pill is only a label.
 */
export function PRStateMenu({ hostId, pr, sessionId }: { hostId?: string; pr: SessionPRSummary; sessionId: string }) {
	const { t } = useTranslation();
	const [confirmClose, setConfirmClose] = useState(false);
	const { change } = usePRStateChange(pr, sessionId, hostId, () => setConfirmClose(false));
	const { icon: Icon, className } = prStateIcon[pr.state];
	const pill = cn("inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-control font-medium", stateTint[pr.state], className);
	const label = t(stateLabel[pr.state]);
	if (pr.state === "merged" || !pr.url) {
		return <span className={pill} data-testid="pr-state"><Icon aria-hidden="true" className="size-icon-sm" />{label}</span>;
	}
	const actions: { key: PRStateAction; label: string; icon: LucideIcon; destructive?: boolean }[] = pr.state === "closed"
		? [{ key: "reopen", label: t("pr.state.action.reopen"), icon: GitPullRequest }]
		: [
			pr.state === "draft"
				? { key: "ready", label: t("pr.state.action.ready"), icon: GitPullRequest }
				: { key: "draft", label: t("pr.state.action.draft"), icon: GitPullRequestDraft },
			{ key: "close", label: t("pr.actions.close"), icon: GitPullRequestClosed, destructive: true },
		];
	const choose = (action: PRStateAction) => {
		if (action === "close") setConfirmClose(true);
		else change.mutate(action);
	};
	const error = change.error instanceof Error && !confirmClose ? change.error.message : null;
	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button
						aria-label={t("pr.tab.state.menu", { number: pr.number, state: label })}
						className={cn(pill, "transition-opacity hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:opacity-60")}
						data-testid="pr-state"
						disabled={change.isPending}
						type="button"
					>
						{change.isPending ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <Icon aria-hidden="true" className="size-icon-sm" />}
						{label}
						<ChevronDown aria-hidden="true" className="size-icon-xs" />
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="min-w-44">
					{actions.map(({ key, label: actionLabel, icon: ActionIcon, destructive }) => (
						<DropdownMenuItem className={destructive ? "text-error focus:text-error" : undefined} key={key} onSelect={() => choose(key)}>
							<ActionIcon aria-hidden="true" />
							{actionLabel}
						</DropdownMenuItem>
					))}
				</DropdownMenuContent>
			</DropdownMenu>
			{error ? <p className="m-0 basis-full text-xs text-error" role="status">{error}</p> : null}
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
		</>
	);
}

/** Why Merge is greyed out, as one plain line. */
const blockedSentence = {
	draft: "pr.actions.blockedDraft",
	conflict: "pr.actions.blockedConflict",
	checks: "pr.actions.blockedChecks",
	cancelled: "pr.merge.checksCancelledDetail",
	checking: "pr.actions.blockedChecking",
	changes: "pr.actions.blockedChanges",
	review: "pr.actions.blockedReview",
	threads: "pr.actions.blockedThreads",
	provider: "pr.actions.blockedProvider",
} as const;

/** The same reasons as a label, short enough for a row. */
export const blockReasonLabel: Record<PRBlockReason, "pr.blocker.draft" | "pr.blocker.conflict" | "pr.blocker.checks" | "pr.card.checksCancelled" | "pr.blocker.checking" | "pr.blocker.changes" | "pr.blocker.review" | "pr.blocker.threads"> = {
	draft: "pr.blocker.draft",
	conflict: "pr.blocker.conflict",
	checks: "pr.blocker.checks",
	cancelled: "pr.card.checksCancelled",
	checking: "pr.blocker.checking",
	changes: "pr.blocker.changes",
	review: "pr.blocker.review",
	threads: "pr.blocker.threads",
};

/** A stack's reasons as a label: a layer's own, or one still to be rebased. */
export const stackBlockReasonLabel: Record<StackBlockReason, (typeof blockReasonLabel)[PRBlockReason] | "pr.stack.needsRebase"> = {
	...blockReasonLabel,
	rebase: "pr.stack.needsRebase",
};
