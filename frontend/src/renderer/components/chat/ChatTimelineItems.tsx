import { AppLink } from "../AppLink";
import { useAgentAccountLabel } from "../../hooks/useAgentAccounts";
/**
 * Timeline entries for the Chat surface.
 *
 * One component per durable item shape, keyed by sequence so a streaming rewrite
 * updates a message in place instead of remounting it. Every component is a pure
 * function of a domain item: no lifecycle decisions, no capability checks, no
 * re-sorting. Those belong to the daemon.
 */

import { stagedAttachmentParts, attachmentURL, IMAGE_ATTACHMENT_PATH, splitInlineImagePaths } from "./messageAttachments";
import { ChatImage } from "./ChatImage";
import { useSentAttachmentPreview } from "../../lib/sent-attachment-previews";
import { CloudMessageAttachments } from "./CloudMessageAttachments";
import { splitCloudAttachmentReferences } from "../../lib/cloud-attachments";
import { createContext, Fragment, memo, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ClipboardEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { agentLabel } from "../../lib/agent-options";
import { recordedModelName } from "../../lib/agent-model-choices";
import { formatEffortLabel } from "../settings/EffortPicker";
import { useChatArtifactFiles, useChatSessionId } from "./chat-image-source";
import { ArtifactThumbnail } from "../ArtifactThumbnail";
import { splitTurnArtifacts, type TurnArtifact } from "../../lib/turn-artifacts";
import type { ArtifactRow } from "../../lib/artifacts";
import { useChatEntrance } from "./chat-entrance";
import { pageHidden } from "../../lib/page-hidden";
import { fetchActivityOutput } from "../../hooks/useConversation";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import {
	AlertTriangle,
	Bot,
	ArrowRight,
	ArrowRightLeft,
	Brain,
	ChevronDown,
	ChevronRight,
	CircleAlert,
	CircleCheck,
	CircleDot,
	CircleHelp,
	CircleStop,
	Clock,
	Copy,
	CornerDownLeft,
	CornerDownRight,
	File as FileIcon,
	FileDiff,
	FilePen,
	FilePlus,
	FileSearch,
	FileText,
	FlaskConical,
	FolderOpen,
	FolderPlus,
	Gauge,
	GitBranch,
	GitCommitHorizontal,
	GitPullRequest,
	Globe,
	Hammer,
	History,
	KeyRound,
	Keyboard,
	ListChecks,
	Loader2,
	Package,
	Paintbrush,
	Pencil,
	Plug,
	RotateCw,
	Search,
	ShieldCheck,
	ShieldQuestion,
	ShieldX,
	SquareTerminal,
	Trash2,
	Undo2,
	User,
	Wrench,
	X,
} from "@/components/app-icons";

/** The mark beside a command, tool or file-edit step, by what the step did. */
const stepIcon: Record<StepIcon, typeof SquareTerminal> = {
	list: FolderOpen,
	read: FileText,
	search: Search,
	find: FileSearch,
	git: GitBranch,
	commit: GitCommitHorizontal,
	pr: GitPullRequest,
	issue: CircleDot,
	web: Globe,
	"folder-new": FolderPlus,
	"file-new": FilePlus,
	write: FilePen,
	move: ArrowRightLeft,
	copy: Copy,
	delete: Trash2,
	test: FlaskConical,
	install: Package,
	build: Hammer,
	check: CircleCheck,
	format: Paintbrush,
	wait: Clock,
	tool: Wrench,
	run: SquareTerminal,
};

/** Fixed icon column, matching the prototype's row anatomy. */
const activityIcon: Record<ActivityKind, typeof SquareTerminal> = {
	command: SquareTerminal,
	file_change: FileDiff,
	plan: ListChecks,
	reasoning: Brain,
	approval: ShieldQuestion,
	usage: Gauge,
	error: AlertTriangle,
	system: CircleAlert,
	mcp_tool: Plug,
	auto_review: ShieldCheck,
	user_input: Keyboard,
	ao_action: Bot,
};
import { cn } from "../../lib/utils";
import { caretNotation, stripAnsi } from "../../lib/ansi";
import { getApiBaseUrl } from "../../lib/api-client";
import { readArtifactRef, readRenderRef } from "../../lib/render-frame";
import { aoToolView, mcpToolArguments, mcpToolName, type AOToolView } from "../../lib/mcp-tool";
import { isWebLink, openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { useComposerTokenRenderer } from "./sentComposerTokens";
import { ActivityTitle, ChatMarkdown, OriginPreviewMarkdown, SessionLabelLink, SessionLinkedText, UserMarkdown } from "./ChatMarkdown";
import { HighlightedCode } from "./HighlightedCode";
import { CopyButton } from "./CopyButton";
import { CollapsibleHumanBody } from "./CollapsibleHumanBody";
import { LatticeLoader } from "./LatticeLoader";
import { ScriptRunPanel } from "../ScriptRunPanel";
import { HumanMessageEditor } from "./HumanMessageEditor";
import { RenderFrame } from "./RenderFrame";
import { ConversationBranchNavigator } from "./ConversationBranchNavigator";
import { ConversationContentItems } from "./ConversationContentItems";
import { AttachmentStrip, StagedAttachmentItems } from "./StagedAttachmentItems";
import { UsageLimitMarker, UsageLimitNotice } from "./UsageLimitNotice";
import {
	ACTIVITY_SUMMARY_BUTTON_CLASS,
	commandBinaryLabel,
	isNonzeroCommandExit,
} from "./activity-command";
import { activityCommandStep, activityToolStep, fileBasename, pastTenseHoldsOnFailure, type CommandStep, type StepIcon } from "./activity-step";
import { actionDetailLines, describeConversationActivity } from "./activity-action";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import {
	fileChangeFiles,
	reviewedPaths,
	type ActivityKind,
	type ConversationActivity,
	type ConversationBranchPoint,
	type ConversationMessage,
	type ConversationUsageLimit,
	type DecisionOption,
	type DeliveryState,
	type DiffStatus,
	type FileChangeFile,
	type ConversationItem,
	type TurnArtifactChange,
	type TurnDiff,
} from "../../types/conversation";
import { resolveTurnFilePath, turnFileOpenPath, turnPathHints } from "../../lib/turn-file-open-path";

const timeFormatter = new Intl.DateTimeFormat(undefined, {
	hour: "2-digit",
	minute: "2-digit",
	hourCycle: "h23",
});
const dateFormatter = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "numeric",
	year: "numeric",
});

const ORIGIN_REPORT_COLLAPSE_AT = 600;
const ORIGIN_REPORT_PREVIEW_LENGTH = 240;

function originReportPreview(text: string): string {
	const cut = text.slice(0, ORIGIN_REPORT_PREVIEW_LENGTH);
	const lastCompleteLine = cut.lastIndexOf("\n");
	const preview = lastCompleteLine > 0 ? cut.slice(0, lastCompleteLine) : cut;
	return `${preview.trimEnd()}…`;
}

/**
 * Each update reveals the share of the backlog that decays over the settle time, so
 * bursts of provider text spread across the gap before the next one instead of landing
 * at once. The settle time follows how far apart the bursts arrive: a stream that
 * lands every ~50ms settles in STREAM_SETTLE_MS, while one that lands a second at a
 * time (a slow link, a polled transcript) is spread over most of that second rather
 * than revealed in a fraction of it and then left still. Updates are capped at ~30 Hz:
 * every one re-parses the message's markdown.
 */
const STREAM_SETTLE_MS = 140;
const STREAM_MAX_SETTLE_MS = 700;
// The share of the gap between bursts the reveal is spread over; less than all of it,
// so text stays close behind the stream instead of trailing a whole burst.
const STREAM_SETTLE_GAP_SHARE = 0.6;
const STREAM_MAX_GAP_MS = 1_500;
const STREAM_MIN_FRAME_MS = 32;
const STREAM_MAX_FRAME_DELTA_MS = 100;
// Long open Markdown blocks are reparsed on each reveal. Beyond this size,
// coalesce provider snapshots rather than adding intermediate renders.
const STREAM_SMOOTH_MAX_CHARS = 4000;
const STREAM_LONG_UPDATE_MS = 100;
// No frame for this long means the tab was occluded or throttled: nobody watched the
// animation, so show the current text instead of replaying it.
const STREAM_STALL_MS = 1000;
// Once the reply is done, what is still unrevealed plays out quickly rather than
// landing at once; a backlog beyond this is shown whole.
const STREAM_FINISH_SETTLE_MS = 90;
const STREAM_FINISH_MAX_BACKLOG = 2000;
const STREAM_GRAPHEME_SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

function streamGraphemes(text: string): string[] {
	return Array.from(STREAM_GRAPHEME_SEGMENTER.segment(text), ({ segment }) => segment);
}

function reconciledStreamPrefix(visibleText: string, targetGraphemes: string[]) {
	let boundary = 0;
	let count = 0;
	for (const grapheme of targetGraphemes) {
		const nextBoundary = boundary + grapheme.length;
		if (nextBoundary > visibleText.length) break;
		boundary = nextBoundary;
		count++;
	}
	return { text: visibleText.slice(0, boundary), count };
}

/**
 * Whether replies pace their reveal to how far apart bursts arrive and play
 * out their last burst (Cloud chat, whose text can arrive a poll at a time).
 * Elsewhere a reply settles in STREAM_SETTLE_MS and shows whole once done.
 */
export const AdaptiveStreamPacingContext = createContext(false);

function useSmoothStreamingText(message: ConversationMessage): string {
	const adaptive = useContext(AdaptiveStreamPacingContext);
	const [reducedMotion, setReducedMotion] = useState(
		() => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches,
	);
	// Set while a finished reply's last burst is still playing out.
	const [finishing, setFinishing] = useState(false);
	const smooth = (message.streaming || finishing) && !reducedMotion && message.text.length <= STREAM_SMOOTH_MAX_CHARS;
	// A snapshot can first reach the renderer after the provider has already emitted
	// text. Keep that first durable burst visible; only later deltas need smoothing.
	const [visibleText, setVisibleText] = useState(() => message.text);
	const visibleRef = useRef(visibleText);
	const targetRef = useRef(message.text);
	const targetGraphemes = useMemo(
		() => smooth ? streamGraphemes(message.text) : [],
		[message.text, smooth],
	);
	const visibleGraphemeCountRef = useRef(targetGraphemes.length);
	const targetGraphemesRef = useRef(targetGraphemes);
	const messageIdRef = useRef(message.id);
	const frameRef = useRef<number | undefined>(undefined);
	const lastFrameAtRef = useRef<number | undefined>(undefined);
	const snapshotTimerRef = useRef<number | undefined>(undefined);
	const settleMsRef = useRef(STREAM_SETTLE_MS);
	const lastGrowthAtRef = useRef<number | undefined>(undefined);
	const gapMsRef = useRef<number | undefined>(undefined);
	const finishingRef = useRef(false);

	useEffect(() => {
		const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
		const update = () => setReducedMotion(mediaQuery.matches);
		mediaQuery.addEventListener("change", update);
		return () => mediaQuery.removeEventListener("change", update);
	}, []);

	const cancelDrain = useCallback(() => {
		if (snapshotTimerRef.current !== undefined) {
			window.clearTimeout(snapshotTimerRef.current);
			snapshotTimerRef.current = undefined;
		}
		if (frameRef.current !== undefined) {
			window.cancelAnimationFrame(frameRef.current);
			frameRef.current = undefined;
		}
		lastFrameAtRef.current = undefined;
	}, []);

	const endFinishing = useCallback(() => {
		if (!finishingRef.current) return;
		finishingRef.current = false;
		setFinishing(false);
	}, []);

	const scheduleDrain = useCallback(() => {
		if (frameRef.current !== undefined) return;
		const scheduledAt = performance.now();

		const tick = (now: number) => {
			frameRef.current = undefined;
			const target = targetGraphemesRef.current;
			const currentCount = visibleGraphemeCountRef.current;
			const backlog = target.length - currentCount;
			if (backlog <= 0) {
				lastFrameAtRef.current = undefined;
				endFinishing();
				return;
			}

			if (document.hidden || now - (lastFrameAtRef.current ?? scheduledAt) > STREAM_STALL_MS) {
				visibleRef.current = targetRef.current;
				visibleGraphemeCountRef.current = target.length;
				setVisibleText(targetRef.current);
				cancelDrain();
				endFinishing();
				return;
			}

			const previousFrameAt = lastFrameAtRef.current;
			if (previousFrameAt !== undefined && now - previousFrameAt < STREAM_MIN_FRAME_MS) {
				frameRef.current = window.requestAnimationFrame(tick);
				return;
			}
			lastFrameAtRef.current = now;
			const elapsedMs = Math.min(STREAM_MAX_FRAME_DELTA_MS, previousFrameAt === undefined ? STREAM_MIN_FRAME_MS : now - previousFrameAt);
			const settleMs = finishingRef.current ? STREAM_FINISH_SETTLE_MS : settleMsRef.current;
			const count = Math.ceil(backlog * (1 - Math.exp(-elapsedMs / settleMs)));
			const nextCount = Math.min(target.length, currentCount + count);
			const next = visibleRef.current + target.slice(currentCount, nextCount).join("");
			visibleRef.current = next;
			visibleGraphemeCountRef.current = nextCount;
			setVisibleText(next);
			if (nextCount < target.length) frameRef.current = window.requestAnimationFrame(tick);
			else {
				lastFrameAtRef.current = undefined;
				endFinishing();
			}
		};

		frameRef.current = window.requestAnimationFrame(tick);
	}, [cancelDrain, endFinishing]);

	useEffect(() => {
		const showWhole = () => {
			cancelDrain();
			visibleRef.current = message.text;
			visibleGraphemeCountRef.current = targetGraphemes.length;
			setVisibleText(message.text);
			endFinishing();
		};
		if (message.id !== messageIdRef.current) {
			cancelDrain();
			endFinishing();
			messageIdRef.current = message.id;
			targetRef.current = message.text;
			targetGraphemesRef.current = targetGraphemes;
			lastGrowthAtRef.current = undefined;
			gapMsRef.current = undefined;
			settleMsRef.current = STREAM_SETTLE_MS;
			visibleRef.current = message.text;
			visibleGraphemeCountRef.current = targetGraphemes.length;
			setVisibleText(message.text);
			return;
		}

		const grew = message.text !== targetRef.current && message.text.startsWith(targetRef.current);
		targetRef.current = message.text;
		targetGraphemesRef.current = targetGraphemes;
		// A hidden window draws no frames, so a reveal would stall and replay once the
		// reader is back. Show what arrived now; the fade treats it as settled too.
		if (reducedMotion || pageHidden()) {
			showWhole();
			return;
		}
		if (!message.streaming) {
			// The reply is done. A short remainder plays out; anything else, a reply
			// too long to reveal smoothly, or one that was never being revealed,
			// shows whole.
			const remaining = message.text.startsWith(visibleRef.current) ? message.text.length - visibleRef.current.length : -1;
			if (!adaptive || remaining <= 0 || remaining > STREAM_FINISH_MAX_BACKLOG || message.text.length > STREAM_SMOOTH_MAX_CHARS) {
				showWhole();
				return;
			}
			if (!finishingRef.current) {
				finishingRef.current = true;
				setFinishing(true);
				// The graphemes are computed once `finishing` is set.
				return;
			}
		}
		if (adaptive && grew && message.streaming) {
			// Spread each burst over most of the gap since the last one.
			const now = performance.now();
			if (lastGrowthAtRef.current !== undefined) {
				// A pause (the agent running a tool) is not a burst rhythm; past a
				// second and a half it counts as no longer than that.
				const gap = Math.min(now - lastGrowthAtRef.current, STREAM_MAX_GAP_MS);
				gapMsRef.current = gapMsRef.current === undefined ? gap : gapMsRef.current * 0.6 + gap * 0.4;
				settleMsRef.current = Math.min(STREAM_MAX_SETTLE_MS, Math.max(STREAM_SETTLE_MS, gapMsRef.current * STREAM_SETTLE_GAP_SHARE));
			}
			lastGrowthAtRef.current = now;
		}
		// A provider correction or rollback can replace the current prefix. In that
		// case the durable snapshot is authoritative and should be shown immediately.
		if (!message.text.startsWith(visibleRef.current)) {
			showWhole();
			return;
		}
		if (message.text.length > STREAM_SMOOTH_MAX_CHARS) {
			if (frameRef.current !== undefined) cancelDrain();
			if (visibleRef.current === message.text) return;
			if (visibleRef.current.length <= STREAM_SMOOTH_MAX_CHARS) {
				visibleRef.current = message.text;
				setVisibleText(message.text);
			} else if (snapshotTimerRef.current === undefined) {
				snapshotTimerRef.current = window.setTimeout(() => {
					snapshotTimerRef.current = undefined;
					visibleRef.current = targetRef.current;
					setVisibleText(targetRef.current);
				}, STREAM_LONG_UPDATE_MS);
			}
			return;
		}
		// A later combining mark or ZWJ can merge the last visible grapheme into a
		// different target grapheme. Reconcile that trailing fragment before using
		// the old grapheme count, otherwise the drain can skip the merged suffix.
		const reconciled = reconciledStreamPrefix(visibleRef.current, targetGraphemesRef.current);
		visibleGraphemeCountRef.current = reconciled.count;
		if (reconciled.text !== visibleRef.current) {
			visibleRef.current = reconciled.text;
			setVisibleText(reconciled.text);
		}
		if (visibleGraphemeCountRef.current < targetGraphemesRef.current.length) scheduleDrain();
		else endFinishing();
	}, [adaptive, cancelDrain, endFinishing, message.id, message.text, message.streaming, reducedMotion, scheduleDrain, targetGraphemes]);

	useEffect(
		() => cancelDrain,
		[cancelDrain],
	);

	return visibleText;
}

/** A status message followed by a full-width rule, with no text inside the rule. */
function TwoRowTimelineMarker({
	message,
	detail,
	detailTone = "text-muted-foreground/70",
	wrapDetail = false,
}: {
	message: string;
	detail?: string;
	detailTone?: string;
	/** The detail is an instruction the reader needs whole, so it wraps instead of truncating. */
	wrapDetail?: boolean;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-1 py-1">
			<div className={cn("flex min-w-0 items-baseline gap-x-2 gap-y-0.5 text-xs text-muted-foreground/70", wrapDetail && "flex-wrap")}>
				<span className="shrink-0">{message}</span>
				{detail ? (
					<span
						className={cn("min-w-0", wrapDetail ? "text-pretty" : "truncate", detailTone)}
						title={detail}
					>
						{detail}
					</span>
				) : null}
			</div>
			<span aria-hidden="true" className="h-px w-full bg-border" />
		</div>
	);
}

export function CompactionMarker({ activity }: { activity: ConversationActivity }) {
	const reclaimed = activity.detail?.tokensReclaimed;
	const after = activity.detail?.tokensAfter;
	const contextWindow = activity.detail?.contextWindow;
	const detail = reclaimed ? `−${formatTokens(reclaimed)}` : undefined;
	const fullness = after && contextWindow ? `${Math.round((after / contextWindow) * 100)}% full` : undefined;

	return (
		<TwoRowTimelineMarker
			message="The conversation history was compacted"
			detail={[detail, fullness].filter(Boolean).join(" · ") || undefined}
		/>
	);
}

/** The remote host a chat's session runs on, whose catalogs name its models. */
export const ChatHostId = createContext<string | undefined>(undefined);

/** One side of a handoff: its model by name when one was recorded, else the agent's name. */
function HandoffSide({ harness, model, effort, compactWindow, bright }: { harness: string; model?: string; effort?: string; compactWindow?: string; bright?: boolean }) {
	const { t } = useTranslation();
	// The marker stores the model id. Its name comes from the agent's catalog on the session's host.
	const hostId = useContext(ChatHostId);
	const catalog = useQuery({ ...agentModelsQueryOptions(harness, "", hostId), enabled: Boolean(model) }).data;
	const name = model ? recordedModelName(harness, model, catalog?.models ?? []) : agentLabel(harness);
	return (
		<span className={cn("flex min-w-0 items-center gap-1.5", bright ? "text-foreground" : undefined)}>
			<span className="truncate">{name.replace(/\s*\b1M\b/g, "")}</span>
			{effort ? <span className="shrink-0 text-muted-foreground">{formatEffortLabel(effort, t)}</span> : null}
			{compactWindow ? <span className="shrink-0 text-muted-foreground" title="Compacts on its own at this size">· {compactWindow.toUpperCase()}</span> : null}
		</span>
	);
}

/** Where one agent handed the conversation to another. */
export function ProviderSwitchMarker({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const detail = activity.detail;
	const switching = Boolean(detail?.switching);
	const named = Boolean(detail?.sourceHarness && detail.targetHarness);
	// A switched row only seconds old is the one just made, so it opens from closed; older ones start open.
	const [open, setOpen] = useState(() => switching || !(Date.now() - Date.parse(activity.createdAt) < 8000));
	useEffect(() => {
		if (switching || open) return;
		let inner = 0;
		const outer = requestAnimationFrame(() => { inner = requestAnimationFrame(() => setOpen(true)); });
		return () => { cancelAnimationFrame(outer); cancelAnimationFrame(inner); };
	}, [switching, open]);
	const revealed = !switching && open;
	const label = switching
		? t("agentSwitch.switching")
		: named
			? t("agentSwitch.switchedFromTo", { from: agentLabel(detail?.sourceHarness ?? ""), to: agentLabel(detail?.targetHarness ?? "") })
			: t("agentSwitch.switched");
	return (
		<div role="separator" aria-label={label} aria-busy={switching || undefined} className="flex min-w-0 items-center gap-3 py-2 text-[11px] text-muted-foreground">
			<span aria-hidden="true" className="h-px min-w-4 flex-1 bg-border" />
			<div className="flex min-w-0 items-center">
				<span className={cn("inline-block shrink-0", switching ? "chat-working-shimmer" : undefined)}>
					{switching ? t("agentSwitch.switching") : t("agentSwitch.switched")}
				</span>
				{named ? (
					// Closed while the switch runs, then its width grows from 0 to its own.
					<div
						className={cn(
							"grid min-w-0 transition-[grid-template-columns] duration-300 ease-out motion-reduce:transition-none",
							revealed ? "grid-cols-[1fr]" : "grid-cols-[0fr]",
						)}
					>
						<div className="min-w-0 overflow-hidden">
							<span className="flex w-max shrink-0 items-center gap-3 whitespace-nowrap pl-3">
								<HandoffSide harness={detail?.sourceHarness ?? ""} model={detail?.sourceModel} />
								<ArrowRight aria-hidden="true" className="size-3 shrink-0" />
								<HandoffSide harness={detail?.targetHarness ?? ""} model={detail?.targetModel} effort={detail?.targetEffort} compactWindow={detail?.targetAutoCompactWindow} bright />
							</span>
						</div>
					</div>
				) : null}
			</div>
			<span aria-hidden="true" className="h-px min-w-4 flex-1 bg-border" />
		</div>
	);
}

export interface TurnOutcomeRetryControl {
	onRetry: () => void;
	pending?: boolean;
	error?: string;
	disabled?: boolean;
}

/** Where the user continued a stopped turn: a marker in place of the continue message. */
export function ContinuedMarker() {
	return (
		// Closer to the stopped turn above and the turn it starts below: the marker joins
		// them, so it should not sit apart from either.
		<div className="-mb-1 -mt-2.5">
			<TwoRowTimelineMarker message="Continued" />
		</div>
	);
}

const TURN_OUTCOME_COPY = {
	recovered: {
		label: "This turn was recovered from an earlier session",
		icon: History,
		tone: "text-muted-foreground",
		iconTone: "text-muted-foreground/70",
	},
	interrupted: {
		label: "The agent was interrupted by you",
		icon: CircleStop,
		tone: "text-muted-foreground",
		iconTone: "text-muted-foreground/70",
	},
	failed: {
		label: "The agent ran into a problem",
		icon: CircleAlert,
		tone: "font-medium text-destructive",
		iconTone: "text-destructive",
	},
	// The controller went away mid-turn, so nothing reported how it ended.
	unknown: {
		label: "The agent stopped before this turn finished",
		icon: CircleHelp,
		tone: "text-muted-foreground",
		iconTone: "text-muted-foreground/70",
	},
} as const;

/**
 * How a turn ended when it did not end with an answer: in the answer's place, and
 * closing on the same action row a finished answer gets.
 *
 * It used to be three shapes for one ending — a status line drawn across a rule,
 * retry off at the far right, undo floating above on a row of its own. Now it reads
 * like a short reply (mark, what happened, the provider's reason) so a stopped or
 * failed turn keeps the outline of every other turn.
 */
export function TurnOutcome({
	state,
	error,
	failureCode,
	onConfigure,
	retry,
	usageLimit,
	copyText,
	onRollback,
	rollbackDisabled,
	timestamp,
}: {
	state: "recovered" | "interrupted" | "failed" | "unknown";
	error?: string;
	failureCode?: "provider_key_missing";
	onConfigure?: () => void;
	usageLimit?: ConversationUsageLimit;
} & TurnActionsProps) {
	// A usage limit has its own notice, whose resume action stands in for Retry. The
	// turn still closes on the shared row, so copy and undo stay where they always are.
	if (state === "failed" && usageLimit) {
		return (
			<div className="group/message flex min-w-0 flex-col gap-2" data-turn-outcome={state}>
				<UsageLimitMarker />
				{error ? <div className="whitespace-pre-wrap wrap-anywhere text-sm leading-relaxed text-foreground">{linkifiedProviderErrorText(error)}</div> : null}
				{retry ? (
					<UsageLimitNotice
						usageLimit={usageLimit}
						onResume={retry.onRetry}
						pending={retry.pending}
						disabled={retry.disabled}
						error={retry.error}
					/>
				) : null}
				<TurnActions
					copyText={copyText}
					onRollback={onRollback}
					rollbackDisabled={rollbackDisabled}
					timestamp={timestamp}
				/>
			</div>
		);
	}
	const copy = TURN_OUTCOME_COPY[state];
	const Icon = copy.icon;
	const needsDeepSeekKey = state === "failed" && failureCode === "provider_key_missing" && onConfigure;
	return (
		// A stopped turn is three short lines, so they sit close: 4px under "Worked for",
		// as an answer's action row does, not the turn's 10px.
		<div className="group/message -mt-1.5 flex min-w-0 flex-col gap-1.5" data-turn-outcome={state}>
			<div className="flex min-w-0 items-start gap-2">
				<Icon aria-hidden="true" className={cn("mt-[3px] size-3.5 shrink-0", copy.iconTone)} />
				<span className={cn("min-w-0 text-sm leading-5", copy.tone)}>
					{needsDeepSeekKey ? "DeepSeek needs an API key for this model route" : copy.label}
				</span>
			</div>
			{needsDeepSeekKey ? (
				<>
					<p className="pl-[22px] text-sm text-foreground">Set up the key in DeepSeek Harness, then retry this turn.</p>
					<Button type="button" variant="primary" size="sm" onClick={onConfigure} className="ml-[22px] w-fit">Set up DeepSeek</Button>
					{error ? (
						<details className="pl-[22px] text-sm text-muted-foreground">
							<summary className="cursor-pointer">Technical details</summary>
							<div className="whitespace-pre-wrap wrap-anywhere pt-1">{linkifiedProviderErrorText(error)}</div>
						</details>
					) : null}
				</>
			) : error ? (
				<div
					className={cn(
						"whitespace-pre-wrap wrap-anywhere leading-relaxed text-muted-foreground",
						// A provider's failure text is code-like (status lines, request ids, URLs):
						// it sits in a quiet mono block like command output, spanning the turn's
						// width under the title. A stop or recovery note stays in the turn's own
						// voice, indented with the title.
						state === "failed" ? "rounded-md bg-surface px-3 py-2 font-mono text-xs" : "pl-[22px] text-sm",
					)}
				>
					{linkifiedProviderErrorText(error)}
				</div>
			) : null}
			<TurnActions
				className={error || needsDeepSeekKey ? undefined : "-mt-1"}
				retry={retry}
				copyText={copyText}
				onRollback={onRollback}
				rollbackDisabled={rollbackDisabled}
				timestamp={timestamp}
			/>
		</div>
	);
}

interface TurnActionsProps {
	/** Present only when this failed turn is eligible for a new attempt. */
	retry?: TurnOutcomeRetryControl;
	/** The answer's stored markdown, so a paste gives back what the agent wrote. */
	copyText?: string;
	/** Discard this turn and everything after it. */
	onRollback?: () => void;
	/** Keep rollback mounted but inert while another turn is active. */
	rollbackDisabled?: boolean;
	/** When the answer was written, revealed on hover of the enclosing `group/message`. */
	timestamp?: string;
}

/**
 * The row a turn closes on: retry, copy, roll back, then the time on hover. How
 * long the turn ran heads it instead, in "Worked for".
 *
 * Every ending shares it — a finished answer, a tool-only turn, a stopped or failed
 * one — so the controls are always in the same place. The negative margin is on the
 * row rather than its first button, so the leading glyph lines up with the text
 * above whichever control comes first.
 */
export function TurnActions({
	retry,
	copyText,
	onRollback,
	rollbackDisabled = false,
	timestamp,
	className,
}: TurnActionsProps & { className?: string }) {
	if (!retry && copyText === undefined && !onRollback) return null;
	const time = timestamp ? formatMessageTimestamp(timestamp) : "";
	return (
		<div className={cn("flex min-w-0 flex-col gap-1", className)}>
			<div className="-ml-1.5 flex h-7 items-center gap-0.5" data-turn-actions="">
				{retry ? (
					<Button
						type="button"
						variant="quiet"
						size="sm"
						onClick={retry.onRetry}
						disabled={retry.pending || retry.disabled}
						aria-label="Retry this turn"
						title={retry.error ?? (retry.disabled ? "Wait for the current turn to finish" : "Send this prompt again as a new turn")}
						data-testid="retry-turn"
					>
						<RotateCw
							aria-hidden="true"
							className={cn("size-3", retry.pending && "animate-spin motion-reduce:animate-none")}
						/>
						{retry.pending ? "Retrying…" : "Retry"}
					</Button>
				) : null}
				{copyText !== undefined ? (
					<div className="size-7 shrink-0">
						<CopyButton
							text={copyText}
							label="Copy message as markdown"
							compact
						/>
					</div>
				) : null}
				{onRollback ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Button
								type="button"
								variant="quiet"
								size="icon-sm"
								onClick={onRollback}
								disabled={rollbackDisabled}
								aria-label="Roll back to here"
							>
								<Undo2 aria-hidden="true" className="size-3.5" />
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">Roll back to here</TooltipContent>
					</Tooltip>
				) : null}
				{time ? (
					<span
						className="w-auto shrink-0 px-1 text-caption tabular-nums text-muted-foreground/75 opacity-0 transition-opacity duration-150 ease-out group-hover/message:opacity-100 group-focus-within/message:opacity-100 motion-reduce:transition-none"
						aria-label={`Sent ${time}`}
					>
						{time}
					</span>
				) : null}
			</div>
			{retry?.error ? (
				<p role="alert" className="text-pretty text-xs leading-snug text-destructive">
					{retry.error}
				</p>
			) : null}
		</div>
	);
}

function formatTokens(tokens: number): string {
	if (tokens < 1000) return `${tokens}`;
	return `${(tokens / 1000).toFixed(1)}k`;
}

/**
 * The bubble's text renderer: inline image chips when the message has images,
 * nothing otherwise, so a message without them keeps UserMarkdown's memo.
 */
function inlineImageRenderer(attachments: string[], sessionId: string, apiBaseUrl: string | null) {
	if (apiBaseUrl === null || !attachments.some((path) => IMAGE_ATTACHMENT_PATH.test(path))) return undefined;
	return (text: string) => (
		<ProseWithInlineImages text={text} attachments={attachments} sessionId={sessionId} apiBaseUrl={apiBaseUrl} renderText={(plain) => plain} />
	);
}

/**
 * The bubble's text renderer: skills and files it sent as chips in the accent
 * color, and inline image chips. Undefined when neither applies, which keeps
 * UserMarkdown's memo; stable while the message and its attachments are.
 */
function useUserTextRenderer(sessionId: string, body: string, attachments: string[], apiBaseUrl: string | null) {
	const renderTokens = useComposerTokenRenderer(sessionId, body);
	const attachmentKey = attachments.join("\n");
	return useMemo(() => {
		const renderImages = inlineImageRenderer(attachmentKey ? attachmentKey.split("\n") : [], sessionId, apiBaseUrl);
		return renderTokens ? (text: string) => renderTokens(text, renderImages) : renderImages;
	}, [renderTokens, attachmentKey, sessionId, apiBaseUrl]);
}

// A list, quote or code block at either end of a message sits flush with the
// bubble's padding instead of adding its own outer margin.
const USER_BODY_CLASS = "break-words text-pretty [&>:first-child]:mt-0 [&>:last-child]:mb-0";

const collapseWhitespace = (text: string) => text.replace(/\s+/g, "");

/**
 * Copying the whole of a rendered message gives back what the person typed, list
 * markers and backticks included, as the Copy button does. A partial selection
 * copies what it shows.
 */
function copyWholeMessageAsTyped(event: ClipboardEvent<HTMLDivElement>, typed: string) {
	const selected = window.getSelection()?.toString() ?? "";
	if (!selected.trim() || collapseWhitespace(selected) !== collapseWhitespace(event.currentTarget.textContent ?? "")) return;
	event.preventDefault();
	event.clipboardData.setData("text/plain", typed);
}

/**
 * Prose whose inline staged image paths (written by the composer's image chips)
 * render as chips that open the image. "Image N" counts images in attachment
 * order, the same label the attachment row and the composer use.
 */
function ProseWithInlineImages({
	text,
	attachments,
	sessionId,
	apiBaseUrl,
	renderText,
}: {
	text: string;
	attachments: string[];
	sessionId: string;
	apiBaseUrl: string | null;
	renderText: (text: string) => ReactNode;
}) {
	const { t } = useTranslation();
	const images = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path));
	if (apiBaseUrl === null || images.length === 0) return renderText(text);
	return splitInlineImagePaths(text, (path) => images.includes(path)).map((segment, index) =>
		segment.path === undefined ? (
			<Fragment key={index}>{renderText(segment.text)}</Fragment>
		) : (
			<InlineStagedImage
				key={index}
				path={segment.path}
				sessionId={sessionId}
				apiBaseUrl={apiBaseUrl}
				alt={t("chat.image.numbered", { index: images.indexOf(segment.path) + 1 })}
			/>
		),
	);
}

/** An inline image chip, drawn from the composer's copy while the file was just sent. */
function InlineStagedImage({ path, sessionId, apiBaseUrl, alt }: { path: string; sessionId: string; apiBaseUrl: string; alt: string }) {
	// A local copy that fails to draw hands the chip to the daemon's copy.
	const local = useSentAttachmentPreview(path);
	return <ChatImage inline src={local.url ?? attachmentURL(apiBaseUrl, sessionId, path)} alt={alt} onError={local.onError} />;
}

/** Collapse the home directory so a long absolute path does not eat the row. */
function shortenPaths(text: string): string {
	return text.replace(/\/(?:Users|home)\/[^/\s]+/g, "~");
}

function formatDuration(ms: number): string {
	// Status labels are intentionally discrete: start at one second and advance
	// in whole seconds so the live and settled rows never show fractional time.
	if (ms < 60_000) return `${Math.max(1, Math.floor(ms / 1000))}s`;
	const totalMinutes = Math.floor(ms / 60_000);
	if (totalMinutes < 60) return `${totalMinutes}m`;
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
}

function formatDecisionDuration(ms: number): string {
	if (ms < 1_000) return `${Math.max(0, Math.round(ms))}ms`;
	return `${(ms / 1_000).toFixed(1)}s`;
}

/**
 * The step rows' icon column (ACTIVITY_SUMMARY_BUTTON_CLASS: a 12px icon at the
 * turn's edge, then a 6px gap). The live row, its setup checklist and their marks
 * use the same box, so a mark sits on the step icons' left edge and the label
 * starts where the step labels do. The lattice is 13px across, a pixel wider than
 * the slot: it starts at the slot's left edge and overhangs into the gap. Centred,
 * it would sit half a pixel outside the column, where the live row clips it.
 */
export const STEP_MARK_SLOT_CLASS = "flex size-3 shrink-0 items-center justify-start";

/**
 * A running setup step's mark: the live row's lattice, so setup and work share one
 * "working" signal, in the step icons' slot (STEP_MARK_SLOT_CLASS).
 */
export function ResponseSpinner() {
	return (
		<span aria-hidden="true" data-testid="response-spinner" className={cn(STEP_MARK_SLOT_CLASS, "text-muted-foreground")}>
			<LatticeLoader />
		</span>
	);
}

/**
 * The live turn's working mark: a lattice of dots with a trail running round it
 * (see LatticeLoader), in the step icons' slot (STEP_MARK_SLOT_CLASS). Decorative
 * here, since the label and timer beside it carry the meaning.
 */
export function LiveWorkingMark() {
	return <ResponseSpinner />;
}

export function formatTime(iso: string): string {
	const parsed = new Date(iso);
	return Number.isNaN(parsed.getTime()) ? "" : timeFormatter.format(parsed);
}

function formatMessageTimestamp(iso: string, now = new Date()): string {
	const parsed = new Date(iso);
	if (Number.isNaN(parsed.getTime())) return "";

	const messageDay = new Date(parsed.getFullYear(), parsed.getMonth(), parsed.getDate()).getTime();
	const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
	const daysAgo = Math.round((today - messageDay) / 86_400_000);
	if (daysAgo === 0) return timeFormatter.format(parsed);
	if (daysAgo === 1) return `Yesterday · ${timeFormatter.format(parsed)}`;
	return dateFormatter.format(parsed);
}

/* -------------------------------------------------------------------------- */
/* messages                                                                    */
/* -------------------------------------------------------------------------- */

/** The user's bubble. A queued one reads as not-yet-sent rather than as sent-and-ignored. */
export function humanBubbleClass(queued?: boolean): string {
	return cn(
		"cursor-chat-human-message w-fit max-w-[min(78%,560px)] rounded-[10px] px-3 py-2.5 text-sm leading-[1.55]",
		queued ? "border border-dashed border-border-strong bg-transparent text-muted-foreground" : "bg-raised text-foreground",
	);
}

export function MessageTimestamp({ createdAt }: { createdAt: string }) {
	return (
		<span className="shrink-0 px-0.5 text-caption tabular-nums text-muted-foreground/75" aria-label={`Sent ${formatMessageTimestamp(createdAt)}`}>
			{formatMessageTimestamp(createdAt)}
		</span>
	);
}

export function QueuedNote() {
	return (
		<div className="flex items-center gap-2 text-xs text-muted-foreground">
			<span>Queued · sends when the agent finishes</span>
		</div>
	);
}

/** What the user typed. Right-aligned and enclosed so it reads as theirs. */
const WORKER_REPORT_OPEN = "<ao-worker-reports>";
const WORKER_REPORT_CLOSE = "</ao-worker-reports>";

export function humanVisibleText(text: string): string {
	if (!text.endsWith(WORKER_REPORT_CLOSE)) return text;
	const reportStart = text.lastIndexOf(`\n\n${WORKER_REPORT_OPEN}\n`);
	if (reportStart < 0) return text;
	return text.slice(0, reportStart);
}

export function HumanMessage({
	message,
	sessionId,
	apiBaseUrl = getApiBaseUrl(),
	queued,
	animateIn = false,
	onEdit,
	editing = false,
	editText,
	onEditStart,
	onEditDraftChange,
	onEditCancel,
	onEditAbandonRecovery,
	editPending = false,
	editSendBlocked = false,
	editRecoveryLabel,
	editBusy = false,
	editError,
	branchPoint,
	onActivateBranch,
	activateBranchPending = false,
	activateBranchError,
	onSelectAnnotation,
}: {
	message: ConversationMessage;
	/** The staged paths are relative to this session's workspace. */
	sessionId: string;
	/** The live daemon origin; passed by the timeline so daemon restarts refresh images. */
	apiBaseUrl?: string | null;
	/** Typed while the agent was busy, and not sent yet. */
	queued?: boolean;
	/** True only for a human message added after the timeline first mounted. */
	animateIn?: boolean;
	onEdit?: (turnId: string, text: string) => Promise<unknown> | void;
	editing?: boolean;
	editText?: string;
	onEditStart?: () => void;
	onEditDraftChange?: (text: string) => void;
	onEditCancel?: () => void;
	onEditAbandonRecovery?: () => void;
	editPending?: boolean;
	editSendBlocked?: boolean;
	editRecoveryLabel?: string;
	editBusy?: boolean;
	editError?: string;
	branchPoint?: ConversationBranchPoint;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
}) {
	// A cloud message's files are listed by the message itself; the references
	// its local echo carries until the server's copy arrives are not prose.
	const visibleMessageText = humanVisibleText(splitCloudAttachmentReferences(message.text).text);
	const { body, attachments } = stagedAttachmentParts(visibleMessageText);
	const excerptReferences = (message.content ?? []).filter(
		(content) => (content.type === "excerpt" && (Boolean(content.text) || content.snapshot?.badgeOnly === true)) || Boolean(content.fileReference),
	);
	const annotationSummary = excerptReferences.length > 0 ? (
		<ChatAnnotationSummary
			annotations={excerptReferences.map((excerpt) => ({
				fileReference: excerpt.fileReference, id: excerpt.annotationId ?? excerpt.fileReference?.id, order: excerpt.annotationOrder ?? excerpt.fileReference?.order,
				text: excerpt.text ?? "",
				conversationId: excerpt.sourceConversationId,
				messageId: excerpt.sourceMessageId,
				revision: excerpt.sourceRevision,
				snapshot: excerpt.snapshot,
			}))}
			onSelect={onSelectAnnotation}
			tile
		/>
	) : null;
	const hasAttachments = Boolean(annotationSummary) || attachments.length > 0 || Boolean(message.cloudAttachments?.length);
	// The skills and files it sent as chips, drawn as colored text.
	const renderText = useUserTextRenderer(sessionId, body, attachments, apiBaseUrl);
	// Attachments carry themselves, so a message of nothing else draws no empty bubble.
	const showBubble = Boolean(body) || !hasAttachments;
	// Hover-only actions float under the bubble instead of reserving a blank row;
	// a branch navigator is always visible, so it keeps its place in the flow.
	const inFlowActions = Boolean(branchPoint && onActivateBranch);
	const actionRowClass = cn("flex h-7 items-center gap-1", inFlowActions ? "mt-1" : "absolute right-0 top-full z-10 mt-1");
	// The editor's Cancel and Send float too, so starting an edit moves nothing. They
	// sit on the "Worked for" line the turn reserves one row gap (10px) under the bubble.
	const editActionRowClass = cn("flex h-7 items-center gap-1", inFlowActions ? "mt-1" : "absolute right-0 top-[calc(100%+0.625rem)] z-10");
	return (
		<div
			className="group/message relative flex flex-col items-end gap-1"
			data-chat-message-id={!queued && !editing ? message.id : undefined}
			data-find-message-id={!queued && !editing && message.origin !== "automation" && message.origin !== "daemon" && !message.continuation ? message.id : undefined}
			data-chat-message-revision={!queued && !editing ? message.revision : undefined}
			data-chat-message-role={!queued && !editing ? message.role : undefined}
		>
			{message.authorName ? (
				<span className="max-w-full truncate px-1 text-xs text-muted-foreground" data-chat-message-author>
					{message.authorName}
				</span>
			) : null}
			{/* A queued message reads as not-yet-sent rather than as sent-and-ignored:
			    the agent has not seen it, and the timeline should not imply it has. */}
			{editing ? (
				// The bubble itself becomes the editor: same box, text and attachments.
				<HumanMessageEditor
					text={editText ?? visibleMessageText}
					originalText={visibleMessageText}
					content={(message.content ?? []).filter((content) => !excerptReferences.includes(content))}
					sessionId={sessionId}
					apiBaseUrl={apiBaseUrl}
					className={humanBubbleClass()}
					actionsClassName={editActionRowClass}
					leading={annotationSummary}
					cloudAttachments={message.cloudAttachments?.length ? <CloudMessageAttachments attachments={message.cloudAttachments} className="contents" /> : undefined}
					pending={editPending}
					locked={Boolean(editRecoveryLabel)}
					recoveryLabel={editRecoveryLabel}
					sendBlocked={editSendBlocked}
					busy={editBusy}
					error={editError}
					onDraftChange={onEditDraftChange}
					onCancel={() => onEditCancel?.()}
					onAbandonRecovery={onEditAbandonRecovery}
					onSend={(text) => {
						if (!message.turnId || !onEdit) return;
						return onEdit(message.turnId, text);
					}}
				/>
			) : (
				<>
					{/* Attachments sit above the text they came with, outside its bubble and fold. */}
					{hasAttachments ? (
						<AttachmentStrip className={cn(animateIn && "chat-human-message-enter")}>
							{annotationSummary}
							<StagedAttachmentItems
								paths={attachments}
								sessionId={sessionId}
								apiBaseUrl={apiBaseUrl}
								ariaLabel="Attached files"
								className="contents"
							/>
							<CloudMessageAttachments attachments={message.cloudAttachments} className="contents" />
						</AttachmentStrip>
					) : null}
					{showBubble ? (
						<div
							/* Themes draw sent and queued differently; light theme needs to tell them
							   apart in CSS because it paints an enclosure only around a sent one. */
							data-queued={queued ? "" : undefined}
							className={cn(humanBubbleClass(queued), animateIn && "chat-human-message-enter")}
						>
							<CollapsibleHumanBody stateKey={visibleMessageText}>
								{body ? (
									<div data-chat-message-body="" className={USER_BODY_CLASS} onCopy={(event) => copyWholeMessageAsTyped(event, body)}>
										<UserMarkdown
											text={body}
											renderText={renderText}
										/>
									</div>
								) : null}
							</CollapsibleHumanBody>
						</div>
					) : null}
				</>
			)}
			{editing ? null : (
				<div className={actionRowClass}>
					<div className="flex items-center gap-2 opacity-0 transition-opacity duration-150 ease-out focus-within:opacity-100 group-hover/message:opacity-100 motion-reduce:transition-none">
						<MessageTimestamp createdAt={message.createdAt} />
						{onEdit && onEditStart && message.turnId ? (
							<Button
								type="button"
								variant="quiet"
								size="icon-sm"
								onClick={onEditStart}
								aria-label="Edit user message"
							>
								<Pencil aria-hidden="true" className="size-3.5" />
							</Button>
						) : null}
						<CopyButton
							text={visibleMessageText}
							label="Copy user message"
							compact
						/>
					</div>
					{branchPoint && onActivateBranch ? (
						<ConversationBranchNavigator
							point={branchPoint}
							pending={activateBranchPending}
							error={activateBranchError}
							onActivate={onActivateBranch}
						/>
					) : null}
				</div>
			)}
			{queued ? <QueuedNote /> : null}
			{message.delivery && message.delivery !== "accepted" && message.delivery !== "sending" ? (
				<DeliveryNote state={message.delivery} />
			) : null}
		</div>
	);
}

/**
 * A message from a worker, automation, or the daemon. Attribution comes from the
 * durable origin field, never from a prefix parsed out of the text.
 */
export function OriginMessage({ message }: { message: ConversationMessage }) {
	const [expanded, setExpanded] = useState(false);
	const senderSessionId = message.senderSessionId?.trim();
	const senderProjectId = message.senderProjectId?.trim();
	const senderLabel = message.senderDisplayName?.trim() || senderSessionId;
	const senderHref = senderSessionId && senderProjectId
		? `ao://sessions/${encodeURIComponent(senderProjectId)}/${encodeURIComponent(senderSessionId)}`
		: undefined;
	const visibleText = senderSessionId ? stripSteerSenderPrefix(message.text, senderSessionId) : message.text;
	const longReport = visibleText.length > ORIGIN_REPORT_COLLAPSE_AT;
	const preview = longReport
		? originReportPreview(visibleText)
		: visibleText;

	return (
		<AutomationMessageFrame
			label={senderSessionId ? <>{"[from "}{senderHref ? <SessionLabelLink href={senderHref}>{senderLabel}</SessionLabelLink> : senderLabel}{"]"}</> : message.senderLabel ?? message.origin}
			createdAt={message.createdAt}
		>
			{longReport && !expanded ? (
				<div className="line-clamp-3">
					<OriginPreviewMarkdown text={preview} />
				</div>
			) : (
				<ChatMarkdown text={visibleText} muted safeOrigin className="whitespace-pre-wrap text-sm leading-relaxed" />
			)}
			{longReport ? <AutomationExpandButton expanded={expanded} onClick={() => setExpanded((current) => !current)} /> : null}
		</AutomationMessageFrame>
	);
}

function AutomationMessageFrame({
	label,
	createdAt,
	children,
}: {
	label: ReactNode;
	createdAt: string;
	children: ReactNode;
}) {
	return (
		<div className="cursor-chat-origin-message rounded-md border border-border border-l-2 border-l-logo-accent/60 px-3.5 py-2.5">
			<div className="mb-1.5 flex min-w-0 items-center gap-2 text-xs font-medium text-muted-foreground">
				<CircleAlert aria-hidden="true" className="size-3.5 shrink-0 text-logo-accent" />
				<span className="min-w-0 truncate">{label}</span>
				<span className="ml-auto shrink-0 font-normal tabular-nums">{formatTime(createdAt)}</span>
			</div>
			{children}
		</div>
	);
}

function AutomationExpandButton({ expanded, onClick }: { expanded: boolean; onClick: () => void }) {
	return (
		<Pressable
			onClick={onClick}
			aria-expanded={expanded}
			className="mt-2 flex items-center gap-1 text-xs font-medium text-logo-accent transition-colors hover:text-markdown-link-hover"
		>
			<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform", expanded && "rotate-90")} />
			{expanded ? "Hide report" : "Show full report"}
		</Pressable>
	);
}

/** The agent's prose. Streaming is represented by text arriving in place. */
export function AssistantMessage({
	message,
	showCopy = false,
	live = false,
	onRollback,
	rollbackDisabled = false,
}: {
	message: ConversationMessage;
	/** The final answer owns the copy action; it stays available while that answer streams. */
	showCopy?: boolean;
	/** The enclosing turn is still active, even if its last text chunk has landed. */
	live?: boolean;
	/**
	 * Discard this turn and everything after it. Lives next to copy so the finished
	 * answer owns both "keep this" and "undo from here".
	 */
	onRollback?: () => void;
	/** Keep the rollback action mounted while another response is streaming. */
	rollbackDisabled?: boolean;
}) {
	const visibleText = useSmoothStreamingText(message);
	const renderingStreaming = message.streaming || visibleText.length < message.text.length;
	// Copy copies the stored markdown, so it need not wait for the typewriter to catch
	// up. Rollback acts on the turn, so it waits for it to settle. While the turn is
	// live the row below the answer is the live status; both actions take its place
	// when the turn settles, so nothing moves.
	const copyReady = showCopy && !message.streaming && !live;
	const rollbackReady = Boolean(onRollback) && !live && !renderingStreaming;
	const showActions = copyReady || rollbackReady;
	return (
		<div
			className="group/message relative"
			data-chat-streaming-output={renderingStreaming ? "" : undefined}
			data-find-message-id={message.origin !== "automation" && message.origin !== "daemon" ? message.id : undefined}
			data-chat-message-id={!live && !renderingStreaming ? message.id : undefined}
			data-chat-message-revision={!live && !renderingStreaming ? message.revision : undefined}
			data-chat-message-role={!live && !renderingStreaming ? message.role : undefined}
		>
			<div data-chat-message-body=""><ChatMarkdown text={visibleText} streaming={renderingStreaming} /></div>
			{showActions ? (
				// One action row for the completed answer, not one after every prose
				// fragment the provider emitted while working. Copy and rollback stay
				// visible; only the wall-clock time reveals on hover.
				<TurnActions
					className="mt-1"
					copyText={copyReady ? message.text : undefined}
					onRollback={rollbackReady ? onRollback : undefined}
					rollbackDisabled={rollbackDisabled}
					timestamp={message.createdAt}
				/>
			) : null}
		</div>
	);
}


/**
 * What the live row says the agent is doing right now, in the words its own row
 * uses: "Reading" while the step is in progress, and "Read" for the moment a
 * finished one stays on the row before it moves up.
 */
export function liveTaskLabel(activity: ConversationActivity): string {
	if (activity.activityKind === "reasoning") return "Thinking";
	if (activity.activityKind === "auto_review") return "Reviewing";
	if (activity.activityKind === "mcp_tool") {
		const ao = aoToolView(activity);
		if (ao) return ao.label;
		const step = activityToolStep(activity);
		if (!step) return mcpToolName(activity).tool.trim() || "Using a tool";
		const verb = stepInProgress(activity) ? step.live : step.done;
		return step.target ? `${verb} ${step.target}` : verb;
	}
	const { label, path } = splitSummary(activity);
	return path ? `${label} ${path}` : label;
}

/**
 * A step still under way. Claude Code reports a tool call as pending and then done,
 * with nothing in between, so a pending step is in progress too: "Reading a.ts",
 * not "Read a.ts", and "Deleting build" while it waits for approval.
 */
export function stepInProgress(activity: ConversationActivity): boolean {
	return activity.status === "running" || activity.status === "pending";
}

/**
 * What the reader chose on the live row for a step, open or closed, by step id.
 * The step's own row starts from it once the step moves up from the live row, so
 * output the reader opened to follow stays open where it lands.
 */
export const LiveStepChoices = createContext<Map<string, boolean> | undefined>(undefined);

/**
 * Which form of a step's label to show. In progress reads in the present tense, and
 * so does a step that failed or stopped when its past tense would claim the result:
 * "Deleting build  failed", not "Deleted build  failed". "Ran tests  exit 1" is true
 * as it stands. `step` is absent for a file edit, whose past tense always claims it.
 */
function stepTense(activity: ConversationActivity, step?: CommandStep): "live" | "done" {
	if (stepInProgress(activity)) return "live";
	const unfinished = activity.status === "failed" || activity.status === "cancelled" || activity.status === "recovered";
	return unfinished && !(step && pastTenseHoldsOnFailure(step)) ? "live" : "done";
}

/**
 * A provider retry, split for display: the attempt count up front and the reason
 * behind it, so "Reconnecting… 4/5" reads as a state and the 401 as its detail.
 * Codex puts the whole error in the summary; Claude keeps the reason in `text`.
 */
export function reconnectCopy(activity: ConversationActivity): { label: string; detail?: string } {
	const summary = activity.summary.trim();
	const text = activity.detail?.text?.trim() || undefined;
	// Only a summary that is about reconnecting carries the attempt count; a count
	// elsewhere in some other error text is not one.
	const match = /^\W*(?:re-?connect|retry)/i.test(summary) ? /(\d+)\s*(?:\/|of)\s*(\d+)/.exec(summary) : null;
	if (!match) return { label: summary || "Reconnecting…", detail: text };
	const rest = summary.slice(match.index + match[0].length).replace(/^[\s.:,;—–-]+/, "").trim();
	return { label: `Reconnecting… ${match[1]}/${match[2]}`, detail: text ?? (rest || undefined) };
}

const LIVE_RECONNECT_CHIP =
	"inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface px-1.5 py-px text-xs tabular-nums text-muted-foreground";

/**
 * The live edge of a turn: the orb, what the agent is doing, and for how long.
 *
 * It sits at the end of the turn and follows the work down. It says only what the
 * reader cannot see for themselves: the orb is the liveness, the label the current
 * step (or the retry the provider is on), the timer the duration. No "Working for":
 * when the turn settles this row fades out where it is and "Worked for" opens at the
 * top of the turn, so nothing appears to travel between the two.
 */
export function LiveResponseStatus({ entranceKey, startedAt, startupLabel, failed = false, task, step, reconnect, children }: {
	/** Names this row across remounts (windowing), so its label's entrance plays once. */
	entranceKey?: string;
	startedAt?: string;
	/** Replaces the timer while the session is still being set up. */
	startupLabel?: string;
	failed?: boolean;
	/** What the agent is doing right now, named the way its activity row names it. */
	task?: string;
	/**
	 * The running step `task` names, when this row stands in for its own row. The
	 * row then opens onto the step's command and output, as that row would.
	 */
	step?: ConversationActivity;
	/** The provider is retrying; the row says so in place of the task. */
	reconnect?: { label: string; detail?: string };
	children?: ReactNode;
}) {
	const reducedMotion = useReducedMotion();
	const labelId = useId();
	// Per step: the row stays one line, even while the step prints, until the reader
	// opens it. The next step starts closed again.
	const [stepChoice, setStepChoice] = useState<{ id: string; open: boolean } | null>(null);
	const stepChoices = useContext(LiveStepChoices);
	const expandable = Boolean(step && !startupLabel && !reconnect && liveStepHasDetail(step));
	const stepOpen = expandable && stepChoice?.id === step?.id && stepChoice!.open;
	// A finished step stays on the row for a moment before it moves up. It says what
	// it did ("Read a.ts"), and the shimmer that means "under way" stops.
	const stepSettled = Boolean(step && isLiveStepKind(step) && !stepInProgress(step));
	const started = useMemo(() => {
		const parsed = startedAt ? Date.parse(startedAt) : Date.now();
		return Number.isFinite(parsed) ? parsed : Date.now();
	}, [startedAt]);
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, []);
	const elapsedMs = Math.max(0, now - started);
	const label = startupLabel ?? reconnect?.label ?? task;
	// The provider re-saves its retry on every attempt, and the step label changes
	// with every tool call. Keyed by its text the label would remount, and replay its
	// entrance, each time; keyed by kind it updates in place and crossfades only when
	// the row changes what it is showing.
	const labelKind = startupLabel ? "startup" : reconnect ? "reconnect" : task ? "task" : undefined;
	// A row that mounts with its chat (a switch to a working session) shows the
	// label it opened with in place; any label after that fades in.
	const labelEnters = useChatEntrance(entranceKey ?? labelId);
	const quietLabelKind = useRef<string | null | undefined>(labelEnters ? null : labelKind);
	if (quietLabelKind.current !== null && quietLabelKind.current !== labelKind) quietLabelKind.current = null;
	const labelEntrance = quietLabelKind.current === null;
	// The mark takes the step rows' icon slot and gap (STEP_MARK_SLOT_CLASS), so the
	// dots sit on the step icons' left edge and the label starts where theirs do: a
	// step that moves up from here into its own row keeps both edges.
	const rowClass = "-mx-1 flex h-7 select-none items-center gap-1.5 px-1 py-0";
	const row = (
		<>
			<span aria-hidden="true" className={STEP_MARK_SLOT_CLASS}>
				{failed ? <X className="size-3 text-destructive" /> : <LiveWorkingMark />}
			</span>
			<span
				id={labelId}
				role={failed ? "alert" : "status"}
				data-testid="live-working-label"
				className="flex min-w-0 items-center gap-2 text-sm"
			>
				{/* Keyed by kind, so a new kind of label fades in once (see .chat-live-label)
				    while the text inside updates in place. */}
				{label && labelKind ? (
					<span key={labelKind} className={cn(labelEntrance && "chat-live-label", "min-w-0 truncate")} data-testid="live-working-task">
						{reconnect && !startupLabel ? (
							<span className={LIVE_RECONNECT_CHIP}>
								<span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-warning" />
								<span className="truncate">{label}</span>
							</span>
						) : (
							<span className={failed ? "text-destructive" : stepSettled ? "text-muted-foreground" : "chat-working-shimmer"}>
								{label}
							</span>
						)}
					</span>
				) : null}
				{/* "Waiting 6s · 10s": the dot keeps the step's own numbers apart from the timer's. */}
				{label && labelKind && !startupLabel ? (
					<span aria-hidden="true" className="shrink-0 text-muted-foreground/50">
						·
					</span>
				) : null}
				{startupLabel ? null : (
					<span className="shrink-0 tabular-nums text-muted-foreground/75" data-testid="live-working-timer">
						{formatDuration(elapsedMs)}
					</span>
				)}
			</span>
			{expandable ? (
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/live:text-muted-foreground",
						stepOpen && "rotate-90",
					)}
				/>
			) : null}
		</>
	);
	return (
		<div className="min-w-0">
			{/* The row is the same element whether or not it opens, so the loader's beat,
			    the label's entrance and the status region are not remounted each time a
			    step with details takes over from Thinking and hands back. When there is
			    something to open, the toggle lies over the whole row. */}
			<div className={cn(rowClass, "group/live relative")}>
				{row}
				{expandable && step ? (
					<Pressable
						onClick={() => {
							stepChoices?.set(step.id, !stepOpen);
							setStepChoice({ id: step.id, open: !stepOpen });
						}}
						aria-expanded={stepOpen}
						aria-labelledby={labelId}
						className="absolute inset-0"
						data-testid="live-working-row"
					/>
				) : null}
			</div>
			<AnimatePresence initial={false}>
				{stepOpen && step ? (
					<motion.div
						key="live-step-detail"
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden rounded-lg"
					>
						<LiveStepDetail activity={step} />
					</motion.div>
				) : null}
			</AnimatePresence>
			{reconnect?.detail && !startupLabel ? (
				<p
					className="truncate pl-4.5 text-xs leading-5 text-muted-foreground"
					title={reconnect.detail}
					data-testid="live-turn-status"
				>
					{linkifiedProviderErrorText(reconnect.detail)}
				</p>
			) : null}
			<AnimatePresence initial={false}>
				{children ? (
					<motion.div
						key="startup-checklist"
						initial={false}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.2, ease: "easeOut" }}
						className="overflow-hidden"
					>
						{children}
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * Delivery state, stated rather than implied. `uncertain` is its own outcome:
 * the provider may have accepted the turn while AO lost the connection, and
 * pretending otherwise in either direction would be a lie.
 */
function DeliveryNote({ state }: { state: DeliveryState }) {
	const copy: Record<DeliveryState, string> = {
		queued: "Queued — will send when the agent is idle",
		sending: "Sending…",
		accepted: "Delivered",
		uncertain: "Delivery uncertain — not retried automatically",
		failed: "Not delivered",
	};
	return (
		<span
			className={cn(
				"text-caption leading-none",
				state === "uncertain" || state === "failed" ? "text-warning" : "text-muted-foreground",
			)}
		>
			{copy[state]}
		</span>
	);
}

/* -------------------------------------------------------------------------- */
/* activities                                                                  */
/* -------------------------------------------------------------------------- */

/**
 * One activity, routed to the renderer for its kind.
 *
 * The dispatch lives here rather than in the timeline because a run collapses
 * activities too, and both paths must agree about what an MCP call looks like. A
 * kind this build does not recognize still renders as a generic row — dropping it
 * would hide work the agent really did.
 */
export function ActivityRow({
	activity,
	repeats,
}: {
	activity: ConversationActivity;
	/** Earlier calls with the same label, folded into this row. */
	repeats?: readonly ConversationActivity[];
}) {
	// A call already in the chat when it opened, or scrolled back into view, is
	// drawn in place; only a call that arrives fades in.
	const entering = useChatEntrance(`row:${activity.id}`, activity.sequence);
	const toolActivity =
		activity.activityKind === "command" ||
		activity.activityKind === "file_change" ||
		activity.activityKind === "mcp_tool" ||
		activity.activityKind === "auto_review" ||
		activity.activityKind === "ao_action";
	// AO records pages as system rows; a page named by any other row (a cloud
	// session's, say) is not one this daemon serves.
	const renderRef = activity.activityKind === "system" ? readRenderRef(activity.detail) : undefined;
	const artifactRef = activity.activityKind === "system" ? readArtifactRef(activity.detail) : undefined;

	let content: ReactNode;
	if (activity.activityKind === "mcp_tool") content = <McpToolRow activity={activity} />;
	else if (activity.activityKind === "auto_review") content = <AutoReviewRow activity={activity} />;
	else if (activity.activityKind === "reasoning") content = <ReasoningBlock activity={activity} />;
	else if (activity.activityKind === "error") content = <ErrorActivityRow activity={activity} />;
	else if (activity.activityKind === "ao_action") content = <AOActionRow activity={activity} />;
	// The model picker already names the model that answered.
	else if (isModelReroute(activity)) return null;
	else if (activity.detail?.event === "auth.reauth_required") content = <ReauthRow activity={activity} />;
	else if (activity.detail?.event === "workspace.script.run" && activity.detail.scriptRun) content = <ScriptRunPanel collapsible run={activity.detail.scriptRun} />;
	else if (activity.detail?.event === "workspace.setup") content = <ProjectSetupRow activity={activity} />;
	else if (activity.detail?.event === "account.switched") content = <AccountSwitchedRow accountId={activity.detail.accountId} />;
	else if (renderRef) content = <RenderFrame render={renderRef} />;
	else if (artifactRef) content = <RenderFrame artifact={artifactRef} />;
	else content = <GenericActivityRow activity={activity} repeats={repeats} />;

	if (!toolActivity) return content;
	return (
		<ActivityTransition value={`${activity.revision}:${activity.summary}:${activity.status}`} enter={entering}>
			{content}
		</ActivityTransition>
	);
}

export function ActivityTransition({
	value,
	children,
	inline = false,
	enter = true,
}: {
	value: string;
	children: ReactNode;
	inline?: boolean;
	/** Fades in on mount. Off for content that was already on screen (see chat-entrance.ts). */
	enter?: boolean;
}) {
	const reducedMotion = useReducedMotion();
	const previousValue = useRef(value);
	const [settling, setSettling] = useState(false);

	useEffect(() => {
		if (previousValue.current === value) return;
		previousValue.current = value;
		// A change made while the window is hidden would dip on the reader's return.
		if (pageHidden()) return;
		setSettling(true);
	}, [value]);

	const MotionElement = inline ? motion.span : motion.div;
	return (
		<MotionElement
			initial={reducedMotion || !enter ? false : { opacity: 0 }}
			animate={
				reducedMotion || !settling
					? { opacity: 1 }
					: { opacity: 0.72 }
			}
			transition={{ duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] }}
			onAnimationComplete={() => setSettling(false)}
		>
			{children}
		</MotionElement>
	);
}

function AOActionRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const descriptor = describeConversationActivity(activity);
	const lines = actionDetailLines(activity);
	const [override, setOverride] = useState<boolean | null>(null);
	const reducedMotion = useReducedMotion();
	const label = descriptor?.label ?? activity.summary;
	const href = descriptor?.href;
	const hasBody = lines.length > 0 || Boolean(href);
	const open = override ?? false;
	return (
		<div className="flex min-w-0 max-w-full flex-col">
			<Pressable
				onClick={() => setOverride(!open)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				aria-label={descriptor?.ariaLabel ?? label}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-row-toggle", !hasBody && "cursor-default")}
			>
				<Bot aria-hidden="true" className="size-3 shrink-0 text-muted-foreground/70" />
				<span className="activity-row-label shrink-0 text-xs font-normal text-muted-foreground">{label}</span>
				{descriptor?.state === "failed" ? (
					<span className="text-xs text-destructive">{t("chat.action.status.failed")}</span>
				) : null}
				{descriptor?.state === "cancelled" ? (
					<span className="text-xs text-muted-foreground/70">{t("chat.action.status.cancelled")}</span>
				) : null}
				{descriptor?.state === "running" ? (
					<Loader2 aria-hidden="true" className="size-3 animate-spin text-muted-foreground/60" />
				) : null}
				{hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
							open && "rotate-90",
						)}
					/>
				) : null}
			</Pressable>
			<AnimatePresence initial={false}>
				{open && hasBody ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden"
					>
						<div className="flex flex-col gap-1 px-2 py-1 text-xs text-muted-foreground">
							{lines.map((line, index) => <span key={index}>{line}</span>)}
							{href && descriptor?.linkLabelKey ? (
								<SessionLabelLink href={href}>{t(descriptor.linkLabelKey)}</SessionLabelLink>
							) : null}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * A collapsed activity row: icon, label, target, state. Expands to its payload.
 *
 * A `running` activity with no completion is a real terminal state here, not a
 * spinner that hangs forever — a provider can start a command and supersede it.
 */
function GenericActivityRow({
	activity,
	repeats = [],
}: {
	activity: ConversationActivity;
	repeats?: readonly ConversationActivity[];
}) {
	// Closed until the reader opens it, and their choice sticks: auto-collapsing a log
	// someone is reading is worse than leaving a finished row open. That goes for a
	// choice made on the live row too, which the row starts from when the step moves
	// up from there.
	const stepChoices = useContext(LiveStepChoices);
	const [open, setOpen] = useState(() => stepChoices?.get(activity.id) ?? false);
	const toggle = (next: boolean) => {
		setOpen(next);
		// Kept current, so a later remount starts from the reader's latest choice.
		if (stepChoices?.has(activity.id)) stepChoices.set(activity.id, next);
	};
	const reducedMotion = useReducedMotion();
	const Icon = activityIcon[activity.activityKind] ?? SquareTerminal;
	const files = fileChangeFiles(activity);
	const hasBody = genericActivityHasBody(activity);
	const { label, path, pathTitle, icon: stepIconName } = splitSummary(activity);
	const StepMark = stepIcon[stepIconName ?? "run"];
	// Commands and file edits share the explore-style summary line: a small mark for
	// what the step did, muted label, always-visible chevron. Everything else keeps
	// the denser bordered activity row.
	const compactSummary =
		activity.activityKind === "command" || activity.activityKind === "file_change";
	// A provider retry ("Reconnecting… 4/5") is a note on the turn, not a row in a
	// list: no rule above it and no inset, so it lines up with the turn's text.
	const notice = activity.activityKind === "system" && activity.detail?.event === "provider.failure";
	// Only a retry with an attempt count splits; other provider notes keep their summary
	// whole, and their reason stays behind the disclosure like any other row's.
	const retry = notice ? reconnectCopy(activity) : undefined;
	const noticeLabel = retry?.label.startsWith("Reconnecting…") ? retry.label : undefined;
	const noticeDetail = noticeLabel ? retry?.detail : undefined;
	const singleEdit = activity.activityKind === "file_change" && files.length === 1 ? files[0] : undefined;

	return (
		<div
			className={cn(
				"min-w-0 max-w-full",
				compactSummary
					? "flex flex-col"
					: notice
						? "group/activity"
						: "group/activity border-t border-border first:border-t-0",
			)}
		>
			<Pressable
				onClick={() => toggle(!open)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					compactSummary
						? ACTIVITY_SUMMARY_BUTTON_CLASS
						: notice
							? "flex min-h-7 w-full min-w-0 select-none items-center gap-2 py-1 text-left text-xs"
							: "flex min-h-[35px] w-full min-w-0 select-none items-center gap-[9px] px-[11px] py-2 text-left text-xs",
					"activity-row-toggle",
					hasBody && !compactSummary && "hover:text-foreground",
					!hasBody && "cursor-default",
				)}
			>
				{compactSummary ? (
					<StepMark aria-hidden="true" className="size-3 shrink-0 text-muted-foreground/70" />
				) : (
					<Icon
						aria-hidden="true"
						className={cn(
							"w-[15px] shrink-0 text-center",
							activity.status === "failed" ? "text-destructive" : "text-muted-foreground/70",
						)}
						size={14}
					/>
				)}
				{singleEdit ? (
					<span className="flex min-w-0 items-center gap-1 text-xs font-normal">
						<span className="shrink-0 text-muted-foreground">
							{stepTense(activity) === "live"
								? liveFileChangeVerb(singleEdit.status ?? "modified")
								: fileChangeVerb(singleEdit.status ?? "modified")}
						</span>
						<FileLocationLabel path={singleEdit.path} oldPath={singleEdit.oldPath} />
						{singleEdit.additions > 0 ? (
							<span className="shrink-0 font-mono text-micro tabular-nums text-success">
								+{singleEdit.additions}
						</span>
						) : null}
						{singleEdit.deletions > 0 ? (
							<span className="shrink-0 font-mono text-micro tabular-nums text-destructive">
								&minus;{singleEdit.deletions}
							</span>
						) : null}
					</span>
				) : compactSummary && path ? (
					// Verb and target sit as close together as a file edit's verb and name.
					// The space is not drawn in the flex row, but it keeps the accessible
					// name "Read README.md" rather than "ReadREADME.md".
					<span className="flex min-w-0 items-center gap-1 text-xs font-normal">
						<strong className="activity-row-label shrink-0 text-xs font-normal text-muted-foreground group-hover/activity:text-foreground">
							<ActivityTitle text={label} />
						</strong>
						{" "}
						<StepTarget text={path} title={pathTitle} />
					</span>
				) : (
					<strong
						className={cn(
							compactSummary
								? "activity-row-label shrink-0 text-xs font-normal text-muted-foreground group-hover/activity:text-foreground"
								: "min-w-0 truncate font-medium",
							!compactSummary &&
								(activity.status === "failed"
									? "text-destructive"
									: notice
										? "font-normal text-muted-foreground"
										: "text-foreground"),
						)}
						title={compactSummary ? undefined : noticeLabel ?? label}
					>
						<ActivityTitle text={noticeLabel ?? label} />
					</strong>
				)}
				{noticeDetail ? (
					<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground/70" title={noticeDetail}>
						{noticeDetail}
					</span>
				) : path && !singleEdit && !compactSummary ? (
					<span
						className="min-w-0 flex-1 truncate font-mono text-caption text-muted-foreground group-hover/activity:text-foreground"
						title={path}
					>
						{path}
					</span>
				) : compactSummary ? null : (
					<span className="flex-1" />
				)}
				<ActivityState
					activity={activity}
					open={open}
					hasBody={hasBody}
					inlineFileStats={Boolean(singleEdit)}
					showDisclosure={!compactSummary}
				/>
				{compactSummary && hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
							open && "rotate-90",
						)}
					/>
				) : null}
			</Pressable>

			<AnimatePresence initial={false}>
				{open && hasBody ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden rounded-lg"
					>
						{repeats.length > 0 ? (
							<div className="flex flex-col gap-1">
								{[...repeats, activity].map((each) => <GenericActivityBody key={each.id} activity={each} />)}
							</div>
						) : (
							<GenericActivityBody activity={activity} />
						)}
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * Whether a command or file-edit step has anything to open onto. A single edit
 * with no patch has nothing: the header already named the file. Multi-file edits
 * open onto a list, and a lone patch onto the diff itself.
 */
function genericActivityHasBody(activity: ConversationActivity): boolean {
	const detail = activity.detail;
	const files = fileChangeFiles(activity);
	const isFileChange = activity.activityKind === "file_change";
	const hasFileBody =
		files.length > 1 || (files.length === 1 && Boolean(fileChangePatch(files[0])));
	return Boolean(
		detail?.command ||
		(!isFileChange && (detail?.output || detail?.reason || detail?.text || detail?.terminalInput)) || hasFileBody,
	);
}

/** What a step's row opens onto: the command and its output, a patch, or a file list. */
function GenericActivityBody({ activity }: { activity: ConversationActivity }) {
	const detail = activity.detail;
	const files = fileChangeFiles(activity);
	const isFileChange = activity.activityKind === "file_change";
	if (
		activity.activityKind === "command" &&
		(detail?.command || detail?.output || detail?.terminalInput)
	) {
		return <CommandExploreBody activity={activity} />;
	}
	return (
		<div className="flex flex-col gap-1.5 px-1 pb-1 pt-0.5">
			{/* One file: open straight onto its patch. Listing the same
			    basename again under "Edited name" is noise. */}
			{files.length === 1 && fileChangePatch(files[0]) ? (
				<Patch patch={fileChangePatch(files[0])!} truncated={files[0].patchTruncated} />
			) : null}
			{files.length > 1 ? <FileChangeList files={files} /> : null}
			{!isFileChange && detail?.command ? (
				// Said explicitly rather than implied by the label: "Ran command"
				// alone never tells the reader what ran, and the collapsed row
				// deliberately keeps only the category.
				<pre className="scrollbar-none overflow-x-auto border border-border bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-foreground">
					{detail.command}
				</pre>
			) : null}
			{!isFileChange && (detail?.reason || detail?.text) ? (
				<p className="whitespace-pre-wrap px-1 text-caption leading-relaxed text-muted-foreground">
					{detail.reason ?? detail.text}
				</p>
			) : null}
			{!isFileChange && detail?.terminalInput ? (
				<TerminalInput
					text={detail.terminalInput}
					truncated={detail.terminalInputTruncated}
				/>
			) : null}
			{!isFileChange && detail?.output ? <CommandOutput activity={activity} /> : null}
		</div>
	);
}

/**
 * The running step the live row names in place of its own row: a command, a file
 * edit, or a tool call. Other kinds keep their row.
 */
export function isLiveStepKind(activity: ConversationActivity): boolean {
	// AO's own tools answer the agent, so their row opens only onto what a reader can
	// use. The live row would open onto the raw answer.
	if (aoToolView(activity)) return false;
	return (
		activity.activityKind === "command" ||
		activity.activityKind === "file_change" ||
		activity.activityKind === "mcp_tool"
	);
}

/** Whether the step on the live row has details to open: command, output, patch, or payload. */
function liveStepHasDetail(activity: ConversationActivity): boolean {
	if (activity.activityKind === "mcp_tool") return mcpToolHasBody(activity);
	return isLiveStepKind(activity) && genericActivityHasBody(activity);
}

/** The step's details, opened from the live row. */
const LiveStepDetail = memo(function LiveStepDetail({ activity }: { activity: ConversationActivity }) {
	return activity.activityKind === "mcp_tool" ? <McpToolBody activity={activity} /> : <GenericActivityBody activity={activity} />;
});

/**
 * Expanded command / explore body: one soft chat-surface card with the shell
 * line nested in its own chip, then muted monospace output underneath — the
 * same anatomy as the Cursor explore block, restated in AO chat tokens.
 */
function CommandExploreBody({ activity }: { activity: ConversationActivity }) {
	const detail = activity.detail;
	const command = detail?.command?.trim();
	const reason = (detail?.reason ?? detail?.text)?.trim();
	const binary = command ? commandBinaryLabel(command) : undefined;
	const showPrompt = Boolean(reason && reason !== command);

	return (
		<div className="cursor-chat-explore-box mt-1 flex min-w-0 flex-col overflow-hidden rounded-lg border">
			{showPrompt ? (
				<div className="flex min-w-0 items-start gap-2 border-b border-border/60 px-3 py-2">
					<span
						aria-hidden="true"
						className="shrink-0 select-none pt-px font-mono text-caption leading-relaxed text-muted-foreground/70"
					>
						&gt;_
					</span>
					<div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5">
						<span className="min-w-0 break-words text-xs leading-relaxed text-foreground/90">
							{reason}
						</span>
						{binary ? (
							<span className="shrink-0 font-mono text-caption text-muted-foreground/55">
								{binary}
							</span>
						) : null}
					</div>
				</div>
			) : null}

			{command ? (
				<pre
					className={cn(
						"cursor-chat-explore-command overflow-x-auto px-3 py-2 font-mono text-caption leading-relaxed text-foreground/85",
						(detail?.output || detail?.terminalInput) && "border-b border-border/60",
					)}
				>
					{command}
				</pre>
			) : null}

			{detail?.terminalInput ? (
				<div className={cn("px-3 py-2", detail?.output && "border-b border-border/60")}>
					<TerminalInput
						text={detail.terminalInput}
						truncated={detail.terminalInputTruncated}
					/>
				</div>
			) : null}
			{detail?.output ? <CommandOutput activity={activity} embedded /> : null}
		</div>
	);
}

/**
 * What the agent typed into a running command's terminal.
 *
 * Shown apart from the output, and labelled as input, because it is the one thing in
 * the row the agent did rather than observed — usually `^C` on a command that was
 * never going to finish. The daemon keeps it out of `output` for the same reason it
 * is drawn separately here: the PTY echoes keystrokes, so merging them would print
 * the abort twice and leave no way to tell which was which.
 *
 * Control characters are spelled the way a terminal spells them. Stripping them
 * would leave an empty box where the interesting thing happened: an abort IS one
 * unprintable byte.
 */
function TerminalInput({ text, truncated }: { text: string; truncated?: boolean }) {
	const shown = useMemo(() => caretNotation(text), [text]);
	return (
		<div className="flex flex-col gap-1">
			<span className="flex items-center gap-1.5 text-caption text-muted-foreground/70">
				<Keyboard aria-hidden="true" className="size-3" />
				Agent typed
			</span>
			<pre className="scrollbar-none overflow-x-auto border border-dashed border-border-strong bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-accent">
				{shown}
			</pre>
			{truncated ? (
				<p className="text-micro text-muted-foreground/70">
					Orchestrator stopped recording keystrokes at its cap; more were sent.
				</p>
			) : null}
		</div>
	);
}

/**
 * A command's output, with an honest account of what it is.
 *
 * The pre scrolls to its own end while the command runs, because output that
 * arrives below the fold is output nobody sees. It only auto-scrolls while the
 * activity is `running` and only when the reader has not scrolled up: hijacking
 * the viewport of someone reading back through a build log is worse than making
 * them scroll down once.
 *
 * The text is what a terminal would have shown, not the bytes: output arrives with
 * its escape sequences intact — nothing in the stack strips them — so a colourized
 * test run rendered here verbatim is a wall of `[0m`, and a progress bar is a
 * hundred stacked copies of itself. See `lib/ansi.ts` for why this is a text pass
 * rather than a terminal.
 */
function CommandOutput({
	activity,
	embedded = false,
}: {
	activity: ConversationActivity;
	/** Inside the explore card: no second bordered surface. */
	embedded?: boolean;
}) {
	const pre = useRef<HTMLPreElement>(null);
	const detail = activity.detail;
	const hostId = useContext(ChatHostId);
	const sessionId = useChatSessionId();
	// A remote page carries only the head and tail of a long output; outputBytes is its full size.
	const cutBytes = typeof detail?.outputBytes === "number" ? detail.outputBytes : undefined;
	const [whole, setWhole] = useState<{ revision: number; output?: string; loading?: boolean }>();
	const wholeOutput = whole?.revision === activity.revision ? whole.output : undefined;
	const showWhole = () => {
		if (!sessionId) return;
		const revision = activity.revision;
		setWhole({ revision, loading: true });
		fetchActivityOutput(sessionId, hostId, activity.sequence, activity.id)
			.then((output) => setWhole({ revision, output }))
			.catch(() => setWhole(undefined));
	};
	// Older ACP-backed conversations may contain the provider's structured
	// rawOutput even though AO's view model promises a string. New events are
	// normalized at the adapter boundary; this compatibility read keeps those
	// already-durable rows from taking down the entire session surface.
	const raw = commandOutputText((wholeOutput ?? detail?.output) as unknown);
	// Memoized per row: the timeline re-renders once a second while a turn runs, and
	// only the rows a reader has opened pay for this at all.
	const output = useMemo(() => stripAnsi(raw), [raw]);
	// A scroll container still lays out all its text. Bound both a single long
	// line and newline-heavy logs before Chromium measures the expanding row.
	const preview = useMemo(() => {
		let tail = output.slice(-64 * 1024);
		const first = tail.charCodeAt(0);
		if (first >= 0xdc00 && first <= 0xdfff) tail = tail.slice(1);
		return tail.split("\n").slice(-400).join("\n");
	}, [output]);
	const streaming = activity.status === "running";

	useEffect(() => {
		const node = pre.current;
		if (!node || !streaming) return;
		// Within a line of the bottom counts as "following along". Anything further
		// up is a deliberate scroll and is left alone.
		const atBottom = node.scrollHeight - node.scrollTop - node.clientHeight < 24;
		if (atBottom) node.scrollTop = node.scrollHeight;
	}, [output, streaming]);

	return (
		<>
			<pre
				ref={pre}
				aria-live={streaming ? "polite" : undefined}
				className={cn(
					"scrollbar-none max-h-64 overflow-auto font-mono leading-relaxed text-muted-foreground",
					embedded
						? "cursor-chat-explore-output px-3 py-2 text-caption"
						: "border border-border bg-background px-2.5 py-2 text-caption",
				)}
			>
				{preview}
			</pre>
			{preview.length < output.length ? (
				<p className="px-2.5 py-1 text-xs text-muted-foreground">
					Showing the end of this large output.
				</p>
			) : null}
			{cutBytes !== undefined && wholeOutput === undefined && sessionId ? (
				<button
					type="button"
					disabled={whole?.revision === activity.revision && whole.loading}
					onClick={showWhole}
					className="self-start px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground disabled:opacity-60"
				>
					{whole?.revision === activity.revision && whole.loading ? "Loading the whole output…" : `Show the whole output (${Math.ceil(cutBytes / 1024)} KB)`}
				</button>
			) : null}
			{detail?.outputTruncated ? (
				<p className="text-xs leading-relaxed text-warning">
					This command printed more than Orchestrator stores, so the output above stops early. Open a shell in
					the worktree to see the rest.
				</p>
			) : null}
		</>
	);
}

function commandOutputText(raw: unknown): string {
	if (typeof raw === "string") return raw;
	if (!raw || typeof raw !== "object") return "";

	const value = raw as Record<string, unknown>;
	for (const key of ["output", "text", "error", "metadata"]) {
		const text = commandOutputText(value[key]);
		if (text) return text;
	}

	try {
		return JSON.stringify(raw);
	} catch {
		return "";
	}
}

/**
 * Split a summary into an action and a muted target, which is how the row scans:
 * what happened, then what it happened to ("Read" README.md, "Searched for" TODO).
 * A step still in progress says it in the present tense ("Reading"). Anything
 * without a natural split keeps its whole label.
 */
function splitSummary(activity: ConversationActivity): { label: string; path?: string; pathTitle?: string; icon?: StepIcon } {
	if (activity.activityKind === "command") {
		const step = activityCommandStep(activity);
		return { label: step[stepTense(activity, step)], path: step.target, pathTitle: step.targetTitle ?? step.target, icon: step.icon };
	}
	const tense = stepTense(activity);
	const files = fileChangeFiles(activity);
	if (activity.activityKind === "file_change" && files.length === 1) {
		const status = files[0]!.status ?? "modified";
		// Basename sits in the chip next to the status verb; the full path is the hover.
		return {
			label: `${tense === "live" ? liveFileChangeVerb(status) : fileChangeVerb(status)} ${fileBasename(files[0]!.path)}`,
			icon: fileChangeIcon(status),
		};
	}
	// The provider sums up an edit of several files once it is done ("Edited 3 files");
	// while it is under way, the row says so itself.
	if (activity.activityKind === "file_change" && files.length > 1 && tense === "live") {
		const statuses = new Set(files.map((file) => file.status ?? "modified"));
		const verb = statuses.size === 1 ? liveFileChangeVerb([...statuses][0]!) : "Editing";
		return { label: `${verb} ${files.length} files`, icon: "write" };
	}
	if (activity.activityKind === "file_change") return { label: activity.summary, icon: "write" };
	return { label: activity.summary };
}

function ActivityState({
	activity,
	open,
	hasBody,
	inlineFileStats = false,
	showDisclosure = true,
}: {
	activity: ConversationActivity;
	open: boolean;
	hasBody: boolean;
	inlineFileStats?: boolean;
	showDisclosure?: boolean;
}) {
	const { status, detail } = activity;
	const files = fileChangeFiles(activity);

	if (status === "running") {
		return (
			<Loader2
				aria-label="running"
				className="size-3 shrink-0 animate-spin self-center text-muted-foreground/60"
			/>
		);
	}
	if (files.length && !inlineFileStats) {
		const additions = files.reduce((sum, file) => sum + file.additions, 0);
		const deletions = files.reduce((sum, file) => sum + file.deletions, 0);
		return (
			<span className="shrink-0 font-mono text-micro tabular-nums text-muted-foreground/70">
				<span className="text-success">+{additions}</span>{" "}
				<span className="text-destructive">&minus;{deletions}</span>
			</span>
		);
	}
	if (status === "failed") {
		return (
			<span
				className={cn(
					"shrink-0 font-mono text-micro tabular-nums",
					isNonzeroCommandExit(activity) ? "text-muted-foreground/70" : "text-destructive",
				)}
			>
				{detail?.exitCode !== undefined ? `exit ${detail.exitCode}` : "failed"}
			</span>
		);
	}
	if (status === "recovered") {
		return (
			<span className="shrink-0 font-mono text-micro text-muted-foreground/70">
				outcome unknown
			</span>
		);
	}
	if (status === "cancelled") {
		return (
			<span className="shrink-0 font-mono text-micro text-muted-foreground/70">
				stopped
			</span>
		);
	}
	// Everything else settled fine, which is the boring majority. A chevron on
	// hover is the whole affordance; a duration or timestamp on every row builds a
	// column of numbers nobody reads.
	if (hasBody && showDisclosure) {
		return (
			<ChevronRight
				aria-hidden="true"
				className={cn(
					"size-3 shrink-0 self-center text-muted-foreground/50 transition-all",
					open ? "rotate-90 opacity-100" : "opacity-0 group-hover/activity:opacity-100",
				)}
			/>
		);
	}
	return null;
}

/**
 * The files one edit touched, and what it did to them.
 *
 * Styled like the explore summary's nested lines — "Edited FAQ.tsx +1 −1" — so an
 * expanded edit reads the same as the turn-level changed-files list. Hovering the
 * basename shows the full location; a file that carries a patch can still open it.
 */
export function FileChangeList({ files }: { files: FileChangeFile[] }) {
	if (!files.length) return null;
	return (
		<ul className="flex flex-col">
			{files.map((file) => (
				<FileChangeRow key={`${file.oldPath ?? ""}→${file.path}`} file={file} />
			))}
		</ul>
	);
}

function FileChangeRow({ file }: { file: FileChangeFile }) {
	const [open, setOpen] = useState(false);
	const status = diffStatusMark[file.status ?? "modified"] ?? diffStatusMark.modified;
	const hasPatch = Boolean(file.patch);
	const accessibleName = `${status.label} ${file.path}`;

	const line = (
		<>
			<span className="sr-only">{status.label}</span>
			<span className="shrink-0 text-xs text-muted-foreground">
				{fileChangeVerb(file.status ?? "modified")}
			</span>
			<FileLocationLabel path={file.path} oldPath={file.oldPath} />
			<span className="shrink-0 font-mono text-micro tabular-nums text-success">
				+{file.additions}
			</span>
			<span className="shrink-0 font-mono text-micro tabular-nums text-destructive">
				&minus;{file.deletions}
			</span>
		</>
	);

	// A file with no patch is not a button: nothing opens, and a control that does
	// nothing when pressed is worse than plain text.
	if (!hasPatch) {
		return (
			<li className="flex items-center gap-1.5 py-0.5 pr-1" aria-label={accessibleName}>
				{line}
			</li>
		);
	}

	return (
		<li className="flex flex-col">
			<Pressable
				onClick={() => setOpen((prev) => !prev)}
				aria-expanded={open}
				aria-label={accessibleName}
				className="flex items-center gap-1.5 rounded-sm py-0.5 pr-1 text-left"
			>
				{line}
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform",
						open && "rotate-90",
					)}
				/>
			</Pressable>
			{open ? <Patch patch={file.patch!} truncated={file.patchTruncated} /> : null}
		</li>
	);
}

/**
 * One file's unified diff.
 *
 * Highlighted by the same engine the prose fences use — `diff` is one of the shipped
 * grammars — rather than by a second highlighter that would double the payload and
 * eventually disagree with the first.
 *
 * An added file's "patch" is its whole contents with no hunk header, which the diff
 * grammar renders as plain text. That is the right outcome: there is no diff to
 * colour, only a new file to read.
 */
function fileChangePatch(file: FileChangeFile | undefined): string | undefined {
	if (!file) return undefined;
	if (file.patch) return file.patch;
	if (file.oldText === undefined && file.newText === undefined) return undefined;
	const oldLines = file.oldText?.split("\n") ?? [];
	const newLines = file.newText?.split("\n") ?? [];
	const header = `--- ${file.path}\n+++ ${file.path}`;
	return [
		header,
		...oldLines.map((line) => `-${line}`),
		...newLines.map((line) => `+${line}`),
	].join("\n");
}

function Patch({ patch, truncated }: { patch: string; truncated?: boolean }) {
	return (
		// `chat-code` is what the token colours are scoped to, so a patch without it
		// tokenizes correctly and renders in one flat colour.
			<div className="mb-1 mt-0.5 overflow-hidden bg-background">
			<ToolDiffCode text={patch} />
			{truncated ? (
				<p className="border-t border-border px-2.5 py-1.5 text-xs leading-relaxed text-warning">
					This patch is longer than Orchestrator stores, so it stops early. The whole change is in the
					worktree and in the turn&rsquo;s diff.
				</p>
			) : null}
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* reasoning                                                                   */
/* -------------------------------------------------------------------------- */

/**
 * The model's own account of what it is doing.
 *
 * Rendered as prose on a rail rather than as a collapsed row, because a reader only
 * ever sees this after deliberately turning reasoning on — hiding it again behind a
 * second click would make the toggle do nothing. It is markdown: the provider writes
 * bolded section headers into its summaries, and showing those as literal asterisks
 * was the visible half of this being unread.
 *
 * The text streams. Earlier builds read a field the payload does not have, so these
 * rows arrived blank even when the provider was sending; it now lands in `text`
 * while the model works and is replaced by the provider's settled summary when the
 * item completes, so one field is read either way.
 */
function ReasoningBlock({ activity }: { activity: ConversationActivity }) {
	const text = activity.detail?.text ?? activity.detail?.reason ?? "";
	if (!text) return null;
	const streaming = activity.status === "running";

	return (
		<div className="flex gap-2.5 border-l-2 border-border-strong py-0.5 pl-3">
			<Brain aria-hidden="true" className="mt-[3px] size-3.5 shrink-0 text-muted-foreground/70" />
			<div className="min-w-0 flex-1">
				<div className="flex items-center gap-2">
					<span className="text-caption text-muted-foreground/70">
						{streaming ? "Thinking" : "Thought"}
					</span>
					{streaming ? (
						<Loader2
							aria-label="still thinking"
							className="size-3 animate-spin text-muted-foreground/50"
						/>
					) : null}
				</div>
				<ChatMarkdown text={text} streaming={streaming} muted />
				{activity.detail?.textTruncated ? (
					<p className="mt-1 text-micro text-muted-foreground/70">
						This summary is longer than Orchestrator stores, so it stops early.
					</p>
				) : null}
			</div>
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* MCP tool calls                                                              */
/* -------------------------------------------------------------------------- */

/**
 * A call to a tool served by an MCP server.
 *
 * Deliberately not shaped like a command row. These used to render as though the
 * agent had run something in the worktree, which is a different and more alarming
 * claim than "it asked a tool server a question": nothing was executed here, and the
 * cwd it appeared to run in did not exist. So the row names the server and the tool,
 * and opens onto the arguments and the answer rather than onto terminal output.
 */
function McpToolRow({ activity }: { activity: ConversationActivity }) {
	const aoView = aoToolView(activity);
	if (aoView) return aoView.hidden ? null : <AOToolRow view={aoView} />;
	return <GenericMcpToolRow activity={activity} />;
}

function GenericMcpToolRow({ activity }: { activity: ConversationActivity }) {
	// A choice made on the live row carries over when the call moves up from there.
	const stepChoices = useContext(LiveStepChoices);
	const [open, setOpen] = useState(() => stepChoices?.get(activity.id) ?? false);
	const toggle = () => {
		setOpen(!open);
		if (stepChoices?.has(activity.id)) stepChoices.set(activity.id, !open);
	};
	const reducedMotion = useReducedMotion();
	const detail = activity.detail;
	// An agent's built-in tool (a read, a web search) is named like a command step;
	// a call to an MCP server keeps its tool and server name.
	const step = activityToolStep(activity);
	const StepMark = step ? stepIcon[step.icon] : undefined;
	const name = mcpToolName(activity);
	const tool = step ? step[stepTense(activity, step)] : name.tool;
	const sourceLabel = step
		? undefined
		: name.server
			? `· ${name.server}`
			: detail?.progress
				? lastLine(detail.progress)
				: undefined;
	const failed = activity.status === "failed" || detail?.success === false || Boolean(detail?.error);
	const hasBody = mcpToolHasBody(activity);

	return (
		<div className="min-w-0 max-w-full">
			<Pressable
				onClick={toggle}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					ACTIVITY_SUMMARY_BUTTON_CLASS,
					"activity-row-toggle",
					!hasBody && "cursor-default",
				)}
			>
				{StepMark ? <StepMark aria-hidden="true" className="size-3 shrink-0 text-muted-foreground/70" /> : null}
				{step?.target ? (
					// Laid out like a command step: verb, then what it acted on.
					<span className="flex min-w-0 items-center gap-1 text-xs font-normal">
						<strong
							className={cn(
								"activity-row-label shrink-0 text-xs font-normal",
								failed ? "text-destructive" : "text-muted-foreground group-hover/activity:text-foreground",
							)}
						>
							{tool}
						</strong>
						{" "}
						<StepTarget text={step.target} title={step.targetTitle} />
					</span>
				) : (
					<strong
						className={cn(
							"activity-row-label min-w-0 truncate text-xs font-normal",
							failed ? "text-destructive" : "text-muted-foreground",
						)}
						title={tool}
					>
						<span>{tool}</span>
					</strong>
				)}
				{sourceLabel ? (
					<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground group-hover/activity:text-foreground">
						{sourceLabel}
					</span>
				) : null}
				{activity.status === "running" ? (
					<Loader2
						aria-label="running"
						className="size-3 shrink-0 animate-spin text-muted-foreground/60"
					/>
				) : failed ? (
					<span className="shrink-0 text-xs text-destructive">failed</span>
				) : activity.status === "recovered" ? (
					<span className="shrink-0 text-xs text-muted-foreground/70">outcome unknown</span>
				) : activity.status === "cancelled" ? (
					<span className="shrink-0 text-xs text-muted-foreground/70">stopped</span>
				) : hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 text-muted-foreground/40 transition-transform", open && "rotate-90")}
					/>
				) : null}
			</Pressable>

			<AnimatePresence initial={false}>
				{open && hasBody ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden rounded-lg"
					>
						<McpToolBody activity={activity} />
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

function mcpToolHasBody(activity: ConversationActivity): boolean {
	const detail = activity.detail;
	return Boolean(
		mcpToolArguments(activity) !== undefined ||
		detail?.result !== undefined ||
		detail?.content !== undefined ||
		detail?.error ||
		detail?.progress,
	);
}

/** What a tool call opens onto: its error, arguments, result, content and progress. */
function McpToolBody({ activity }: { activity: ConversationActivity }) {
	const detail = activity.detail;
	const args = mcpToolArguments(activity);
	return (
		<div className="flex flex-col gap-2 pb-2.5">
			{detail?.error ? (
				<p className="border border-destructive/30 bg-background px-2.5 py-1.5 text-xs leading-relaxed text-destructive">
					{detail.error}
				</p>
			) : null}
			{args !== undefined ? <JsonPayload label="Arguments" value={args} /> : null}
			{detail?.result !== undefined ? (
				<JsonPayload label="Result" value={detail.result} />
			) : null}
			{detail?.content !== undefined ? <ToolContent value={detail.content} /> : null}
			{detail?.progress ? (
				<div className="flex flex-col gap-1">
					<span className="text-caption text-muted-foreground/70">
						Progress
					</span>
					<pre className="scrollbar-none max-h-40 overflow-auto border border-border bg-background px-2.5 py-1.5 font-mono text-caption leading-relaxed text-muted-foreground">
						{detail.progress}
					</pre>
				</div>
			) : null}
		</div>
	);
}

/**
 * A call to one of AO's own tools, told in the user's words: what was done, and
 * what went wrong when something did. The tool's answer is written for the agent,
 * so it is never shown as is.
 */
function AOToolRow({ view }: { view: AOToolView }) {
	const [open, setOpen] = useState(false);
	const reducedMotion = useReducedMotion();
	const failed = view.state === "failed";
	const hasBody = Boolean((failed && view.error) || view.lines?.length || view.text);

	return (
		<div className="min-w-0 max-w-full">
			<Pressable
				onClick={() => setOpen((prev) => !prev)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-row-toggle", !hasBody && "cursor-default")}
			>
				<strong
					className={cn(
						"activity-row-label min-w-0 truncate text-xs font-normal",
						failed ? "text-destructive" : "text-muted-foreground",
					)}
					title={view.label}
				>
					<span>{view.label}</span>
				</strong>
				{view.note ? (
					<span className="min-w-0 flex-1 truncate text-xs text-warning">{view.note}</span>
				) : null}
				{view.state === "running" ? (
					<Loader2 aria-label="running" className="size-3 shrink-0 animate-spin text-muted-foreground/60" />
				) : hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 text-muted-foreground/40 transition-transform", open && "rotate-90")}
					/>
				) : null}
			</Pressable>

			<AnimatePresence initial={false}>
				{open && hasBody ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden"
					>
						<div className="flex flex-col gap-1 pb-2.5">
							{failed && view.error ? (
								<p className="whitespace-pre-wrap break-words text-xs leading-relaxed text-destructive">
									{view.error}
								</p>
							) : null}
							{view.lines?.map((line, index) => (
								<p
									key={`${index}:${line.text}`}
									className={cn(
										"whitespace-pre-wrap break-words text-xs leading-relaxed",
										line.tone === "error" ? "text-destructive" : "text-muted-foreground",
									)}
								>
									{line.text}
								</p>
							))}
							{view.text ? (
								<pre className="scrollbar-none max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-caption leading-relaxed text-muted-foreground">
									{view.text}
								</pre>
							) : null}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

function ToolContent({ value }: { value: unknown }) {
	const text = normalizeToolCode(toolContentText(value));
	if (text) {
		if (looksLikeUnifiedDiff(text)) return <ToolDiffCode text={text} />;
		return (
			<pre className="chat-code max-h-64 overflow-auto whitespace-pre rounded-lg border border-border bg-background px-2.5 py-1.5">
				<code className="block whitespace-pre font-mono text-caption leading-relaxed text-muted-foreground">
					{ text }
				</code>
			</pre>
		);
	}
	const truncated = truncationNote(value);
	return (
		<pre className="chat-code max-h-56 overflow-auto rounded-lg border border-border bg-background px-2.5 py-1.5">
			<code className="font-mono text-caption leading-[1.55] text-foreground">
				{truncated ?? formatJson(value)}
			</code>
		</pre>
	);
}

function normalizeToolCode(text: string): string {
	const fenced = text.match(/^\s*```[^\n]*\n([\s\S]*?)\n```\s*$/);
	return (fenced?.[1] ?? text).replace(/\r\n?/g, "\n");
}

function looksLikeUnifiedDiff(text: string): boolean {
	const lines = text.split("\n");
	return (
		lines.some((line) => line.startsWith("@@")) ||
		(lines.some((line) => line.startsWith("---")) && lines.some((line) => line.startsWith("+++"))) ||
		(lines.some((line) => line.startsWith("-")) && lines.some((line) => line.startsWith("+")))
	);
}

function ToolDiffCode({ text }: { text: string }) {
	return (
		<div className="chat-code max-h-64 overflow-auto rounded-lg border border-border bg-background px-0 py-1 font-mono text-caption leading-[1.55]">
			{ text.split("\n").map((line, index) => {
				const kind = line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : "context";
				const marker = kind === "add" ? "+" : kind === "del" ? "-" : " ";
				const content = kind === "context" ? line : line.slice(1);
				return (
					<div
						key={`${index}-${line}`}
						data-tool-diff-row=""
						className={cn(
							"flex min-w-max whitespace-pre px-2.5",
							kind === "add" && "bg-success/10",
							kind === "del" && "bg-error/10",
						)}
					>
						<span
							aria-hidden="true"
							className={cn(
								"w-4 shrink-0 select-none text-center",
								kind === "add" && "text-success",
								kind === "del" && "text-error",
							)}
						>
							{marker}
						</span>
						<span className="whitespace-pre text-foreground/90">{content}</span>
					</div>
				);
			})}
		</div>
	);
}

function toolContentText(value: unknown): string {
	if (typeof value === "string") return value;
	if (Array.isArray(value)) return value.map(toolContentText).filter(Boolean).join("\n");
	if (!value || typeof value !== "object") return "";
	const record = value as Record<string, unknown>;
	for (const key of ["text", "content", "output", "data"]) {
		const text = toolContentText(record[key]);
		if (text) return text;
	}
	return "";
}

/**
 * A tool's arguments or answer, as JSON.
 *
 * A payload the daemon could not store arrives as a stand-in object rather than as
 * the real thing, and printing that stand-in as if it were the tool's answer would
 * be a small lie with a confusing shape. It is recognized and said plainly instead.
 */
function JsonPayload({ label, value }: { label: string; value: unknown }) {
	const capped = truncationNote(value);
	const text = useMemo(() => (capped ? "" : formatJson(value)), [capped, value]);

	return (
		<div className="flex flex-col gap-1">
			<span className="text-caption text-muted-foreground/70">
				{label}
			</span>
			{capped ? (
				<p className="border border-border bg-background px-2.5 py-1.5 text-caption leading-relaxed text-muted-foreground">
					{capped}
				</p>
			) : (
				<pre className="chat-code max-h-56 overflow-auto rounded-lg border border-border bg-background px-2.5 py-1.5">
					<code className="font-mono text-caption leading-[1.55] text-foreground">
						<HighlightedCode code={text} language="json" />
					</code>
				</pre>
			)}
		</div>
	);
}

/** The daemon's stand-in for a payload past its cap, or nothing. */
function truncationNote(value: unknown): string | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const record = value as { truncated?: unknown; bytes?: unknown; note?: unknown };
	if (record.truncated !== true) return undefined;
	const bytes = typeof record.bytes === "number" ? ` (${formatBytes(record.bytes)})` : "";
	return `This payload${bytes} was larger than Orchestrator stores, so it was not kept.`;
}

function formatBytes(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatJson(value: unknown): string {
	if (typeof value === "string") return value;
	try {
		return JSON.stringify(value, null, 2) ?? String(value);
	} catch {
		return String(value);
	}
}

/** The most recent line of a progress stream, for the collapsed row. */
function lastLine(text: string): string {
	const lines = text.trimEnd().split("\n");
	return lines[lines.length - 1] ?? "";
}

/* -------------------------------------------------------------------------- */
/* auto review                                                                 */
/* -------------------------------------------------------------------------- */

const RISK_TONE: Record<string, string> = {
	low: "text-muted-foreground",
	medium: "text-warning",
	high: "text-destructive",
	critical: "text-destructive",
};

/**
 * A decision the provider made instead of asking.
 *
 * This is not an approval card and must not read like one: nobody was asked, and
 * there is nothing to answer. What the row owes the user is the fact that a decision
 * was taken on their behalf, what it allowed, and — reachable rather than shouted —
 * the reasoning that allowed it. Denials are called out more loudly than approvals,
 * because a denial is why something the user expected did not happen.
 */
function AutoReviewRow({ activity }: { activity: ConversationActivity }) {
	const [open, setOpen] = useState(false);
	const detail = activity.detail;
	const denied = (detail?.status ?? "").toLowerCase().includes("den");
	const Icon = denied ? ShieldX : ShieldCheck;
	const paths = reviewedPaths(activity);
	const hasBody = Boolean(
		detail?.rationale || detail?.command || detail?.cwd || detail?.host || paths.length,
	);

	return (
		<div className="group/activity border-t border-border first:border-t-0">
			<Pressable
				onClick={() => setOpen((prev) => !prev)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					"activity-row-toggle flex min-h-[35px] w-full select-none items-center gap-[9px] px-[11px] py-2 text-left text-xs",
					hasBody && "hover:text-foreground",
					!hasBody && "cursor-default",
				)}
			>
				<Icon
					aria-hidden="true"
					className={cn(
						"w-[15px] shrink-0 text-center",
						denied ? "text-destructive" : "text-muted-foreground/70",
					)}
					size={14}
				/>
				<strong className="shrink-0 font-medium text-foreground">
					{denied ? "Auto-declined" : "Auto-approved"}
				</strong>
				<span
					className="min-w-0 flex-1 truncate font-mono text-caption text-muted-foreground"
					title={activity.summary}
				>
					<ActivityTitle text={shortenPaths(activity.summary)} />
				</span>
				{detail?.riskLevel ? (
					<span
						className={cn(
							"shrink-0 text-xs",
							RISK_TONE[detail.riskLevel.toLowerCase()] ?? "text-muted-foreground",
						)}
						title={`Risk assessed as ${detail.riskLevel}`}
					>
						{detail.riskLevel}
					</span>
				) : null}
				{hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/50 transition-all",
							open ? "rotate-90 opacity-100" : "opacity-0 group-hover/activity:opacity-100",
						)}
					/>
				) : null}
			</Pressable>

			{open && hasBody ? (
				<div className="flex flex-col gap-2 px-[11px] pb-2.5">
					{/* Said in full rather than implied by the label: "auto-approved" alone
					    leaves it ambiguous whether the user set something up that did this. */}
					<p className="text-xs leading-relaxed text-muted-foreground">
						{denied
							? "The agent asked to do this and the provider declined on your behalf. You were not asked."
							: "The agent asked to do this and the provider allowed it on your behalf. You were not asked."}
					</p>
					{detail?.rationale ? (
						<p className="rounded border border-border bg-background px-2.5 py-1.5 text-xs leading-relaxed text-foreground">
							{detail.rationale}
						</p>
					) : null}
					<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 font-mono text-caption leading-relaxed">
						{detail?.command ? (
							<>
								<dt className="text-muted-foreground/70">command</dt>
								<dd className="min-w-0 break-all text-foreground">{detail.command}</dd>
							</>
						) : null}
						{detail?.cwd ? (
							<>
								<dt className="text-muted-foreground/70">cwd</dt>
								<dd className="min-w-0 break-all text-muted-foreground">
									{shortenPaths(detail.cwd)}
								</dd>
							</>
						) : null}
						{detail?.host ? (
							<>
								<dt className="text-muted-foreground/70">host</dt>
								<dd className="min-w-0 break-all text-muted-foreground">{detail.host}</dd>
							</>
						) : null}
						{paths.length ? (
							<>
								<dt className="text-muted-foreground/70">files</dt>
								<dd className="min-w-0 break-all text-muted-foreground">
									{paths.map((path) => shortenPaths(path)).join(", ")}
								</dd>
							</>
						) : null}
						{detail?.decisionSource ? (
							<>
								<dt className="text-muted-foreground/70">decided by</dt>
								<dd className="text-muted-foreground">
									{/* A model making the call is a materially different thing to be
									    told than a policy rule matching, so the provider's own word
									    for it is carried rather than flattened to "automatically". */}
									{detail.decisionSource}
									{detail.durationMs !== undefined && detail.durationMs > 0
										? ` · ${formatDecisionDuration(detail.durationMs)}`
										: ""}
								</dd>
							</>
						) : null}
					</dl>
				</div>
			) : null}
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* system events                                                               */
/* -------------------------------------------------------------------------- */

/** A provider's note that another model answered. Never drawn: the model picker already names that model. */
export function isModelReroute(item: ConversationItem): boolean {
	return item.kind === "activity" && item.detail?.event === "model.rerouted";
}

/**
 * A provider error item — reconnect storms, credit exhaustion, and similar.
 *
 * Codex (and peers) often put the whole JSON envelope in `message` / `summary`.
 * Rendering that as a generic activity label made one long unbreakable line that
 * widened the chat column under the sidebars, and five reconnect attempts painted
 * five walls of red JSON. This row unwraps the human parts and always wraps inside
 * the column.
 *
 * Not a live region: the enclosing timeline is already a `role="log"`, and the
 * controller banner announces a terminal failure. Marking every historical
 * reconnect row as `role="alert"` would interrupt a screen reader once per attempt.
 */
function ErrorActivityRow({ activity }: { activity: ConversationActivity }) {
	const { headline, detail } = providerErrorCopy(activity);
	const actionUrl = String(activity.detail?.actionUrl ?? "").trim();
	const standaloneActionUrl = actionUrl && !detail?.includes(actionUrl) ? actionUrl : undefined;
	return (
		<div className="flex min-w-0 max-w-full items-baseline overflow-hidden py-0.5 text-xs leading-snug text-muted-foreground">
			<span className="wrap-anywhere min-w-0 whitespace-pre-wrap">
				<span>{linkifiedProviderErrorText(headline)}</span>
				{detail ? (
					<>
						{" — "}
						<span className="text-muted-foreground/80">{linkifiedProviderErrorText(detail)}</span>
					</>
				) : null}
				{standaloneActionUrl ? (
					<>
						{detail ? " " : " — "}
						{isWebLink(standaloneActionUrl) ? (
							<ProviderErrorLink href={standaloneActionUrl} />
						) : (
							<span className="text-muted-foreground/80">{standaloneActionUrl}</span>
						)}
					</>
				) : null}
			</span>
		</div>
	);
}

const providerErrorWebUrlPattern = /\bhttps?:\/\/[^\s<>"'`]+/giu;
const trailingProviderUrlPunctuation = /[),.;!?}\]]+$/u;

function ProviderErrorLink({ href }: { href: string }) {
	return (
		<AppLink
			href={href}
			target="_blank"
			rel="noreferrer noopener"
			onClick={(event) => {
				event.preventDefault();
				void openLinkInSystemBrowser(href);
			}}
			className="text-markdown-link underline decoration-markdown-link/45 underline-offset-2 transition-colors hover:text-markdown-link-hover hover:decoration-markdown-link-hover/75"
		>
			{href}
		</AppLink>
	);
}

/** Render provider prose verbatim, activating only literal HTTP(S) URLs. */
function linkifiedProviderErrorText(text: string): ReactNode[] {
	const parts: ReactNode[] = [];
	let cursor = 0;
	for (const match of text.matchAll(providerErrorWebUrlPattern)) {
		const start = match.index;
		const rawUrl = match[0];
		const href = rawUrl.replace(trailingProviderUrlPunctuation, "");
		if (start > cursor) parts.push(text.slice(cursor, start));
		if (href && isWebLink(href)) {
			parts.push(<ProviderErrorLink key={`${start}-${href}`} href={href} />);
			if (href.length < rawUrl.length) parts.push(rawUrl.slice(href.length));
		} else {
			parts.push(rawUrl);
		}
		cursor = start + rawUrl.length;
	}
	if (cursor < text.length) parts.push(text.slice(cursor));
	return parts;
}

/**
 * Prefer the provider's short status line and `additionalDetails` over a raw JSON
 * dump. Falls back to the stored text when it is already plain language.
 */
export function providerErrorCopy(activity: ConversationActivity): {
	headline: string;
	detail?: string;
} {
	const candidates = [activity.detail?.message, activity.summary, activity.detail?.error];
	for (const candidate of candidates) {
		const raw = String(candidate ?? "").trim();
		if (!raw) continue;
		const unwrapped = unwrapProviderErrorJson(raw);
		if (unwrapped) return unwrapped;
	}

	const headline = String(activity.detail?.message ?? activity.summary ?? "").trim();
	const extra = String(activity.detail?.error ?? "").trim();
	if (!headline) return { headline: extra || "Provider error" };
	if (extra && extra !== headline) return { headline, detail: extra };
	return { headline };
}

function unwrapProviderErrorJson(raw: string): { headline: string; detail?: string } | undefined {
	const parsed = parseJsonObjectSuffix(raw);
	const err = parsed
		? parsed.error && typeof parsed.error === "object" && !Array.isArray(parsed.error)
			? (parsed.error as Record<string, unknown>)
			: parsed
		: undefined;
	const message =
		typeof err?.message === "string"
			? err.message.trim()
			: readJsonStringField(raw, "message");
	const additional =
		typeof err?.additionalDetails === "string"
			? err.additionalDetails.trim()
			: readJsonStringField(raw, "additionalDetails");
	if (!message && !additional) return undefined;
	if (message && additional && additional !== message) {
		return { headline: message, detail: additional };
	}
	return { headline: message || additional };
}

/** Read a complete string field from an envelope whose trailing JSON was truncated. */
function readJsonStringField(raw: string, field: "message" | "additionalDetails"): string {
	const match = new RegExp(`"${field}"\\s*:\\s*("(?:\\\\.|[^"\\\\])*")`).exec(raw);
	if (!match?.[1]) return "";
	try {
		const value = JSON.parse(match[1]) as unknown;
		return typeof value === "string" ? value.trim() : "";
	} catch {
		return "";
	}
}

/** AO used to persist `provider error: {…}`; parse the JSON object even when prefixed. */
function parseJsonObjectSuffix(raw: string): Record<string, unknown> | undefined {
	const start = raw.indexOf("{");
	if (start < 0) return undefined;
	const slice = raw.slice(start).trim();
	const asObject = (value: unknown): Record<string, unknown> | undefined => {
		if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
		return value as Record<string, unknown>;
	};
	try {
		return asObject(JSON.parse(slice));
	} catch {
		const end = slice.lastIndexOf("}");
		if (end <= 0) return undefined;
		try {
			return asObject(JSON.parse(slice.slice(0, end + 1)));
		} catch {
			return undefined;
		}
	}
}

/**
 * Where the provider stopped accepting work until someone signs in.
 *
 * The banner above the timeline is what the user acts on; this row is the record of
 * when it happened, so a turn that failed for this reason is not left looking like an
 * ordinary failure.
 */
/** Whole seconds for a short duration, minutes and seconds past a minute. */
function setupDuration(durationMs: number): { minutes: number; seconds: number } {
	const total = Math.max(0, Math.round(durationMs / 1000));
	return { minutes: Math.floor(total / 60), seconds: total % 60 };
}

/**
 * How the project's Setup commands (Project settings > Scripts) ended, before
 * the agent started. Compact like the other system rows; the failed step's
 * output (the tail the worker kept, already redacted) opens below it in
 * monospace. Workers that publish workspace.script.step events get the
 * ScriptRunPanel instead; this row stays for workers that predate them.
 */
function ProjectSetupRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const detail = activity.detail;
	const status = detail?.setupStatus ?? (activity.status === "completed" ? "succeeded" : "failed");
	const step = detail?.setupStep ?? 1;
	const duration = typeof detail?.durationMs === "number" ? setupDuration(detail.durationMs) : undefined;
	const durationText = duration
		? duration.minutes > 0
			? t("chat.projectSetup.durationMinutes", { minutes: duration.minutes, seconds: duration.seconds })
			: t("chat.projectSetup.durationSeconds", { seconds: duration.seconds })
		: undefined;
	const headline = status === "succeeded"
		? durationText ? t("chat.projectSetup.succeededIn", { duration: durationText }) : t("chat.projectSetup.succeeded")
		: status === "timed_out"
			? t("chat.projectSetup.timedOut", { step })
			: typeof detail?.exitCode === "number" && detail.exitCode >= 0
				? t("chat.projectSetup.failedWithCode", { step, code: detail.exitCode })
				: t("chat.projectSetup.failed", { step });
	const failed = status !== "succeeded";
	const stepOutput = detail?.output?.trim() ? stripAnsi(detail.output) : undefined;
	// The failed step's command, who and where it ran, and its full log, one
	// fact per line above its output.
	const setupFailure = failed ? detail?.setupFailure : undefined;
	const contextLines: string[] = [];
	if (setupFailure?.command) contextLines.push(t("chat.projectSetup.stepCommand", { step, command: setupFailure.command }));
	if (setupFailure?.user) {
		contextLines.push(setupFailure.groups?.length
			? t("chat.projectSetup.ranAsWithGroups", { user: setupFailure.user, groups: setupFailure.groups.join(" ") })
			: t("chat.projectSetup.ranAs", { user: setupFailure.user }));
	}
	if (setupFailure?.dir) contextLines.push(t("chat.projectSetup.ranIn", { dir: setupFailure.dir }));
	if (setupFailure?.logPath) contextLines.push(t("chat.projectSetup.fullOutput", { path: setupFailure.logPath }));
	const context = contextLines.join("\n");
	const output = context ? (stepOutput ? `${context}\n\n${stepOutput}` : context) : stepOutput;
	const hint = failed ? detail?.setupHint?.trim() : undefined;
	const Icon = failed ? CircleAlert : SquareTerminal;
	return (
		<div className={cn("flex flex-col gap-1.5 rounded-md border bg-surface/60 px-3 py-2", failed ? "border-destructive/40" : "border-border")} data-testid="project-setup-row">
			<div className="flex items-center gap-2.5">
				<Icon aria-hidden="true" className={cn("size-3.5 shrink-0", failed ? "text-destructive" : "text-muted-foreground")} />
				<span className={cn("min-w-0 flex-1 text-xs", failed ? "text-destructive" : "text-foreground")}>{headline}</span>
				{output ? (
					<Pressable
						aria-expanded={expanded}
						className="inline-flex shrink-0 items-center gap-1 rounded text-xs text-muted-foreground hover:text-foreground"
						onClick={() => setExpanded((value) => !value)}
					>
						{expanded ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
						{expanded ? t("chat.projectSetup.hideOutput") : t("chat.projectSetup.showOutput")}
					</Pressable>
				) : null}
			</div>
			{hint ? <p className="pl-6 text-xs text-muted-foreground">{hint}</p> : null}
			{output && expanded ? (
				<pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-background/60 px-2.5 py-2 font-mono text-[12px] leading-5 text-muted-foreground select-text">{output}</pre>
			) : null}
		</div>
	);
}

/** Where an account switch stopped the turn: not a failure, the conversation
 * continues on the new account with the next message. */
function AccountSwitchedRow({ accountId }: { accountId?: string }) {
	const { t } = useTranslation();
	const label = useAgentAccountLabel();
	return <TwoRowTimelineMarker message={t("chat.accountSwitched", { email: label(accountId) })} detail={t("chat.accountSwitchedDetail")} wrapDetail />;
}

function ReauthRow({ activity }: { activity: ConversationActivity }) {
	return (
		<div className="flex items-start gap-2.5 rounded-md border border-destructive/40 bg-surface px-3 py-2">
			<KeyRound aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<strong className="text-xs font-medium text-destructive">
					The provider asked you to sign in again
				</strong>
				{activity.detail?.reason ? (
					<span className="text-xs leading-snug text-muted-foreground">
						{activity.detail.reason}
					</span>
				) : null}
			</div>
		</div>
	);
}

/**
 * Guidance the user delivered into a turn that was already running.
 *
 * Drawn as the user's own words, because they are — the daemon records it as an
 * activity only because a message would have opened a second turn. It carries a
 * marker rather than looking identical to an ordinary message: a reader scrolling
 * back needs to see that this arrived mid-turn, since it explains why the agent
 * changed course without a new exchange starting.
 */
export function SteerMessage({
	activity,
	sessionId,
	apiBaseUrl = getApiBaseUrl(),
}: {
	activity: ConversationActivity;
	sessionId: string;
	apiBaseUrl?: string | null;
}) {
	const [expanded, setExpanded] = useState(false);
	const text = activity.detail?.text ?? activity.summary;
	const senderSessionId = activity.detail?.senderSessionId?.trim();
	const senderProjectId = activity.detail?.senderProjectId?.trim();
	const senderLabel = activity.detail?.senderDisplayName?.trim() || senderSessionId;
	const automationSteer = Boolean(senderSessionId);
	const senderHref = senderSessionId && senderProjectId
		? `ao://sessions/${encodeURIComponent(senderProjectId)}/${encodeURIComponent(senderSessionId)}`
		: undefined;
	const visibleText = senderSessionId ? stripSteerSenderPrefix(text, senderSessionId) : text;
	const { body, attachments } = stagedAttachmentParts(visibleText);
	// Only the person's own steer is marked; a steer from another session is not theirs.
	const renderText = useUserTextRenderer(sessionId, body, attachments, apiBaseUrl);
	const longReport = body.length > ORIGIN_REPORT_COLLAPSE_AT;
	const preview = longReport
		? `${body.slice(0, ORIGIN_REPORT_PREVIEW_LENGTH).trimEnd()}…`
		: body;
	let stagedImagesToMatch = attachments.filter((path) => IMAGE_ATTACHMENT_PATH.test(path)).length;
	// Composer images are recorded twice: once as durable staged paths and once as
	// native prompt blocks. Suppress only the corresponding leading native images;
	// later image blocks may be distinct provider content and must remain visible.
	const remainingContent = (activity.detail?.content ?? []).filter((item) => {
		if (item.type !== "image" || stagedImagesToMatch === 0) return true;
		stagedImagesToMatch -= 1;
		return false;
	});
	const automationBody = (
		<>
			{longReport && expanded ? (
				<ChatMarkdown text={body} muted />
			) : body ? (
				<p className={cn("whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground", longReport && "line-clamp-3")}>
					<SessionLinkedText text={preview} />
				</p>
			) : null}
				<StagedAttachmentItems
					paths={attachments}
					sessionId={sessionId}
					apiBaseUrl={apiBaseUrl}
					ariaLabel="Steered attachments"
					className={cn(body && "mt-2")}
				/>
				<ConversationContentItems
					content={remainingContent}
					ariaLabel={attachments.length > 0 ? "Steered content" : "Steered attachments"}
					imageLabel="Image"
					imageAlt={(position) => `Steered attachment ${position}`}
					className={cn((body || attachments.length > 0) && "mt-2")}
				/>
			{longReport ? <AutomationExpandButton expanded={expanded} onClick={() => setExpanded((current) => !current)} /> : null}
		</>
	);
	return (
		<div className={cn("flex flex-col gap-1", automationSteer ? "items-stretch" : "items-end")}>
			{automationSteer ? (
				<AutomationMessageFrame
					label={<>[from {senderHref ? <SessionLabelLink href={senderHref}>{senderLabel}</SessionLabelLink> : senderLabel}]</>}
					createdAt={activity.createdAt}
				>
					{automationBody}
				</AutomationMessageFrame>
			) : (
				<>
					{/* As on a sent message: attachments above the text, outside its bubble. */}
					{attachments.length > 0 || remainingContent.length > 0 ? (
						<AttachmentStrip>
							<StagedAttachmentItems
								paths={attachments}
								sessionId={sessionId}
								apiBaseUrl={apiBaseUrl}
								ariaLabel="Steered attachments"
								className="contents"
							/>
							<ConversationContentItems
								content={remainingContent}
								ariaLabel={attachments.length > 0 ? "Steered content" : "Steered attachments"}
								imageLabel="Image"
								imageAlt={(position) => `Steered attachment ${position}`}
								className="contents"
							/>
						</AttachmentStrip>
					) : null}
					{body || (attachments.length === 0 && remainingContent.length === 0) ? (
						<div className="break-words text-sm leading-[1.55] w-fit max-w-[min(78%,560px)] rounded-[10px] bg-raised px-3 py-2.5 text-foreground">
							{body ? (
								<div className={USER_BODY_CLASS} onCopy={(event) => copyWholeMessageAsTyped(event, body)}>
									<UserMarkdown text={body} renderText={renderText} />
								</div>
							) : null}
						</div>
					) : null}
				</>
			)}
			<span className="flex items-center gap-1 text-xs text-muted-foreground">
				<CornerDownRight aria-hidden="true" className="size-3" />
				Steered
			</span>
		</div>
	);
}

export function stripSteerSenderPrefix(text: string, senderSessionId: string): string {
	const escaped = senderSessionId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const prefix = new RegExp(`^\\[from (?:worker\\s+)?${escaped}(?:\\s+"[^"]*")?\\]\\s*`);
	return text.replace(prefix, "");
}

/* -------------------------------------------------------------------------- */
/* approval                                                                    */
/* -------------------------------------------------------------------------- */

/**
 * A decision the agent is blocked on.
 *
 * Decisions come from `activity.decisions` — the provider's own list — never from
 * a fixed set. The UI still presents common permission choices with AO/Codex copy
 * so provider-flavored labels do not leak into the chat surface.
 */
export function ApprovalCard({
	activity,
	onDecide,
	busy,
	embedded,
}: {
	activity: ConversationActivity;
	onDecide?: (requestId: string, decisionId: string) => void;
	busy?: boolean;
	/** Render inside the shared chat composer instead of drawing another card shell. */
	embedded?: boolean;
}) {
	const resolved = activity.status !== "pending";
	const decisions = orderedApprovalDecisions(activity.decisions ?? []);
	const detail = activity.detail;
	const command = detail?.command ?? activity.summary;
	const subjectKind = approvalSubjectKind(activity);
	const rejectOnceDecision = decisions.find(
		(decision) => approvalDecisionKind(decision) === "reject_once",
	);
	const denyDecision =
		rejectOnceDecision ??
		decisions.find((decision) => approvalDecisionKind(decision) === "reject_always");
	const allowOnceDecision = decisions.find(
		(decision) => approvalDecisionKind(decision) === "allow_once",
	);
	const alternateAllowDecisions = allowOnceDecision
		? decisions.filter((decision) => approvalDecisionKind(decision) === "allow_always")
		: [];
	const otherDecisions = decisions.filter(
		(decision) =>
			decision !== denyDecision &&
			decision !== allowOnceDecision &&
			!alternateAllowDecisions.includes(decision),
	);
	// Some agents ask a question through a permission request, offering each
	// answer as its own option of the same kind. A generic per-kind label would
	// render identical buttons, so a kind offered more than once keeps the
	// provider's own wording.
	const repeatedKinds = repeatedDecisionKinds(decisions);
	const decisionLabel = (decision: DecisionOption) =>
		repeatedKinds.has(approvalDecisionKind(decision) ?? "") && decision.label.trim()
			? decision.label
			: approvalDecisionLabel(decision, subjectKind);
	const requestId = activity.requestId ?? "";
	const cardRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		if (!embedded || resolved || busy || !requestId) return;
		const card = cardRef.current;
		if (card && (document.activeElement === document.body || !document.activeElement)) card.focus();
	}, [busy, embedded, requestId, resolved]);

	if (resolved) {
		return <ResolvedApprovalRow activity={activity} command={command} />;
	}

	return (
		<div
			ref={cardRef}
			role="group"
			tabIndex={-1}
			aria-label={`Approval request ${requestId}`.trim()}
			className={cn(
				"cursor-chat-activity-panel",
				embedded ? "px-1 py-0.5" : "rounded-lg border border-border px-3 py-2.5",
			)}
			onKeyDown={(event) => {
				if (busy || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
				if (event.key === "Escape" && rejectOnceDecision) {
					event.preventDefault();
					onDecide?.(requestId, rejectOnceDecision.id);
					return;
				}
				const target = event.target;
				const interactive =
					target instanceof HTMLElement &&
					Boolean(target.closest("button, a, input, textarea, select, [contenteditable='true']"));
				if (event.key === "Enter" && !event.shiftKey && !interactive && allowOnceDecision) {
					event.preventDefault();
					onDecide?.(requestId, allowOnceDecision.id);
				}
			}}
		>
			<div className="flex flex-col">
				<p className="whitespace-pre-wrap text-sm leading-[1.4] text-foreground/90">
					{detail?.reason ?? approvalPrompt(subjectKind)}
				</p>

				<pre className="mt-2 scrollbar-none max-h-32 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border/70 bg-background/45 px-2.5 py-1.5 font-mono text-xs leading-[1.45] text-muted-foreground">
					{detail?.rawCommand ?? command}
				</pre>

				<div className="mt-2 flex flex-wrap justify-end gap-1.5">
					{denyDecision ? (
						<Button
							type="button"
							variant="secondary"
							size="sm"
							shape="pill"
							disabled={busy}
							onClick={() => onDecide?.(requestId, denyDecision.id)}
						>
							{decisionLabel(denyDecision)}
							{denyDecision === rejectOnceDecision ? (
								<kbd className="rounded-full bg-foreground/10 px-1.5 py-0.5 font-sans text-caption leading-none text-muted-foreground">
									Esc
								</kbd>
							) : null}
						</Button>
					) : null}

					{allowOnceDecision ? (
						<div className="flex overflow-hidden rounded-full">
							<Button
								type="button"
								variant="accent"
								size="sm"
								className="rounded-none"
								disabled={busy}
								onClick={() => onDecide?.(requestId, allowOnceDecision.id)}
							>
								{decisionLabel(allowOnceDecision)}
								<kbd className="rounded-full bg-logo-accent-foreground/15 p-0.5" aria-label="Press Return">
									<CornerDownLeft aria-hidden="true" className="size-3" />
								</kbd>
							</Button>
							{alternateAllowDecisions.length > 0 ? (
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button
											type="button"
											variant="accent"
											size="icon-sm"
											aria-label="More approval options"
											className="rounded-none border-l border-l-logo-accent-foreground/20"
											disabled={busy}
										>
											<ChevronDown aria-hidden="true" className="size-3.5" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent
										align="end"
										side="bottom"
										className="min-w-52"
										data-approval-menu=""
										onEscapeKeyDown={(event) => event.stopPropagation()}
									>
										{alternateAllowDecisions.map((decision) => (
											<DropdownMenuItem
												key={decision.id}
												disabled={busy}
												onSelect={() => onDecide?.(activity.requestId ?? "", decision.id)}
											>
												{decisionLabel(decision)}
											</DropdownMenuItem>
										))}
									</DropdownMenuContent>
								</DropdownMenu>
							) : null}
						</div>
					) : null}
					{otherDecisions.map((decision) => (
						<Button
							key={decision.id}
							type="button"
							variant="secondary"
							size="sm"
							shape="pill"
							disabled={busy}
							onClick={() => onDecide?.(requestId, decision.id)}
						>
							{decisionLabel(decision)}
						</Button>
					))}
					{decisions.length === 0 ? (
						<p className="text-xs text-warning">
							The agent offered no decisions Orchestrator can present. Open diagnostics.
						</p>
					) : null}
				</div>
			</div>
		</div>
	);
}

function approvalSubjectKind(activity: ConversationActivity): ActivityKind | undefined {
	if (activity.detail?.subjectKind) return activity.detail.subjectKind;
	if (activity.detail?.method === "item/fileChange/requestApproval") return "file_change";
	if (activity.detail?.method === "item/commandExecution/requestApproval") return "command";
	return undefined;
}

function approvalPrompt(subjectKind?: ActivityKind): string {
	return subjectKind === "file_change"
		? "Do you want to allow these file changes?"
		: "Do you want to run this command?";
}

function approvalDecisionKind(decision: DecisionOption): DecisionOption["kind"] | "unknown" {
	return decision.kind ?? "unknown";
}

function approvalDecisionRank(decision: DecisionOption): number {
	switch (approvalDecisionKind(decision)) {
		case "reject_once":
			return 10;
		case "reject_always":
			return 15;
		case "allow_once":
			return 20;
		case "allow_always":
			return 30;
		default:
			return 40;
	}
}

function approvalDecisionLabel(decision: DecisionOption, subjectKind?: ActivityKind): string {
	switch (approvalDecisionKind(decision)) {
		case "allow_always":
			return subjectKind === "file_change"
				? "Always allow these file changes"
				: "Always allow this command";
		case "allow_once":
			return "Allow once";
		case "reject_once":
			return "Deny";
		case "reject_always":
			return "Always deny";
		default:
			return decision.label;
	}
}

function repeatedDecisionKinds(decisions: DecisionOption[]): Set<string> {
	const seen = new Set<string>();
	const repeated = new Set<string>();
	for (const decision of decisions) {
		const kind = approvalDecisionKind(decision);
		if (!kind) continue;
		if (seen.has(kind)) repeated.add(kind);
		seen.add(kind);
	}
	return repeated;
}

function orderedApprovalDecisions(decisions: DecisionOption[]): DecisionOption[] {
	return decisions
		.map((decision, index) => ({ decision, index }))
		.sort((left, right) => {
			const byRank = approvalDecisionRank(left.decision) - approvalDecisionRank(right.decision);
			return byRank || left.index - right.index;
		})
		.map(({ decision }) => decision);
}

function ResolvedApprovalRow({
	activity,
	command,
}: {
	activity: ConversationActivity;
	command: string;
}) {
	const detail = activity.detail;
	const decision = typeof detail?.decision === "string" ? detail.decision : undefined;
	const decisionKind = activity.decisions?.find((option) => option.id === decision)?.kind;
	const outcome = resolvedApprovalOutcome(activity.status, decision, detail?.resolvedBy, decisionKind);
	const title = detail?.cwd ? `${command}\n${detail.cwd}` : command;

	return (
		<div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-3 rounded-md border border-border/80 bg-surface/45 px-2.5 py-1.5 text-xs text-muted-foreground">
			<strong className="shrink-0 font-medium text-foreground">{outcome.label}</strong>
			<span className="min-w-0 truncate text-right font-mono" title={title}>
				{command}
			</span>
		</div>
	);
}

function resolvedApprovalOutcome(
	status: ConversationActivity["status"],
	decision?: string,
	resolvedBy?: string,
	decisionKind?: DecisionOption["kind"],
): { label: string; success: boolean } {
	const value = decision?.toLowerCase() ?? "";
	if (status === "failed") return { label: "Approval expired", success: false };
	if (
		status === "cancelled" ||
		decisionKind === "reject_once" ||
		decisionKind === "reject_always" ||
		/(deny|decline|reject|cancel)/.test(value)
	) {
		return { label: "Cancelled", success: false };
	}
	if (decisionKind === "allow_always" || /(remember|always|amendment|policy)/.test(value)) {
		return { label: "Approved and remembered", success: true };
	}
	if (decisionKind === "allow_once" || /(allow|approve|accept)/.test(value)) {
		return { label: "Approved", success: true };
	}
	if (resolvedBy === "provider") return { label: "Resolved elsewhere", success: false };
	return { label: "Resolved", success: false };
}

/* -------------------------------------------------------------------------- */
/* turn diff                                                                   */
/* -------------------------------------------------------------------------- */

/** Single letter per change kind, which is how a diff list is normally read. */
const diffStatusMark: Record<DiffStatus, { mark: string; tone: string; label: string }> = {
	added: { mark: "A", tone: "text-success", label: "added" },
	modified: { mark: "M", tone: "text-accent", label: "modified" },
	deleted: { mark: "D", tone: "text-destructive", label: "deleted" },
	renamed: { mark: "R", tone: "text-muted-foreground", label: "renamed" },
};

/** Match the daemon's file_change summary verbs (Created/Deleted/Renamed/Edited). */
function fileChangeVerb(status: DiffStatus): string {
	switch (status) {
		case "added":
			return "Created";
		case "deleted":
			return "Deleted";
		case "renamed":
			return "Renamed";
		default:
			return "Edited";
	}
}

function liveFileChangeVerb(status: DiffStatus): string {
	switch (status) {
		case "added":
			return "Creating";
		case "deleted":
			return "Deleting";
		case "renamed":
			return "Renaming";
		default:
			return "Editing";
	}
}

function fileChangeIcon(status: DiffStatus): StepIcon {
	switch (status) {
		case "added":
			return "file-new";
		case "deleted":
			return "delete";
		case "renamed":
			return "move";
		default:
			return "write";
	}
}

/** Fills a 5:4 tile with a page laid out 1000 wide, as the Summary's previews do. */
const TURN_ARTIFACT_PAGE_HEIGHT = 800;

/**
 * The end-of-turn results: deliverables the turn created or edited as preview
 * cards, then every other changed file in the changed-files card. An artifact
 * is the thing the reader asked for, not a code change to review, so it never
 * appears in both.
 */
export function TurnResults({
	diff,
	reportedArtifacts,
	items,
	onReview,
	onOpenFile,
	onOpenArtifact,
}: {
	/** Absent for an agent that reports no diffs, or a turn that only wrote artifacts. */
	diff?: TurnDiff;
	/** The artifacts the daemon saw the turn create or edit. */
	reportedArtifacts?: TurnArtifactChange[];
	items?: ConversationItem[];
	onReview?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	/** Opens an artifact in the inspector's Artifacts tab, by its artifact path. */
	onOpenArtifact?: (target: { path: string }) => void;
}) {
	const source = useChatArtifactFiles();
	const pathHints = useMemo(() => turnPathHints(items), [items]);
	const split = useMemo(
		() => splitTurnArtifacts(diff, source?.sessionId, source?.artifacts, (path) => resolveTurnFilePath(path, pathHints), reportedArtifacts),
		[diff, source?.sessionId, source?.artifacts, pathHints, reportedArtifacts],
	);
	const files = useMemo<TurnDiff | undefined>(() => (diff ? { ...diff, files: split.files } : undefined), [diff, split.files]);
	return (
		<>
			{split.artifacts.length > 0 && source ? (
				<TurnArtifacts artifacts={split.artifacts} sessionId={source.sessionId} cloud={source.cloud} onOpenArtifact={onOpenArtifact} />
			) : null}
			{files ? <TurnChangedFiles diff={files} items={items} onReview={onReview} onOpenFile={onOpenFile} /> : null}
		</>
	);
}

/** Preview cards for the deliverables one turn created or edited. */
export function TurnArtifacts({
	artifacts,
	sessionId,
	cloud,
	onOpenArtifact,
}: {
	artifacts: TurnArtifact[];
	sessionId: string;
	cloud?: ArtifactRow["cloud"];
	/**
	 * Opens the artifact by its artifact path. Not the workspace-file opener: a
	 * cloud chat resolves that against workspace files, which never holds the
	 * artifact, so a card would open the wrong file or none.
	 */
	onOpenArtifact?: (target: { path: string }) => void;
}) {
	return (
		<section aria-label={artifacts.length === 1 ? "Artifact" : `${artifacts.length} Artifacts`} className="flex min-w-0 flex-col gap-2">
			<span className="text-xs text-muted-foreground">
				{artifacts.length === 1 ? "1 Artifact" : `${artifacts.length} Artifacts`}
			</span>
			<ul className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-2.5">
				{artifacts.map(({ artifact, status }) => {
					const row: ArtifactRow = { ...artifact, sessionId, sessionTitle: "", cloud };
					const verb = fileChangeVerb(status);
					const body = (
						<>
							<span className="relative block aspect-[5/4] overflow-hidden rounded-md bg-[var(--solid-muted,var(--muted))] ring-1 ring-border transition-shadow group-hover:ring-foreground/20 group-focus-visible:ring-foreground/20">
								<ArtifactThumbnail key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`} pageHeight={TURN_ARTIFACT_PAGE_HEIGHT} row={row} />
							</span>
							<span className="flex min-w-0 flex-col">
								<span className="truncate text-caption font-medium leading-4 text-foreground">{artifact.name}</span>
								<span className="truncate text-caption leading-4 text-passive">{verb}</span>
							</span>
						</>
					);
					const tileClass = "group flex w-full min-w-0 flex-col gap-1.5 rounded-md text-left focus-visible:ring-1 focus-visible:ring-ring";
					return (
						<li key={artifact.path} className="min-w-0">
							{onOpenArtifact ? (
								<Pressable
									aria-label={`Open ${artifact.name}, ${verb.toLowerCase()} in this turn`}
									className={tileClass}
									onClick={() => onOpenArtifact({ path: artifact.path })}
									title={artifact.path}
								>
									{body}
								</Pressable>
							) : (
								<div className={tileClass} title={artifact.path}>{body}</div>
							)}
						</li>
					);
				})}
			</ul>
		</section>
	);
}

/**
 * What a turn changed on disk.
 *
 * A bordered summary card at the end of the turn (kept near rollback), not the
 * compact explore line used for mid-turn activity. Always shows the changed
 * files; Review opens the Files rail, and clicking a row focuses that path.
 *
 * Rendered only when the daemon reported a diff. An agent that cannot report one
 * gets no empty panel implying it changed nothing.
 */
export function TurnChangedFiles({
	diff,
	live,
	onReview,
	onOpenFile,
	items,
}: {
	diff: TurnDiff;
	live?: boolean;
	/** Opens the session Files inspector for the full workspace diff. */
	onReview?: () => void;
	/** Opens the Files inspector focused on this path. */
	onOpenFile?: (path: string, line?: number) => void;
	/**
	 * Timeline items from the same turn. Turn diffs often carry repo-relative
	 * basenames (`random_words.txt`); file_change rows and command cwds often
	 * carry the absolute worktree path the Edited tooltip already shows.
	 */
	items?: ConversationItem[];
}) {
	const [expanded, setExpanded] = useState(false);
	const pathHints = useMemo(() => turnPathHints(items), [items]);
	if (diff.files.length === 0) return null;

	const previewLimit = 4;
	const hidden = Math.max(0, diff.files.length - previewLimit);
	const visible = expanded ? diff.files : diff.files.slice(0, previewLimit);

	return (
		<div className="overflow-hidden rounded-lg bg-surface">
			<div className="flex items-center gap-2 px-3 py-2">
				<span className="shrink-0 text-xs text-muted-foreground">
					{diff.files.length === 1 ? "1 File Changed" : `${diff.files.length} Files Changed`}
				</span>
				{live ? (
					<Loader2
						aria-label="still changing"
						className="size-3 shrink-0 animate-spin text-muted-foreground/60"
					/>
				) : null}
				<span className="flex-1" />
				{onReview ? (
					<Button
						type="button"
						variant="quiet"
						size="xs"
						onClick={onReview}
					>
						Review
					</Button>
				) : null}
			</div>

			<ul className="flex flex-col px-1.5 pb-1.5">
				{visible.map((file) => {
					const status = diffStatusMark[file.status] ?? diffStatusMark.modified;
					const rowClass =
						"flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-interactive-hover";
					const tooltipPath = resolveTurnFilePath(file.path, pathHints);
					const tooltipOldPath = file.oldPath
						? resolveTurnFilePath(file.oldPath, pathHints)
						: undefined;
					const openPath = turnFileOpenPath(file.path, pathHints);
					const location = fileLocationLabel(tooltipPath, tooltipOldPath);

					const body = (
						<>
							<span className="sr-only">{status.label}</span>
							<FileIcon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
							<span
								className="min-w-0 flex-1 truncate text-xs text-foreground/80"
								title=""
							>
								{fileBasename(file.path)}
							</span>
							{file.additions > 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-success">
									+{file.additions}
								</span>
							) : null}
							{file.deletions > 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-destructive">
									&minus;{file.deletions}
								</span>
							) : null}
							{file.additions === 0 && file.deletions === 0 ? (
								<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground/50">
									0
								</span>
							) : null}
						</>
					);

					return (
						<li key={`${file.status}-${file.oldPath ?? ""}-${file.path}`}>
							{onOpenFile ? (
								<Tooltip>
									<TooltipTrigger asChild>
										<Pressable
											onClick={() => onOpenFile(tooltipPath)}
											aria-label={`Open ${openPath} in Files`}
											className={rowClass}
										>
											{body}
										</Pressable>
									</TooltipTrigger>
									<TooltipContent side="top" className="max-w-[min(28rem,90vw)] font-mono text-caption font-normal">
										{location}
									</TooltipContent>
								</Tooltip>
							) : (
								<div className={rowClass}>
									<span className="sr-only">{status.label}</span>
									<FileIcon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
									<FileLocationLabel
										path={file.path}
										oldPath={file.oldPath}
										locationPath={tooltipPath}
										locationOldPath={tooltipOldPath}
										className="min-w-0 flex-1 truncate text-xs text-foreground/80"
									/>
									{file.additions > 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-success">
											+{file.additions}
										</span>
									) : null}
									{file.deletions > 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-destructive">
											&minus;{file.deletions}
										</span>
									) : null}
									{file.additions === 0 && file.deletions === 0 ? (
										<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground/50">
											0
										</span>
									) : null}
								</div>
							)}
						</li>
					);
				})}
			</ul>

			{hidden > 0 ? (
				<Pressable
					onClick={() => setExpanded((prev) => !prev)}
					aria-expanded={expanded}
					className="flex w-full items-center gap-1.5 px-3 pb-2 text-left text-xs text-muted-foreground transition-colors hover:text-foreground"
				>
					{expanded ? "Show less" : `Show ${hidden} more`}
				</Pressable>
			) : null}

			{diff.truncated ? (
				<p className="px-3 pb-2 text-xs leading-relaxed text-warning">
					This turn changed more files than Orchestrator lists here.
					{onReview ? " Use Review for the whole change." : " Open the Files tab for the whole change."}
				</p>
			) : null}
		</div>
	);
}

/**
 * Basename only — color distinguishes it from "Edited", no hover fill. Hovering
 * shows the home-shortened worktree path in a monospace tooltip.
 */
function FileLocationLabel({
	path,
	oldPath,
	locationPath,
	locationOldPath,
	className,
}: {
	path: string;
	oldPath?: string;
	/** Absolute/worktree path for the tooltip when `path` is only a basename. */
	locationPath?: string;
	locationOldPath?: string;
	className?: string;
}) {
	const location = fileLocationLabel(locationPath ?? path, locationOldPath ?? oldPath);

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				{/* `title=""` blocks Chromium's native ellipsis tooltip so only the
				    path tooltip below appears — otherwise hover shows the basename. */}
				<span
					className={cn(
						"min-w-0 truncate text-xs text-foreground/65 outline-none",
						className,
					)}
					title=""
				>
					{fileBasename(path)}
				</span>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-[min(28rem,90vw)] font-mono text-caption font-normal">
				{location}
			</TooltipContent>
		</Tooltip>
	);
}

/**
 * What a command step acted on, after its verb: a file, a pattern, a PR number.
 * Styled like a file edit's name. A shortened target keeps the whole one in a tooltip.
 */
function StepTarget({ text, title }: { text: string; title?: string }) {
	const label = <span className="min-w-0 truncate text-xs text-foreground/65 outline-none" title="">{text}</span>;
	// A short target that is shown whole needs no tooltip repeating it.
	if ((!title || title === text) && text.length <= 48) return label;
	return (
		<Tooltip>
			<TooltipTrigger asChild>{label}</TooltipTrigger>
			<TooltipContent side="top" className="max-w-[min(28rem,90vw)] font-mono text-caption font-normal">
				{shortenPaths(title ?? text)}
			</TooltipContent>
		</Tooltip>
	);
}

function fileLocationLabel(path: string, oldPath?: string): string {
	return oldPath ? `${shortenPaths(oldPath)} → ${shortenPaths(path)}` : shortenPaths(path);
}

/* -------------------------------------------------------------------------- */
/* turn boundary                                                               */
/* -------------------------------------------------------------------------- */

/** Turn wall-clock duration, after "Worked for" at the head of a settled turn. */
export function TurnDuration({ durationMs }: { durationMs: number }) {
	if (durationMs <= 0) return null;
	// The summary's own color (and the fold's hover color) carries the number with the words.
	return <span className="shrink-0 font-sans text-sm leading-none tabular-nums">{formatDuration(durationMs)}</span>;
}

export { User };
