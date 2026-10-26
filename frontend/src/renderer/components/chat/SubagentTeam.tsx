/**
 * The subagents a turn launched, drawn as one team.
 *
 * Each launch used to vanish and leave its child's commands strewn through the
 * parent's turn. Here every child gets one row in a shared list: who it is, what it
 * is doing right now, how long it has been at it, and whether it finished. A row
 * opens onto that child's own timeline and the report it handed back, so the parent's
 * turn reads as message, team, message instead of as a log of other agents' calls.
 *
 * Motion only marks a change of state: a new child fades into the list, the status
 * glyph swaps when a child finishes, the live line crossfades when the child moves on.
 * Reduced motion turns all of it into immediate state changes.
 */

import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronRight, Loader2, Square, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { ActivityRun } from "./ActivityRun";
import { ChatMarkdown } from "./ChatMarkdown";
import { ActivityTransition } from "./ChatTimelineItems";
import { ACTIVITY_SUMMARY_BUTTON_CLASS } from "./activity-command";
import {
	childMessageText,
	isChildMessage,
	latestSubagentLine,
	readSubagent,
	type Subagent,
	type SubagentLine,
	type SubagentState,
} from "./subagent-team";
import type { ConversationActivity } from "../../types/conversation";

const EASE_OUT = [0.22, 1, 0.36, 1] as const;
/** At most this many avatars stack in the header; the count says the rest. */
const STACKED_AVATARS = 3;
/**
 * Claude's default agent type. It says nothing a reader can act on, so a row names
 * only a specialized type, such as `Explore`, `Plan` or a project's own agent.
 */
const DEFAULT_AGENT_TYPE = "general-purpose";

export function SubagentTeam({
	launches,
	steps,
	live,
	harness,
	disclosureKey,
	disclosureOverrides,
	onDisclosureChange,
}: {
	launches: ConversationActivity[];
	steps: ReadonlyMap<string, ConversationActivity[]>;
	/** The turn that launched the team is still running. */
	live: boolean;
	/** Whose agents these are, for the avatar. */
	harness?: string;
	disclosureKey?: string;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const agents = useMemo(
		() => launches.map((launch) => readSubagent(launch, steps.get(launch.id) ?? [], live)),
		[launches, steps, live],
	);
	const working = agents.some((agent) => agent.state === "working");
	const now = useNow(working);
	const [localOpen, setLocalOpen] = useState<Record<string, boolean>>({});
	// The caller's overrides survive the row scrolling out of a virtualized timeline;
	// without them the choices live here.
	const shared = Boolean(onDisclosureChange && disclosureOverrides);
	const isOpen = (key: string, fallback: boolean) =>
		(shared ? disclosureOverrides! : localOpen)[key] ?? fallback;
	const setOpen = (key: string, open: boolean) => {
		if (shared) onDisclosureChange!(key, open);
		else setLocalOpen((current) => ({ ...current, [key]: open }));
	};
	const teamKey = disclosureKey ?? "subagents";
	// The list is the point of the card, so it starts open; a click folds it.
	const open = isOpen(teamKey, true);
	const counts = countStates(agents);

	return (
		<motion.div
			data-testid="subagent-team"
			initial={reducedMotion ? false : { opacity: 0, y: 4 }}
			animate={{ opacity: 1, y: 0 }}
			transition={{ duration: reducedMotion ? 0 : 0.2, ease: EASE_OUT }}
			className="flex min-w-0 flex-col"
		>
			<button
				type="button"
				onClick={() => setOpen(teamKey, !open)}
				aria-expanded={open}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-run-toggle min-h-7")}
			>
				<span aria-hidden="true" className="flex shrink-0 items-center -space-x-1.5">
					{agents.slice(0, STACKED_AVATARS).map((agent) => (
						<span
							key={agent.launch.id}
							className="flex rounded-full bg-background ring-2 ring-background"
						>
							<AgentAvatar decorative provider={harness ?? "claude-code"} className="size-4" />
						</span>
					))}
				</span>
				<span className="activity-summary-label shrink-0 text-xs font-medium text-foreground/85">
					{t("chat.subagent.count", { count: agents.length })}
				</span>
				<TeamSummary counts={counts} />
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
						open && "rotate-90",
					)}
				/>
			</button>

			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						key="team"
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.2, ease: EASE_OUT }}
						className="overflow-hidden"
					>
						<ul className="cursor-chat-explore-box mt-1 flex min-w-0 flex-col overflow-hidden rounded-lg border">
							<AnimatePresence initial={false}>
								{agents.map((agent) => {
									const agentKey = `${teamKey}:agent:${agent.launch.id}`;
									return (
										<motion.li
											key={agent.launch.id}
											initial={reducedMotion ? false : { opacity: 0, y: 4 }}
											animate={{ opacity: 1, y: 0 }}
											exit={{ opacity: 0 }}
											transition={{ duration: reducedMotion ? 0 : 0.2, ease: EASE_OUT }}
											className="min-w-0 border-t border-border/60 first:border-t-0"
										>
											<SubagentRow
												agent={agent}
												harness={harness}
												now={now}
												open={isOpen(agentKey, false)}
												onOpenChange={(next) => setOpen(agentKey, next)}
												disclosureKey={agentKey}
												disclosureOverrides={disclosureOverrides}
												onDisclosureChange={onDisclosureChange}
											/>
										</motion.li>
									);
								})}
							</AnimatePresence>
						</ul>
					</motion.div>
				) : null}
			</AnimatePresence>
		</motion.div>
	);
}

type StateCounts = Record<SubagentState, number>;

function countStates(agents: readonly Subagent[]): StateCounts {
	const counts: StateCounts = { working: 0, done: 0, failed: 0, stopped: 0, unknown: 0 };
	for (const agent of agents) counts[agent.state] += 1;
	return counts;
}

/** "2 working · 1 done": only the states someone is in, working first. */
function TeamSummary({ counts }: { counts: StateCounts }) {
	const { t } = useTranslation();
	const parts: { key: string; text: string; className: string }[] = [];
	if (counts.working) {
		parts.push({ key: "working", text: t("chat.subagent.summary.working", { count: counts.working }), className: "text-status-working" });
	}
	if (counts.done) {
		parts.push({ key: "done", text: t("chat.subagent.summary.done", { count: counts.done }), className: "text-muted-foreground" });
	}
	if (counts.failed) {
		parts.push({ key: "failed", text: t("chat.subagent.summary.failed", { count: counts.failed }), className: "text-destructive" });
	}
	if (counts.stopped) {
		parts.push({ key: "stopped", text: t("chat.subagent.summary.stopped", { count: counts.stopped }), className: "text-muted-foreground/70" });
	}
	if (parts.length === 0) return null;
	return (
		<span className="min-w-0 truncate text-xs tabular-nums">
			{parts.map((part, index) => (
				<Fragment key={part.key}>
					{index > 0 ? <span className="text-muted-foreground/50"> · </span> : null}
					<span className={part.className}>{part.text}</span>
				</Fragment>
			))}
		</span>
	);
}

function SubagentRow({
	agent,
	harness,
	now,
	open,
	onOpenChange,
	disclosureKey,
	disclosureOverrides,
	onDisclosureChange,
}: {
	agent: Subagent;
	harness?: string;
	now: number;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	disclosureKey: string;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const reducedMotion = useReducedMotion();
	const elapsedMs = (agent.endedAt ?? (agent.state === "working" ? now : agent.startedAt)) - agent.startedAt;
	const showElapsed = agent.state === "working" || elapsedMs >= 1_000;

	return (
		<div className="flex min-w-0 flex-col">
			<button
				type="button"
				onClick={() => onOpenChange(!open)}
				aria-expanded={open}
				// A list row, not a raised button: `activity-summary-button` keeps the chat's
				// press-scale off it, the same as the other timeline disclosures.
				className="activity-summary-button group/agent flex w-full min-w-0 select-none items-start gap-2.5 px-3 py-2 text-left outline-none hover:bg-interactive-hover focus-visible:bg-interactive-hover"
			>
				<SubagentAvatar harness={harness} state={agent.state} />
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="flex min-w-0 items-baseline gap-2">
						<span className="min-w-0 truncate text-xs font-medium text-foreground/90" title={agent.title}>
							{agent.title}
						</span>
						{agent.agentType && agent.agentType !== DEFAULT_AGENT_TYPE ? (
							<span className="shrink-0 text-caption text-muted-foreground/70">{agent.agentType}</span>
						) : null}
					</span>
					<SubagentLineText agent={agent} />
				</span>
				<span className="flex shrink-0 items-center gap-2 pt-px">
					<StateGlyph state={agent.state} />
					{showElapsed ? (
						<span className="text-caption tabular-nums text-muted-foreground/70">
							{formatElapsed(elapsedMs)}
						</span>
					) : null}
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/agent:text-muted-foreground",
							open && "rotate-90",
						)}
					/>
				</span>
			</button>

			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						key="detail"
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: EASE_OUT }}
						className="overflow-hidden"
					>
						<SubagentDetail
							agent={agent}
							disclosureKey={disclosureKey}
							disclosureOverrides={disclosureOverrides}
							onDisclosureChange={onDisclosureChange}
						/>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * The provider mark, with a dot while the child works or after it failed. The dot
 * holds still: the spinner in the status slot already says the child is live.
 */
function SubagentAvatar({ harness, state }: { harness?: string; state: SubagentState }) {
	const reducedMotion = useReducedMotion();
	return (
		<span aria-hidden="true" className="relative mt-px flex shrink-0">
			<AgentAvatar decorative provider={harness ?? "claude-code"} className="size-4" />
			<AnimatePresence initial={false}>
				{state === "working" || state === "failed" ? (
					<motion.span
						key={state}
						initial={reducedMotion ? false : { scale: 0, opacity: 0 }}
						animate={{ scale: 1, opacity: 1 }}
						exit={reducedMotion ? { opacity: 0, transition: { duration: 0 } } : { scale: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.15, ease: EASE_OUT }}
						className={cn(
							"absolute -bottom-0.5 -right-0.5 size-1.5 rounded-full ring-1 ring-background",
							state === "working" ? "bg-status-working" : "bg-destructive",
						)}
					/>
				) : null}
			</AnimatePresence>
		</span>
	);
}

/**
 * The status slot: a spinner while working, then a glyph and a word for anything
 * that did not simply finish, so the state never rests on color alone. Done shows
 * nothing: the report under the name and the header's count already say it, so a
 * check on every row is noise. Screen readers still hear it.
 */
function StateGlyph({ state }: { state: SubagentState }) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	let glyph: ReactNode;
	if (state === "working") {
		glyph = (
			<Loader2
				aria-label={t("chat.subagent.state.working")}
				className="size-3 animate-spin text-muted-foreground/70 motion-reduce:animate-none"
			/>
		);
	} else if (state === "done") {
		glyph = undefined;
	} else if (state === "failed") {
		glyph = (
			<span className="flex items-center gap-1 text-caption text-destructive">
				<X aria-hidden="true" className="size-3" />
				{t("chat.subagent.state.failed")}
			</span>
		);
	} else if (state === "stopped") {
		glyph = (
			<span className="flex items-center gap-1 text-caption text-muted-foreground/70">
				<Square aria-hidden="true" className="size-2.5" />
				{t("chat.subagent.state.stopped")}
			</span>
		);
	} else {
		glyph = <span className="text-caption text-muted-foreground/70">{t("chat.subagent.state.unknown")}</span>;
	}
	return (
		<>
			{state === "done" ? <span className="sr-only">{t("chat.subagent.state.done")}</span> : null}
			{/* Stays mounted when the slot empties, so the spinner still fades out. */}
			<AnimatePresence initial={false} mode="popLayout">
				{glyph ? (
					<motion.span
						key={state}
						initial={reducedMotion ? false : { opacity: 0, scale: 0.8 }}
						animate={{ opacity: 1, scale: 1 }}
						exit={reducedMotion ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, scale: 0.8 }}
						transition={{ duration: reducedMotion ? 0 : 0.16, ease: EASE_OUT }}
						className="flex items-center"
					>
						{glyph}
					</motion.span>
				) : null}
			</AnimatePresence>
		</>
	);
}

/** The one line under the name: the report once it is in, otherwise the latest step. */
function SubagentLineText({ agent }: { agent: Subagent }) {
	const { t } = useTranslation();
	let text: ReactNode;
	let value: string;
	if (agent.state === "failed" && agent.error) {
		value = `error:${agent.error}`;
		text = <span className="text-destructive/90">{firstLine(agent.error)}</span>;
	} else if (agent.state === "done" && agent.report) {
		value = "report";
		text = firstLine(plainText(agent.report));
	} else {
		const line = latestSubagentLine(agent.steps);
		value = lineKey(line);
		if (line.kind === "starting") text = t("chat.subagent.line.starting");
		else if (line.kind === "said") text = line.text;
		else if (line.kind === "tool") text = line.name;
		else {
			const verb = line.kind === "command"
				? t(line.running ? "chat.subagent.line.running" : "chat.subagent.line.ran")
				: t(line.running ? "chat.subagent.line.editing" : "chat.subagent.line.edited");
			text = (
				<>
					{verb}{" "}
					<span className="font-mono">{line.kind === "command" ? line.command : line.path}</span>
				</>
			);
		}
	}
	return (
		<span
			className={cn(
				"block min-w-0 truncate text-caption text-muted-foreground",
				agent.state === "working" && "chat-working-shimmer",
			)}
		>
			<ActivityTransition value={value} inline>
				{text}
			</ActivityTransition>
		</span>
	);
}

function lineKey(line: SubagentLine): string {
	switch (line.kind) {
		case "starting":
			return "starting";
		case "said":
			return `said:${line.text}`;
		case "command":
			return `command:${line.running}:${line.command}`;
		case "edit":
			return `edit:${line.running}:${line.path}`;
		case "tool":
			return `tool:${line.running}:${line.name}`;
	}
}

/**
 * One child's own timeline: the prompt it was given, what it said and ran in order,
 * and the report it handed back.
 */
function SubagentDetail({
	agent,
	disclosureKey,
	disclosureOverrides,
	onDisclosureChange,
}: {
	agent: Subagent;
	disclosureKey: string;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const { t } = useTranslation();
	const sections = stepSections(agent.steps);
	const working = agent.state === "working";
	const lastRunIndex = sections.findLastIndex((section) => section.kind === "run");
	const empty = sections.length === 0 && !agent.report && !agent.error;

	return (
		<div className="flex min-w-0 flex-col gap-1.5 pb-2.5 pl-[38px] pr-3">
			{agent.prompt ? <PromptDisclosure prompt={agent.prompt} /> : null}
			{sections.map((section, index) =>
				section.kind === "said" ? (
					<div key={section.key} className="min-w-0 text-muted-foreground">
						<ChatMarkdown text={section.text} muted className="text-xs leading-relaxed" />
					</div>
				) : (
					<ActivityRun
						key={section.key}
						activities={section.steps}
						disclosureKey={`${disclosureKey}:${section.key}`}
						disclosureOverrides={disclosureOverrides}
						onDisclosureChange={onDisclosureChange}
						live={working && index === lastRunIndex}
					/>
				),
			)}
			{empty ? (
				<span className="text-caption text-muted-foreground/70">
					{t(working ? "chat.subagent.line.starting" : "chat.subagent.empty")}
				</span>
			) : null}
			{agent.error ? (
				<p className="whitespace-pre-wrap text-xs leading-relaxed text-destructive">{agent.error}</p>
			) : null}
			{agent.report ? (
				<div className="mt-1 flex min-w-0 flex-col gap-1 border-t border-border/60 pt-2">
					<span className="text-caption text-muted-foreground/70">{t("chat.subagent.report")}</span>
					<ChatMarkdown text={agent.report} className="text-xs leading-relaxed" />
				</div>
			) : null}
		</div>
	);
}

function PromptDisclosure({ prompt }: { prompt: string }) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const [open, setOpen] = useState(false);
	return (
		<div className="flex min-w-0 flex-col">
			<button
				type="button"
				onClick={() => setOpen(!open)}
				aria-expanded={open}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-row-toggle")}
			>
				<span className="activity-row-label shrink-0 text-xs text-muted-foreground">
					{t("chat.subagent.prompt")}
				</span>
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
						open && "rotate-90",
					)}
				/>
			</button>
			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						key="prompt"
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.18, ease: EASE_OUT }}
						className="overflow-hidden"
					>
						<p className="max-h-48 overflow-auto whitespace-pre-wrap break-words pb-1 text-caption leading-relaxed text-muted-foreground">
							{prompt}
						</p>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

type StepSection =
	| { kind: "said"; key: string; text: string }
	| { kind: "run"; key: string; steps: ConversationActivity[] };

/** What the child wrote breaks its tool calls into runs, as the parent's prose does. */
function stepSections(steps: readonly ConversationActivity[]): StepSection[] {
	const sections: StepSection[] = [];
	for (const step of steps) {
		if (isChildMessage(step)) {
			const text = childMessageText(step);
			if (text) sections.push({ kind: "said", key: step.id, text });
			continue;
		}
		const last = sections.at(-1);
		if (last?.kind === "run") last.steps.push(step);
		else sections.push({ kind: "run", key: `run-${step.id}`, steps: [step] });
	}
	return sections;
}

/** Ticks once a second while something is counting, and not at all otherwise. */
function useNow(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		setNow(Date.now());
		if (!active) return;
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [active]);
	return now;
}

/** Whole seconds, then minutes: the same steps as the turn's own Working row. */
function formatElapsed(ms: number): string {
	if (ms < 60_000) return `${Math.max(1, Math.floor(ms / 1000))}s`;
	const totalMinutes = Math.floor(ms / 60_000);
	if (totalMinutes < 60) return `${totalMinutes}m`;
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
}

function firstLine(text: string): string {
	return text.split("\n").map((line) => line.trim()).find(Boolean) ?? "";
}

/** Enough markdown stripped that a report's first line reads as a sentence. */
function plainText(markdown: string): string {
	return markdown
		.replace(/```[\s\S]*?```/g, " ")
		.replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
		.replace(/^\s{0,3}(?:#{1,6}\s+|[-*+]\s+|\d+\.\s+|>\s?)/gm, "")
		.replace(/[*_`~]+/g, "");
}
