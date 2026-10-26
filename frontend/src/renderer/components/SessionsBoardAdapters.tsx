import { useEffect, useId, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
	scmUserAvatarUrl,
	SessionCardView,
	type BoardPullRequestLabels,
	type BoardPullRequestProgress,
	type BoardSessionPresentation,
	type BoardColumnLabels,
	type BoardUsagePresentation,
	type ProductUITranslator,
} from "@aoagents/product-ui";
import { Archive, Check, Copy, File, GitBranch, LoaderCircle, RotateCcw } from "lucide-react";
import type { MessageKey } from "../i18n";
import { aoBridge } from "../lib/bridge";
import { sessionBranchPresentation } from "../lib/session-branch";
import { BranchNamingLabel } from "./BranchNamingLabel";
import { formatTimeCompact } from "../lib/format-time";
import { prBrowserUrl, sessionPRDisplaySummaries } from "../lib/pr-display";
import { agentLabel } from "../lib/agent-options";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { canonicalTrackerIssueId, sessionArtifacts } from "../types/workspace";
import { formatCPU, formatMemory, type SessionMemoryReading } from "../hooks/useSessionMemory";
import type { ChipTone } from "@aoagents/product-ui";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import {
	clearTerminateSessionState,
	useTerminateSessionState,
} from "../hooks/useTerminateSession";
import { cn } from "../lib/utils";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import { SessionArchiveDialog, type SessionArchiveChoice } from "./SessionArchiveDialog";
import { ArtifactPreviewCard } from "./SessionLinkPreviewCard";
import { HoverCard, HoverCardTrigger } from "./ui/hover-card";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function toBoardSessionPresentation(
	session: WorkspaceSession,
): BoardSessionPresentation {
	const provisioningStatus =
		session.provisionState === "provisioning"
			? {
					className: "text-status-working",
					indicatorClassName: "bg-status-working animate-status-pulse",
					label: `Starting ${agentLabel(session.provider)}…`,
					tone: "var(--color-status-working)",
				}
			: undefined;
	const failedStartStatus =
		session.provisionState === "failed"
			? {
					className: "text-status-exited",
					indicatorClassName: "bg-status-exited",
					label: "Start failed",
					tone: "var(--color-status-exited)",
				}
			: undefined;
	return {
		activity: session.activity,
		branch: sessionBranchPresentation(session).branch,
		id: session.id,
		isTerminated: session.isTerminated,
		kanbanColumn: session.kanbanColumn,
		displayStatus: session.displayStatus,
		provider: session.provider,
		status: session.status,
		statusReadiness: session.statusReadiness,
		statusPresentation: provisioningStatus ?? failedStartStatus,
		title: session.title,
		trackerIssueId: canonicalTrackerIssueId(session.issueId),
		updatedAt: session.updatedAt,
		lastUserMessageAt: session.lastUserMessageAt,
	};
}

export function sessionsBoardLabels(t: TFunction): BoardColumnLabels {
	return {
		columnAria: (label) => t("shell.sessionsAria", { label }),
	};
}

export function BoardSessionCardAdapter({
	memory,
	memoryTone,
	onOpen,
	onOpenArtifact,
	onOpenArtifacts,
	onTerminate,
	session,
}: {
	/** Live reading of the session's process tree, for the card's resource chip. */
	memory?: SessionMemoryReading;
	/** Colour for the chip; neutral unless this card is part of the fix. */
	memoryTone?: ChipTone;
	onOpen: () => void;
	/** Opens the session with this artifact (by path) shown in the inspector. */
	onOpenArtifact?: (path: string) => void;
	onOpenArtifacts?: () => void;
	onTerminate?: (choice: SessionArchiveChoice) => void;
	session: WorkspaceSession;
}) {
	return (
		<DesktopSessionCard
			memory={memory}
			memoryTone={memoryTone}
			onOpen={onOpen}
			onOpenArtifact={onOpenArtifact}
			onOpenArtifacts={onOpenArtifacts}
			onTerminate={onTerminate}
			session={session}
		/>
	);
}

export function ArchivedSessionCardAdapter({
	hideTerminatedStatus = false,
	isRestoreDisabled,
	isRestoring,
	restoreAction,
	restoreError,
	session,
}: {
	hideTerminatedStatus?: boolean;
	isRestoreDisabled: boolean;
	isRestoring: boolean;
	restoreAction: (event: MouseEvent<HTMLButtonElement>) => void;
	restoreError?: string;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const branch = sessionBranchPresentation(session).branch ?? "";
	return (
		<DesktopSessionCard
			hideTerminatedStatus={hideTerminatedStatus}
			action={
				<ArchiveRestoreButton
					isDisabled={isRestoreDisabled}
					isRestoring={isRestoring}
					label={t("shell.restoreNamed", { title: session.title })}
					onClick={restoreAction}
				/>
			}
			branchAction={
				branch ? (
					<CopyActionButton
						copiedLabel={t("shell.copiedBranchNamed", { branch })}
						copyLabel={t("shell.copyBranchNamed", { branch })}
						value={branch}
					/>
				) : undefined
			}
			footer={<ArchiveRestoreError message={restoreError} />}
			interactive={false}
			session={session}
		/>
	);
}

function DesktopSessionCard({
	action,
	branchAction,
	footer,
	hideTerminatedStatus = false,
	interactive = true,
	memory,
	memoryTone,
	onOpen,
	onOpenArtifact,
	onOpenArtifacts,
	onTerminate,
	session,
}: {
	action?: ReactNode;
	branchAction?: ReactNode;
	footer?: ReactNode;
	hideTerminatedStatus?: boolean;
	interactive?: boolean;
	memory?: SessionMemoryReading;
	memoryTone?: ChipTone;
	onOpen?: () => void;
	onOpenArtifact?: (path: string) => void;
	onOpenArtifacts?: () => void;
	onTerminate?: (choice: SessionArchiveChoice) => void;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const summaries = sessionPRDisplaySummaries(
		session,
		useSessionScmSummary(session.id, true, session.cloud?.orgId, session.cloud?.orgId ? session.autoInjectCI === true : false, session.hostId).data?.prs,
	);
	const termination = useTerminateSessionState(session.id, session.hostId);
	const showTerminate = interactive && session.isTerminated !== true && onTerminate;
	const keepTerminateVisible = session.status === "merged";
	const resourcePresentation = toResourcePresentation(memory, session.activity?.state === "active", memoryTone, t);
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);

	const terminationOverlay = showTerminate ? (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex">
					<SessionArchiveDialog
						onConfirm={(choice) => {
							setConfirmOpen(false);
							onTerminate(choice);
						}}
						onOpenChange={setConfirmOpen}
						open={confirmOpen}
						session={session}
						trigger={
							<button
								aria-label={
									termination.isPending
										? t("shell.archivingNamedAria", { title: session.title })
										: t("shell.archiveNamed", { title: session.title })
								}
								className={cn(
									"inline-flex size-control-md items-center justify-center rounded-sm text-passive transition-[color,background-color,opacity] hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
									keepTerminateVisible || termination.isPending
										? "opacity-100"
										: "pointer-events-none opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100",
								)}
								onClick={(event) => {
									event.stopPropagation();
									clearTerminateSessionState(queryClient, session.id, session.hostId);
									// Always open the confirm; the modal owns its own dismissal.
									setConfirmOpen(true);
								}}
								disabled={termination.isPending}
								type="button"
							>
								{termination.isPending ? (
									<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								) : (
									<Archive className="size-icon-sm" aria-hidden="true" />
								)}
							</button>
						}
					/>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">
				{termination.isPending ? t("shell.archivingSession") : t("shell.archiveSession")}
			</TooltipContent>
		</Tooltip>
	) : undefined;

	return (
		<SessionCardView
			action={action}
			artifacts={
				interactive && onOpenArtifact ? (
					<BoardArtifactLinks artifacts={sessionArtifacts(session)} onOpen={onOpenArtifact} onOpenInspector={onOpenArtifacts} session={session} />
				) : undefined
			}
			branchAction={branchAction}
			branchLabel={sessionBranchPresentation(session).naming ? <BranchNamingLabel /> : undefined}
			branchIcon={<GitBranch aria-hidden="true" className="size-icon-2xs shrink-0" />}
			error={termination.error ?? undefined}
			externalLink={ProductExternalLink}
			footer={
				<>
					{footer}
				</>
			}
			interactive={interactive}
			labels={{
				formatTime: formatTimeCompact,
				intakeIssue: (id) => t("shell.intakeIssue", { id }),
				pr: pullRequestLabels(t),
				updatedAt: (timestamp: string) =>
					t("shell.lastMessageAt", { time: formatTimeCompact(timestamp) }),
			}}
			onOpen={onOpen}
			resource={resourcePresentation}
			overlay={terminationOverlay}
			prs={summaries.map((pr) => ({
				commentCount: pr.review.unresolvedBy.reduce((count, reviewer) => count + reviewer.count, 0),
				number: pr.number,
				reviewers: Array.from(
					new Map(
						pr.review.unresolvedBy.map((reviewer) => [reviewer.reviewerId, reviewer]),
					).values(),
				).map((reviewer) => ({
					avatarUrl: scmUserAvatarUrl(pr.provider, prBrowserUrl(pr), reviewer.reviewerId),
					id: reviewer.reviewerId,
				})),
				state: pr.state,
				url: prBrowserUrl(pr),
			}))}
			renderAvatar={(provider) => <AgentAvatar provider={provider} />}
			session={
				hideTerminatedStatus
					? hideTerminatedCardStatus(toBoardSessionPresentation(session))
					: toBoardSessionPresentation(session)
			}
			translate={translate}
		/>
	);
}

const BOARD_ARTIFACT_LIMIT = 2;
const BOARD_ARTIFACT_EXPANDED_LIMIT = 10;
const BOARD_ARTIFACT_HOVER_DELAY = 300;

/** The session's most recently updated artifacts, newest first. */
export function latestSessionArtifacts(artifacts: SessionArtifact[], limit = BOARD_ARTIFACT_LIMIT): SessionArtifact[] {
	return [...artifacts]
		.sort(
			(a, b) =>
				(Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0) || a.path.localeCompare(b.path),
		)
		.slice(0, limit);
}

function BoardArtifactLinks({
	artifacts,
	onOpen,
	onOpenInspector,
	session,
}: {
	artifacts: SessionArtifact[];
	onOpen: (path: string) => void;
	onOpenInspector?: () => void;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const [previewPath, setPreviewPath] = useState<string | null>(null);
	const suppressedPointer = useRef<{ x: number; y: number } | null>(null);
	const previewTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const listId = useId();
	const reduceMotion = useReducedMotion();
	const cancelPendingPreview = () => {
		if (previewTimer.current !== null) clearTimeout(previewTimer.current);
		previewTimer.current = null;
	};
	useEffect(() => () => {
		if (previewTimer.current !== null) clearTimeout(previewTimer.current);
	}, []);
	if (artifacts.length === 0) return null;
	const sorted = latestSessionArtifacts(artifacts, BOARD_ARTIFACT_EXPANDED_LIMIT);
	const latest = sorted.slice(0, expanded ? BOARD_ARTIFACT_EXPANDED_LIMIT : BOARD_ARTIFACT_LIMIT);
	return (
		<div
			className="flex min-w-0 flex-col gap-0.5 font-sans text-2xs"
			data-testid="board-session-artifacts"
			onPointerLeave={() => {
				cancelPendingPreview();
				suppressedPointer.current = null;
			}}
		>
			<div className="flex min-w-0 flex-col" id={listId}>
				<AnimatePresence initial={false}>
					{latest.map((artifact) => (
						<motion.div
							key={artifact.path}
							initial={{ height: 0, opacity: 0 }}
							animate={{ height: "auto", opacity: 1 }}
							exit={{ height: 0, opacity: 0 }}
							transition={{ duration: reduceMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
							className="flex min-w-0 flex-col overflow-hidden"
						>
							<HoverCard
								openDelay={BOARD_ARTIFACT_HOVER_DELAY}
								open={previewPath === artifact.path}
								onOpenChange={(open) => {
									if (open && !suppressedPointer.current) setPreviewPath(artifact.path);
									else if (!open) setPreviewPath((current) => current === artifact.path ? null : current);
								}}
							>
								<HoverCardTrigger asChild>
									<button
										aria-label={t("shell.openArtifactNamed", { name: artifact.name })}
										className="flex min-w-0 max-w-full items-center gap-1.5 self-start rounded-sm py-0.5 text-left text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
										onFocus={() => {
											cancelPendingPreview();
											suppressedPointer.current = null;
										}}
										onPointerLeave={cancelPendingPreview}
										onPointerMove={(event) => {
											const pointer = suppressedPointer.current;
											if (!pointer) return;
											cancelPendingPreview();
											if (Math.hypot(event.clientX - pointer.x, event.clientY - pointer.y) < 4) return;
											previewTimer.current = setTimeout(() => {
												previewTimer.current = null;
												suppressedPointer.current = null;
												setPreviewPath(artifact.path);
											}, BOARD_ARTIFACT_HOVER_DELAY);
										}}
										onClick={(event) => {
											event.stopPropagation();
											onOpen(artifact.path);
										}}
										type="button"
									>
										<File aria-hidden="true" className="size-icon-2xs shrink-0 text-muted-foreground" />
										<span className="truncate leading-none">{artifact.name}</span>
									</button>
								</HoverCardTrigger>
								<ArtifactPreviewCard artifact={artifact} session={session} />
							</HoverCard>
						</motion.div>
					))}
				</AnimatePresence>
			</div>
			{artifacts.length > BOARD_ARTIFACT_LIMIT ? (
				<div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
					<button
						aria-controls={listId}
						aria-expanded={expanded}
						className="self-start rounded-sm py-0.5 text-left text-muted-foreground hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={(event) => {
							event.stopPropagation();
							cancelPendingPreview();
							suppressedPointer.current = { x: event.clientX, y: event.clientY };
							setPreviewPath(null);
							setExpanded(!expanded);
						}}
						type="button"
					>
						{expanded ? t("shell.showLess") : t("shell.moreArtifacts", { count: artifacts.length - BOARD_ARTIFACT_LIMIT })}
					</button>
					{expanded && artifacts.length > BOARD_ARTIFACT_EXPANDED_LIMIT && onOpenInspector ? (
						<button
							className="rounded-sm py-0.5 text-left text-muted-foreground hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							onClick={(event) => {
								event.stopPropagation();
								onOpenInspector();
							}}
							type="button"
						>
							{t("shell.openArtifactsInInspector")}
						</button>
					) : null}
				</div>
			) : null}
		</div>
	);
}

function hideTerminatedCardStatus(session: BoardSessionPresentation): BoardSessionPresentation {
	const terminated =
		session.displayStatus === "Terminated" ||
		(session.status === "terminated" && !session.displayStatus);
	if (!terminated) return session;
	return {
		...session,
		displayStatus: undefined,
		statusPresentation: {
			className: "hidden",
			indicatorClassName: "hidden",
			label: "",
			tone: "transparent",
		},
	};
}

function pullRequestLabels(t: TFunction): BoardPullRequestLabels {
	return {
		progress: (progress) => pullRequestProgressLabel(progress, t),
		short: t("pr.short"),
		states: {
			closed: t("pr.state.closed"),
			draft: t("pr.state.draft"),
			merged: t("pr.state.merged"),
			open: t("pr.state.open"),
		},
	};
}

function pullRequestProgressLabel(
	{ closed, draft, merged, open, total }: BoardPullRequestProgress,
	t: TFunction,
): string {
	return [
		t("pr.progress.merged", { count: total, merged }),
		open > 0 ? t("pr.progress.open", { count: open }) : undefined,
		draft > 0 ? t("pr.progress.draft", { count: draft }) : undefined,
		closed > 0 ? t("pr.progress.closed", { count: closed }) : undefined,
	]
		.filter((part): part is string => part !== undefined)
		.join(" · ");
}

/** "1.4 GB · 82%" while working, just "240 MB" while idle: an idle agent is
 * always at 0% and printing it is noise. */
function toResourcePresentation(
	memory: SessionMemoryReading | undefined,
	working: boolean,
	tone: ChipTone | undefined,
	t: TFunction,
): (BoardUsagePresentation & { tone?: ChipTone }) | undefined {
	if (!memory || memory.rssBytes <= 0) return undefined;
	const size = formatMemory(memory.rssBytes);
	if (!working) {
		return { accessibleLabel: t("shell.sessionMemoryAria", { size }), compactLabel: size, tone };
	}
	const cpu = formatCPU(memory.cpuPercent);
	return {
		accessibleLabel: t("shell.sessionResourceAria", { size, cpu }),
		compactLabel: `${size} · ${cpu}`,
		tone,
	};
}

function ArchiveRestoreButton({
	label,
	onClick,
	isRestoring,
	isDisabled,
}: {
	label: string;
	onClick: (event: MouseEvent<HTMLButtonElement>) => void;
	isRestoring: boolean;
	isDisabled: boolean;
}) {
	const { t } = useTranslation();
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex">
					<button
						aria-label={label}
						className="grid size-control-board-sm shrink-0 place-items-center rounded-md text-passive transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent/50 disabled:cursor-not-allowed disabled:opacity-35"
						disabled={isDisabled}
						onClick={onClick}
						type="button"
					>
						<RotateCcw className={cn("size-icon-md", isRestoring && "animate-spin")} aria-hidden="true" />
					</button>
				</span>
			</TooltipTrigger>
			<TooltipContent side="top">
				{isRestoring ? t("shell.restoringSession") : t("shell.restoreSession")}
			</TooltipContent>
		</Tooltip>
	);
}

function ArchiveRestoreError({ message }: { message?: string }) {
	return message ? (
		<div className="border-t border-border px-2 py-1.5 text-2xs text-destructive" role="alert">
			{message}
		</div>
	) : null;
}

function CopyActionButton({
	copiedLabel,
	copyLabel,
	value,
}: {
	copiedLabel: string;
	copyLabel: string;
	value: string;
}) {
	const [copied, setCopied] = useState(false);
	const copiedTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(
		() => () => {
			if (copiedTimeoutRef.current !== null) clearTimeout(copiedTimeoutRef.current);
		},
		[],
	);
	const buttonLabel = copied ? copiedLabel : copyLabel;
	const copyValue = async (event: MouseEvent<HTMLButtonElement>) => {
		event.stopPropagation();
		try {
			await aoBridge.clipboard.writeText(value);
		} catch {
			return;
		}
		setCopied(true);
		if (copiedTimeoutRef.current !== null) clearTimeout(copiedTimeoutRef.current);
		copiedTimeoutRef.current = setTimeout(() => {
			setCopied(false);
			copiedTimeoutRef.current = null;
		}, 1_500);
	};
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={buttonLabel}
					className="inline-flex size-4 shrink-0 items-center justify-center rounded-sm text-passive transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
					onClick={(event) => void copyValue(event)}
					type="button"
				>
					{copied ? (
						<Check className="size-icon-2xs text-success" aria-hidden="true" />
					) : (
						<Copy className="size-icon-2xs" aria-hidden="true" />
					)}
				</button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{buttonLabel}</TooltipContent>
		</Tooltip>
	);
}
