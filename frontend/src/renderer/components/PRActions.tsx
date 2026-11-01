import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, Loader2, MoreHorizontal } from "lucide-react";
import { Fragment, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
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

export type PRMergeMethod = "squash" | "merge" | "rebase";
export type PRStateAction = "close" | "reopen" | "ready" | "draft";

const MERGE_METHODS: PRMergeMethod[] = ["squash", "merge", "rebase"];

/** The merge method picked per repository; outlives the card like GitHub's own choice. */
const chosenMethod = new Map<string, PRMergeMethod>();

export function resetPRMergeMethods() {
	chosenMethod.clear();
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
 * Merge with a method picker beside it: Squash and merge, Create a merge
 * commit, or Rebase and merge. Picking a method only changes the button; the
 * button merges, at once like the squash-only button it replaces, fenced to
 * the head commit the card shows.
 */
export function PRMergeButton({ hostId, pr, sessionId }: { hostId?: string; pr: SessionPRSummary; sessionId: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const repoKey = pr.repo || pr.url;
	const [method, setMethod] = useState<PRMergeMethod>(() => chosenMethod.get(repoKey) ?? "squash");
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
	const choose = (next: string) => {
		const value = next as PRMergeMethod;
		chosenMethod.set(repoKey, value);
		setMethod(value);
	};
	const label = t(`pr.merge.method.${method}`);
	return (
		<div className="flex min-w-0 flex-col items-end gap-1">
			<div className="inline-flex shrink-0 items-stretch" data-testid="pr-merge-button">
				<Button
					aria-label={t("pr.merge.methodFor", { method: label, number: pr.number })}
					className="gap-1 rounded-r-none bg-success px-2 text-xs text-on-foreground hover:bg-success/80"
					disabled={merge.isPending}
					onClick={() => merge.mutate()}
					size="sm"
					type="button"
				>
					{merge.isPending ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <GitMerge aria-hidden="true" className="size-icon-sm" />}
					{merge.isPending ? t("pr.merge.merging") : label}
				</Button>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("pr.merge.chooseMethod")}
							className="rounded-l-none border-l border-on-foreground/25 bg-success px-1 text-on-foreground hover:bg-success/80"
							disabled={merge.isPending}
							size="sm"
							type="button"
						>
							<ChevronDown aria-hidden="true" className="size-icon-sm" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="min-w-56">
						<DropdownMenuLabel>{t("pr.merge.methodTitle")}</DropdownMenuLabel>
						<DropdownMenuRadioGroup onValueChange={choose} value={method}>
							{MERGE_METHODS.map((value) => (
								<DropdownMenuRadioItem className="flex-col items-start gap-0.5" key={value} value={value}>
									<span>{t(`pr.merge.method.${value}`)}</span>
									<span className="text-micro text-muted-foreground">{t(`pr.merge.methodDetail.${value}`)}</span>
								</DropdownMenuRadioItem>
							))}
						</DropdownMenuRadioGroup>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			{merge.error instanceof Error ? <p className="m-0 text-xs leading-normal text-error" role="status">{merge.error.message}</p> : null}
		</div>
	);
}

function usePRStateChange(pr: SessionPRSummary, sessionId: string, hostId: string | undefined, onDone?: () => void) {
	const { t } = useTranslation();
	const refresh = useRefreshPR(sessionId, hostId);
	return useMutation({
		mutationFn: async (action: PRStateAction) => {
			if (usePreviewData) return;
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/state", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, action },
			});
			if (error) throw new Error(apiErrorMessage(error, t(`pr.state.failed.${action}`, { number: pr.number })));
		},
		onSuccess: async () => {
			onDone?.();
			await refresh();
		},
	});
}

/**
 * The one state change a draft or closed PR is waiting for, as a button under
 * the readiness rows: Ready for review, or Reopen.
 */
export function PRStateActions({ hostId, pr, sessionId }: { hostId?: string; pr: SessionPRSummary; sessionId: string }) {
	const { t } = useTranslation();
	const change = usePRStateChange(pr, sessionId, hostId);
	const action: PRStateAction | undefined = pr.state === "draft" ? "ready" : pr.state === "closed" ? "reopen" : undefined;
	if (!action || !pr.url) return null;
	return (
		<div className="mt-2 flex min-w-0 flex-col items-end gap-1" data-testid="pr-state-actions">
			<Button className="gap-1 px-2 text-xs" disabled={change.isPending} onClick={() => change.mutate(action)} size="sm" type="button" variant="secondary">
				{change.isPending ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <GitPullRequest aria-hidden="true" className="size-icon-sm" />}
				{t(`pr.state.action.${action}`)}
			</Button>
			{change.error instanceof Error ? <p className="m-0 text-xs leading-normal text-error" role="status">{change.error.message}</p> : null}
		</div>
	);
}

/**
 * The PR's other state changes in its header row, beside the link to the
 * provider: Convert to draft and Close PR. Closing asks first.
 */
export function PRStateMenu({ hostId, onError, pr, sessionId }: {
	hostId?: string;
	/** A failed change outside the close dialog, for the card to show; null clears it. */
	onError?: (message: string | null) => void;
	pr: SessionPRSummary;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const [confirmClose, setConfirmClose] = useState(false);
	const change = usePRStateChange(pr, sessionId, hostId, () => setConfirmClose(false));
	const failure = change.error instanceof Error && !confirmClose ? change.error.message : null;
	useEffect(() => { onError?.(failure); }, [failure, onError]);
	const menu: PRStateAction[] = pr.state === "open" ? ["draft", "close"] : pr.state === "draft" ? ["close"] : [];
	if (menu.length === 0 || !pr.url) return null;
	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button
						aria-label={t("pr.state.more", { number: pr.number })}
						className="flex min-h-8 shrink-0 items-center rounded-md px-1.5 text-passive transition-colors hover:bg-muted/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:opacity-60"
						disabled={change.isPending}
						type="button"
					>
						{change.isPending ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <MoreHorizontal aria-hidden="true" className="size-icon-sm" />}
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					{menu.map((action, index) => (
						<Fragment key={action}>
							{index > 0 && action === "close" ? <DropdownMenuSeparator /> : null}
							<DropdownMenuItem
								className={cn(action === "close" && "text-error focus:text-error")}
								onSelect={() => (action === "close" ? setConfirmClose(true) : change.mutate(action))}
							>
								{action === "close" ? <GitPullRequestClosed aria-hidden="true" /> : <GitPullRequestDraft aria-hidden="true" />}
								{t(`pr.state.action.${action}`)}
							</DropdownMenuItem>
						</Fragment>
					))}
				</DropdownMenuContent>
			</DropdownMenu>
			<ConfirmDialog
				busy={change.isPending}
				confirmLabel={t("pr.state.action.close")}
				description={t("pr.state.confirmCloseDescription", { number: pr.number })}
				destructive
				error={change.error instanceof Error && confirmClose ? change.error.message : null}
				onConfirm={() => change.mutate("close")}
				onOpenChange={(open) => { setConfirmClose(open); if (!open) change.reset(); }}
				open={confirmClose}
				title={t("pr.state.confirmCloseTitle", { number: pr.number })}
			/>
		</>
	);
}
