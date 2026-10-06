import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
	ArrowUpFromLine,
	ChevronDown,
	ChevronRight,
	GitBranch,
	GitCommitHorizontal,
	GitPullRequest,
	GitPullRequestDraft,
	Loader2,
	Pencil,
} from "lucide-react";
import {
	sessionWorkspaceFilesQueryOptions,
	sessionWorkspaceHistoryQueryOptions,
	type WorkspaceFileSummary,
} from "../hooks/useSessionWorkspaceFiles";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { useSessionGitActionStore, type SessionGitActionKind } from "../stores/session-git-action-store";
import type { WorkspaceSession } from "../types/workspace";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

// While an action is pending, re-read git state: a push changes no worktree
// file, so the workspace file watcher never invalidates on it.
const PENDING_POLL_MS = 3_000;
const PENDING_MAX_MS = 10 * 60_000;
// After the session goes idle, how long to wait for the result to show up.
// A new PR reaches the session through SCM discovery, which lags the push.
const SETTLE_MS: Record<SessionGitActionKind, number> = {
	createPR: 60_000,
	createDraftPR: 60_000,
	commit: 10_000,
	commitPush: 15_000,
	push: 15_000,
};

type GitFacts = {
	loaded: boolean;
	uncommitted: WorkspaceFileSummary[];
	committed: WorkspaceFileSummary[];
	/** Undefined while history is unavailable. */
	commits?: number;
	/** Commits not on the upstream; null when the branch has none, undefined while unknown. */
	ahead?: number | null;
	base?: string;
};

type GitActionPlan = { primary: SessionGitActionKind; menu: SessionGitActionKind[] };

export function SessionBranchSummary({
	hostId,
	onOpenFiles,
	openPRNumber,
	pullRequests,
	session,
}: {
	hostId?: string;
	onOpenFiles: () => void;
	/** The open or draft PR that new commits would update. */
	openPRNumber?: number;
	/** The rendered pull request section, or null when the session has none. */
	pullRequests: ReactNode;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const key = sessionUiKey(session.id, hostId);
	const pending = useSessionGitActionStore((state) => state.pending[key]);
	const startPending = useSessionGitActionStore((state) => state.start);
	const updatePending = useSessionGitActionStore((state) => state.update);
	const clearPending = useSessionGitActionStore((state) => state.clear);
	// Cloud sessions have no local workspace facts; orchestrators coordinate
	// workers rather than shipping a branch of their own.
	const showGit = !session.cloud && session.kind !== "orchestrator";
	const refetchInterval = pending ? PENDING_POLL_MS : false;
	const manifest = useQuery({ ...sessionWorkspaceFilesQueryOptions(session.id, undefined, hostId), enabled: showGit, refetchInterval });
	const history = useQuery({ ...sessionWorkspaceHistoryQueryOptions(session.id, undefined, hostId), enabled: showGit, refetchInterval });

	const sections = manifest.data?.sections;
	const facts: GitFacts = {
		loaded: manifest.data !== undefined,
		uncommitted: sections ? uniqueByPath([...sections.staged, ...sections.unstaged, ...sections.untracked]) : [],
		committed: sections?.committed ?? [],
		commits: history.data?.commits.length,
		ahead: history.data ? (history.data.ahead ?? null) : undefined,
		// head_fallback compares against HEAD because the base is unknown.
		base: manifest.data?.compareMode === "head_fallback" ? undefined : displayBaseRef(manifest.data?.compareBaseRef),
	};
	const plan = gitActionPlan(facts, openPRNumber);

	const send = useMutation({
		mutationFn: async (kind: SessionGitActionKind) => {
			if (usePreviewData) return;
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
				params: { path: { sessionId: session.id } },
				body: { message: gitActionMessage(kind, facts.base, openPRNumber) },
			});
			if (error) throw new Error(apiErrorMessage(error, t("inspector.git.actionFailed")));
		},
		onMutate: (kind) => startPending(key, kind),
		onError: () => clearPending(key),
	});

	const done = pending ? gitActionDone(pending.kind, facts, openPRNumber) : false;
	const activityState = session.activity?.state;
	useEffect(() => {
		if (!pending) return;
		if (done) {
			clearPending(key);
			return;
		}
		if (activityState === "active") {
			if (!pending.sawActive || pending.idleSince) updatePending(key, { sawActive: true, idleSince: undefined });
		} else if (pending.sawActive && !pending.idleSince) {
			updatePending(key, { idleSince: Date.now() });
		}
	}, [activityState, clearPending, done, key, pending, updatePending]);
	useEffect(() => {
		if (!pending) return;
		const deadline = Math.min(
			pending.startedAt + PENDING_MAX_MS,
			pending.idleSince ? pending.idleSince + SETTLE_MS[pending.kind] : Number.POSITIVE_INFINITY,
		);
		const timer = setTimeout(() => clearPending(key), Math.max(0, deadline - Date.now()));
		return () => clearTimeout(timer);
	}, [clearPending, key, pending]);

	if (!showGit) return pullRequests ?? null;

	const commitsRow = commitsRowView(facts, t);
	const hasWork = facts.uncommitted.length > 0 || commitsRow !== null;
	if (!session.branch && !pullRequests && !hasWork) return null;

	const sendError = send.error instanceof Error ? send.error.message : null;
	let action: ReactNode = null;
	if (pending) {
		action = (
			<Button aria-busy="true" className="w-full" disabled size="sm" type="button" variant="secondary">
				<Loader2 aria-hidden="true" className="size-icon-sm animate-spin" />
				{pendingLabel(pending.kind, t)}
			</Button>
		);
	} else if (plan) {
		action = (
			<GitActionButton
				menu={plan.menu.map((kind) => ({ kind, label: menuLabel(kind, t) }))}
				onRun={(kind) => send.mutate(kind)}
				primary={{ kind: plan.primary, label: primaryLabel(plan.primary, facts, openPRNumber, t) }}
				variant={openPRNumber === undefined ? "primary" : "secondary"}
			/>
		);
	}

	return (
		<section aria-label={t("inspector.branch")} className="mb-4" data-testid="inspector-branch">
			{session.branch ? <BranchLine base={facts.base} branch={session.branch} /> : null}
			{pullRequests}
			{facts.uncommitted.length > 0 ? (
				<GitStatusRow
					additions={sum(facts.uncommitted, "additions")}
					deletions={sum(facts.uncommitted, "deletions")}
					icon={<Pencil aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />}
					label={t("inspector.git.uncommittedFiles", { count: facts.uncommitted.length })}
					onClick={onOpenFiles}
				/>
			) : null}
			{commitsRow ? (
				<GitStatusRow
					additions={sum(facts.committed, "additions")}
					deletions={sum(facts.committed, "deletions")}
					icon={<GitCommitHorizontal aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />}
					label={commitsRow.label}
					note={commitsRow.note}
					noteTone={commitsRow.noteTone}
					onClick={onOpenFiles}
				/>
			) : null}
			{action ? <div className="mt-2">{action}</div> : null}
			{sendError ? (
				<p className="mt-1.5 text-2xs leading-normal text-error" role="status">
					{sendError}
				</p>
			) : null}
		</section>
	);
}

function BranchLine({ base, branch }: { base?: string; branch: string }) {
	return (
		<div className="flex min-w-0 items-center gap-1.5 px-0.5 pb-2 font-mono text-xs text-muted-foreground">
			<GitBranch aria-hidden="true" className="size-icon-2xs shrink-0" />
			<span className="min-w-0 truncate" title={branch}>
				{branch}
			</span>
			{base ? <span className="shrink-0 text-passive">→ {base}</span> : null}
		</div>
	);
}

function GitStatusRow({
	additions,
	deletions,
	icon,
	label,
	note,
	noteTone,
	onClick,
}: {
	additions: number;
	deletions: number;
	icon: ReactNode;
	label: string;
	note?: string;
	noteTone?: "warn" | "muted";
	onClick: () => void;
}) {
	return (
		<button
			className="flex min-h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm transition-colors hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			onClick={onClick}
			type="button"
		>
			{icon}
			<span className="flex min-w-0 flex-1 items-baseline gap-1.5">
				<span className="truncate">{label}</span>
				{note ? (
					<span className={noteTone === "warn" ? "shrink-0 text-xs text-status-in-review" : "shrink-0 text-xs text-passive"}>{note}</span>
				) : null}
			</span>
			<span className="inline-flex shrink-0 gap-1.5 font-mono text-xs tabular-nums">
				<span className="text-success">+{additions}</span>
				<span className="text-error">−{deletions}</span>
			</span>
			<ChevronRight aria-hidden="true" className="size-icon-sm shrink-0 text-passive" />
		</button>
	);
}

function GitActionButton({
	menu,
	onRun,
	primary,
	variant,
}: {
	menu: { kind: SessionGitActionKind; label: string }[];
	onRun: (kind: SessionGitActionKind) => void;
	primary: { kind: SessionGitActionKind; label: string };
	variant: "primary" | "secondary";
}) {
	const { t } = useTranslation();
	const icon = primary.kind === "push" || primary.kind === "commitPush"
		? <ArrowUpFromLine aria-hidden="true" className="size-icon-sm" />
		: <GitPullRequest aria-hidden="true" className="size-icon-sm" />;
	return (
		<div className="flex w-full">
			<Button
				className={menu.length ? "min-w-0 flex-1 rounded-r-none" : "w-full"}
				onClick={() => onRun(primary.kind)}
				size="sm"
				type="button"
				variant={variant}
			>
				{icon}
				<span className="truncate">{primary.label}</span>
			</Button>
			{menu.length ? (
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("inspector.git.moreActions")}
							className={variant === "primary"
								? "w-control-md rounded-l-none border-l-primary-foreground/20 px-0"
								: "w-control-md rounded-l-none border-l-border px-0"}
							size="sm"
							type="button"
							variant={variant}
						>
							<ChevronDown aria-hidden="true" className="size-icon-sm" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="min-w-48">
						{menu.map((item) => (
							<DropdownMenuItem className="gap-2" key={item.kind} onSelect={() => onRun(item.kind)}>
								{menuIcon(item.kind)}
								{item.label}
							</DropdownMenuItem>
						))}
					</DropdownMenuContent>
				</DropdownMenu>
			) : null}
		</div>
	);
}

function menuIcon(kind: SessionGitActionKind) {
	const className = "size-icon-sm text-muted-foreground";
	if (kind === "commit") return <GitCommitHorizontal aria-hidden="true" className={className} />;
	if (kind === "createDraftPR") return <GitPullRequestDraft aria-hidden="true" className={className} />;
	if (kind === "createPR") return <GitPullRequest aria-hidden="true" className={className} />;
	return <ArrowUpFromLine aria-hidden="true" className={className} />;
}

function commitsRowView(facts: GitFacts, t: TFunction): { label: string; note?: string; noteTone?: "warn" | "muted" } | null {
	if (facts.commits === undefined) {
		// History unavailable: still say the branch carries committed work.
		return facts.committed.length > 0 ? { label: t("inspector.git.committedFiles", { count: facts.committed.length }) } : null;
	}
	if (facts.commits === 0) return null;
	const label = t("inspector.git.commits", { count: facts.commits });
	if (facts.ahead === undefined) return { label };
	if (facts.ahead === null) return { label, note: t("inspector.git.notPushed"), noteTone: "warn" };
	if (facts.ahead > 0) return { label, note: t("inspector.git.someNotPushed", { count: facts.ahead }), noteTone: "warn" };
	return { label, note: t("inspector.git.pushed"), noteTone: "muted" };
}

export function gitActionPlan(facts: GitFacts, openPRNumber: number | undefined): GitActionPlan | null {
	if (!facts.loaded) return null;
	const dirty = facts.uncommitted.length > 0;
	const commits = facts.commits ?? 0;
	const unpushed = facts.ahead === null ? commits : facts.ahead ?? 0;
	if (openPRNumber !== undefined) {
		if (dirty) return { primary: "commitPush", menu: ["commit"] };
		if (unpushed > 0) return { primary: "push", menu: [] };
		return null;
	}
	if (dirty) return { primary: "createPR", menu: ["commit", "commitPush", "createDraftPR"] };
	if (unpushed > 0) return { primary: "createPR", menu: ["push", "createDraftPR"] };
	if (commits > 0 && facts.ahead === 0) return { primary: "createPR", menu: ["createDraftPR"] };
	return null;
}

export function gitActionDone(kind: SessionGitActionKind, facts: GitFacts, openPRNumber: number | undefined): boolean {
	switch (kind) {
		case "createPR":
		case "createDraftPR":
			return openPRNumber !== undefined;
		case "commit":
			return facts.loaded && facts.uncommitted.length === 0;
		case "commitPush":
			return facts.loaded && facts.uncommitted.length === 0 && facts.ahead === 0;
		case "push":
			return facts.ahead === 0;
	}
}

// Instructions for the session's agent, delivered through AO's automation relay.
export function gitActionMessage(kind: SessionGitActionKind, base: string | undefined, openPRNumber: number | undefined): string {
	const target = base ? `\`${base}\`` : "the default branch";
	const toPR = openPRNumber === undefined ? "" : ` to update PR #${openPRNumber}`;
	switch (kind) {
		case "createPR":
		case "createDraftPR":
			return `Commit any uncommitted changes with a clear message, push this branch, and open a ${kind === "createDraftPR" ? "draft " : ""}pull request against ${target}. Give it a descriptive title and summary.`;
		case "commit":
			return "Commit all uncommitted changes with a clear message. Do not push.";
		case "commitPush":
			return `Commit all uncommitted changes with a clear message and push this branch${toPR}.`;
		case "push":
			return `Push this branch${toPR}.`;
	}
}

function primaryLabel(kind: SessionGitActionKind, facts: GitFacts, openPRNumber: number | undefined, t: TFunction): string {
	if (kind === "commitPush") return t("inspector.git.commitAndPushToPR", { number: openPRNumber });
	if (kind === "push") return t("inspector.git.pushToPR", { number: openPRNumber });
	if (facts.uncommitted.length > 0) return t("inspector.git.commitAndCreatePR");
	if (facts.ahead !== 0) return t("inspector.git.pushAndCreatePR");
	return t("inspector.git.createPR");
}

function menuLabel(kind: SessionGitActionKind, t: TFunction): string {
	switch (kind) {
		case "commit":
			return t("inspector.git.commit");
		case "commitPush":
			return t("inspector.git.commitAndPush");
		case "push":
			return t("inspector.git.push");
		case "createDraftPR":
			return t("inspector.git.createDraftPR");
		case "createPR":
			return t("inspector.git.createPR");
	}
}

function pendingLabel(kind: SessionGitActionKind, t: TFunction): string {
	switch (kind) {
		case "createPR":
		case "createDraftPR":
			return t("inspector.git.creatingPR");
		case "commit":
			return t("inspector.git.committing");
		case "commitPush":
			return t("inspector.git.committingAndPushing");
		case "push":
			return t("inspector.git.pushing");
	}
}

function displayBaseRef(ref: string | undefined): string | undefined {
	return ref?.replace(/^refs\/(heads|remotes)\//, "").replace(/^origin\//, "") || undefined;
}

function uniqueByPath(files: WorkspaceFileSummary[]): WorkspaceFileSummary[] {
	return [...new Map(files.map((file) => [file.path, file])).values()];
}

function sum(files: WorkspaceFileSummary[], field: "additions" | "deletions"): number {
	return files.reduce((total, file) => total + file[field], 0);
}
