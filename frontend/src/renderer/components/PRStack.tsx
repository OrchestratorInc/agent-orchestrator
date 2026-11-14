import { apiErrorException } from "../lib/api-error-exception";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CornerDownRight, Plus } from "lucide-react";
import { useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { prStackQueryKey } from "../hooks/usePRStack";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { canStackOn, type PRStackInfo, type PRStackLayer } from "../lib/pr-stack";
import { openPRTab } from "../lib/pr-tabs";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { AppLink } from "./AppLink";
import { ConfirmDialog } from "./ConfirmDialog";
import { prStateIcon } from "./PRPills";
import { ProductExternalLink } from "./ProductExternalLink";
import { Button } from "./ui/button";

export type PRStackSectionData = {
	info: PRStackInfo;
	label: string;
	/** A new task can start on top of this PR. */
	canStartTask: boolean;
};

/** What the side column shows of a PR's stack; nothing when there's nothing to do with one. */
export function usePRStackSection(pr: SessionPRSummary, session: WorkspaceSession, info: PRStackInfo | undefined): PRStackSectionData | undefined {
	const { t } = useTranslation();
	// Not read yet is not the same as no stack: a mid-stack PR can't take a new task.
	if (session.cloud || pr.provider !== "github" || !info) return undefined;
	const canStartTask = (pr.state === "open" || pr.state === "draft") && Boolean(pr.sourceBranch) && canStackOn(info.stack, pr.number);
	if (!info.stack && !info.parent && !canStartTask) return undefined;
	return {
		canStartTask,
		info,
		label: info.stack ? t("pr.stack.title", { number: info.stack.number }) : t("pr.stack.label"),
	};
}

const rowClass = "flex min-h-6 min-w-0 max-w-full items-center gap-2 text-control";
const markClass = "size-icon-sm shrink-0";
const linkClass = "flex min-w-0 items-center gap-1.5 rounded-sm text-left text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

function LayerName({ layer }: { layer: PRStackLayer }) {
	return (
		<>
			<span className="shrink-0 tabular-nums">#{layer.number}</span>
			{layer.title ? <span className="min-w-0 truncate">{layer.title}</span> : null}
		</>
	);
}

/**
 * One layer of the stack. This PR is plain text, a layer with an AO session opens
 * as a tab in that session, and the rest open on GitHub.
 */
function LayerRow({ current, hostId, layer, session }: { current: boolean; hostId?: string; layer: PRStackLayer; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const state = prStateIcon[layer.state];
	const title = layer.title ? `#${layer.number} ${layer.title}` : `#${layer.number}`;
	const open = () => {
		const owner = layer.sessionId;
		if (!owner) return;
		openPRTab(sessionUiKey(owner, hostId), layer.url);
		if (owner !== session.id) navigateToSession(session.workspaceId, owner, hostId);
	};
	return (
		<li aria-current={current ? "true" : undefined} className={rowClass} title={title}>
			<state.icon aria-hidden="true" className={cn(markClass, state.className)} />
			<span className="sr-only">{t(`pr.tab.state.${layer.state}`)}</span>
			{current ? (
				<span className="flex min-w-0 items-center gap-1.5 font-medium text-foreground"><LayerName layer={layer} /></span>
			) : layer.sessionId ? (
				<button className={linkClass} onClick={open} type="button"><LayerName layer={layer} /></button>
			) : layer.url ? (
				<AppLink className={linkClass} href={layer.url} noHoverPreview rel="noopener noreferrer" target="_blank"><LayerName layer={layer} /></AppLink>
			) : (
				<span className="flex min-w-0 items-center gap-1.5 text-muted-foreground"><LayerName layer={layer} /></span>
			)}
		</li>
	);
}

/** Describes the task to stack on this PR. Its worker starts from this PR's branch. */
function StackTaskDialog({ hostId, onOpenChange, open, pr, session }: {
	hostId?: string;
	onOpenChange: (open: boolean) => void;
	open: boolean;
	pr: SessionPRSummary;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const navigateToSession = useNavigateToSession();
	const [brief, setBrief] = useState("");
	const textRef = useRef<HTMLTextAreaElement>(null);
	// One key per dialog, so a retry after a dropped response can't start two tasks.
	const requestId = useRef(crypto.randomUUID());
	const start = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/orchestrators/delegate", {
				body: {
					agent: session.provider === "fake" ? undefined : session.provider,
					baseBranch: pr.sourceBranch,
					brief: brief.trim(),
					clientRequestId: requestId.current,
					projectId: session.workspaceId,
				},
			});
			if (error || !data?.workerId) throw apiErrorException(error, t("pr.stack.newTaskFailed"));
			return data.workerId;
		},
		onSuccess: (workerId) => {
			setBrief("");
			requestId.current = crypto.randomUUID();
			onOpenChange(false);
			navigateToSession(session.workspaceId, workerId, hostId);
		},
	});
	const ready = brief.trim().length > 0;
	const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
		if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && ready && !start.isPending) start.mutate();
	};
	return (
		<ConfirmDialog
			busy={start.isPending}
			confirmDisabled={!ready}
			confirmLabel={t("pr.stack.newTaskStart")}
			description={t("pr.stack.newTaskDescription", { branch: pr.sourceBranch })}
			error={start.error instanceof Error ? start.error.message : null}
			initialFocusRef={textRef}
			onConfirm={() => start.mutate()}
			onOpenChange={(next) => { onOpenChange(next); if (!next) start.reset(); }}
			open={open}
			title={t("pr.stack.newTaskTitle", { number: pr.number })}
		>
			<textarea
				aria-label={t("pr.stack.newTaskPlaceholder")}
				className="min-h-24 w-full resize-none rounded-lg border border-border bg-transparent px-3 py-2 text-sm text-foreground outline-none placeholder:text-passive focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40"
				onChange={(event) => setBrief(event.target.value)}
				onKeyDown={onKeyDown}
				placeholder={t("pr.stack.newTaskPlaceholder")}
				ref={textRef}
				value={brief}
			/>
		</ConfirmDialog>
	);
}

/**
 * A PR's stack: its layers top first, as they sit on the base branch, this one
 * marked. A PR based on another open PR but not stacked on it can join; the top
 * of a stack, or a PR with none, can take a new task.
 */
export function PRStackSection({ hostId, pr, section, session }: {
	hostId?: string;
	pr: SessionPRSummary;
	section: PRStackSectionData;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [composing, setComposing] = useState(false);
	const { info } = section;
	const stack = info.stack;
	const add = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/stack", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url },
			});
			if (error) throw apiErrorException(error, t("pr.stack.addFailed"));
			return data;
		},
		onSuccess: (data) => queryClient.setQueryData(prStackQueryKey(pr.url, hostId), data),
	});
	return (
		<div className="flex min-w-0 flex-1 flex-col gap-1 @3xl:w-full">
			{stack ? (
				<ol aria-label={section.label} className="m-0 flex min-w-0 list-none flex-col p-0">
					{[...stack.layers].reverse().map((layer) => (
						<LayerRow current={layer.number === pr.number} hostId={hostId} key={layer.number} layer={layer} session={session} />
					))}
					<li className={cn(rowClass, "text-passive")}>
						<CornerDownRight aria-hidden="true" className={markClass} />
						<span className="min-w-0 truncate" title={stack.baseBranch}>{stack.baseBranch}</span>
					</li>
				</ol>
			) : null}
			{info.parent ? (
				<div className={cn(rowClass, "justify-between")}>
					<ProductExternalLink className="min-w-0 truncate text-muted-foreground underline-offset-2 hover:underline" href={info.parent.url}>
						{t("pr.stack.basedOn", { number: info.parent.number })}
					</ProductExternalLink>
					<Button className="h-6 shrink-0 px-2 text-xs" disabled={add.isPending} onClick={() => add.mutate()} size="sm" type="button" variant="secondary">
						{t("pr.stack.add")}
					</Button>
				</div>
			) : null}
			{add.error instanceof Error ? <p className="m-0 text-xs leading-normal text-error" role="status">{add.error.message}</p> : null}
			{section.canStartTask ? (
				<>
					<button className={cn(rowClass, linkClass, "w-fit")} onClick={() => setComposing(true)} type="button">
						<Plus aria-hidden="true" className={markClass} />
						{t("pr.stack.newTask")}
					</button>
					<StackTaskDialog hostId={hostId} onOpenChange={setComposing} open={composing} pr={pr} session={session} />
				</>
			) : null}
		</div>
	);
}
