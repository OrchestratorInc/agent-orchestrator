/**
 * A run of consecutive tool calls, collapsed to one line.
 *
 * The agent may run fifteen commands to answer one question. Rendering each as
 * its own row turns the conversation into a log: the prose that actually answers
 * the question gets pushed off screen by the mechanics of finding it.
 *
 * So a run summarizes itself — "Explored 4 files, 3 searches" — and expands to the
 * individual calls only when asked. The summary is not decoration: it counts what
 * the agent did by category, so a reader can tell "it looked around" from "it
 * changed something" without opening anything.
 *
 * It stays folded while the turn runs too: the live row names the step in progress.
 * Only a call that failed shows without being asked for, under the line that folds it.
 */

import { useState, type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronRight, Loader2 } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { Pressable } from "../ui/pressable";
import { ActivityRow, ActivityTransition } from "./ChatTimelineItems";
import { useChatEntrance } from "./chat-entrance";
import {
	ACTIVITY_SUMMARY_BUTTON_CLASS,
	commandCategory,
	isFailedCall,
	isNonzeroCommandExit,
} from "./activity-command";
import { aoActionName, summarizeAOActions } from "./activity-action";
import { activityCommandStep, activityToolStep, isExploratoryTool, type CommandStep } from "./activity-step";
import { fileChangeFiles, type ConversationActivity } from "../../types/conversation";

export function ActivityRun({
	activities,
	disclosureKey,
	disclosureOverrides,
	onDisclosureChange,
}: {
	activities: ConversationActivity[];
	disclosureKey?: string;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const [localOpen, setLocalOpen] = useState(false);
	const open = disclosureKey && disclosureOverrides
		? disclosureOverrides[disclosureKey] ?? false
		: localOpen;
	const updateOverride = (key: string, next: boolean) => {
		if (disclosureKey && onDisclosureChange) onDisclosureChange(key, next);
		else setLocalOpen(next);
	};
	const overrides = disclosureOverrides ?? {};
	const reducedMotion = useReducedMotion();
	// The group fades in when it forms while the chat is on screen, never when the
	// chat opens onto it or it scrolls back into view.
	const entering = useChatEntrance(`run:${activities[0]?.id ?? ""}`, newestSequence(activities));
	const running = activities.some((a) => a.status === "running");
	const nonzeroExits = activities.filter(isNonzeroCommandExit).length;
	const failed = activities.filter(
		(activity) => activity.status === "failed" && !isNonzeroCommandExit(activity),
	).length;
	const cancelled = activities.filter((a) => a.status === "cancelled").length;
	const diffTotals = activities.reduce(
		(totals, activity) => {
			if (activity.activityKind !== "file_change") return totals;
			for (const file of fileChangeFiles(activity)) {
				totals.additions += file.additions;
				totals.deletions += file.deletions;
			}
			return totals;
		},
		{ additions: 0, deletions: 0 },
	);

	// A single call is its own best summary — collapsing one row into a count of
	// one would be worse than just showing it.
	const hierarchy = buildHierarchy(activities);
	const nodesByActivityID = new Map(hierarchy.map((node) => [node.activity.id, node]));
	const rootActivities = hierarchy.map((node) => node.activity);
	const subgroups = groupSimilarActivities(rootActivities);
	const summary = summarize(activities);
	if (activities.length === 1 && hierarchy[0]?.children.length === 0) {
		return <ActivityRow activity={activities[0]!} />;
	}

	return (
		<motion.div
			initial={reducedMotion || !entering ? false : { opacity: 0 }}
			animate={{ opacity: 1 }}
			transition={{ duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] }}
			className="flex flex-col"
		>
			<Pressable
				onClick={() => updateOverride(disclosureKey ?? "", !open)}
				aria-expanded={open}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-run-toggle")}
			>
				<span className="activity-summary-label text-xs text-muted-foreground">
					<ActivityTransition value={summary} inline enter={entering}>{summary}</ActivityTransition>
				</span>
				{nonzeroExits > 0 ? (
					<span className="text-caption text-muted-foreground/70">
						{nonzeroExits} exited
					</span>
				) : null}
				{failed > 0 ? (
					<span className="text-xs text-destructive">
						{failed} failed
					</span>
				) : null}
				{cancelled > 0 ? (
					<span className="text-caption text-muted-foreground/70">
						{cancelled} stopped
					</span>
				) : null}
				{diffTotals.additions > 0 || diffTotals.deletions > 0 ? (
					<span className="shrink-0 font-mono text-micro tabular-nums text-muted-foreground/70">
						{diffTotals.additions > 0 ? (
							<span className="text-success">+{diffTotals.additions}</span>
						) : null}
						{diffTotals.additions > 0 && diffTotals.deletions > 0 ? " " : null}
						{diffTotals.deletions > 0 ? (
							<span className="text-destructive">−{diffTotals.deletions}</span>
						) : null}
					</span>
				) : null}
				{running ? (
					<Loader2 aria-hidden="true" className="size-3 animate-spin text-muted-foreground/60" />
				) : null}
				{/* Always visible: the line has to read as openable, or a reader who
				    wants the detail has no reason to think it is there. */}
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-3 shrink-0 text-muted-foreground/40 transition-transform group-hover/run:text-muted-foreground",
						open && "rotate-90",
					)}
				/>
			</Pressable>

			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden"
					>
						<div className="flex flex-col gap-1 pt-1">
							{subgroups.length === 1
								? renderActivityRows(subgroups[0]!.activities, nodesByActivityID)
								: subgroups.map((group) => (
										<ActivitySubgroup
											key={group.activities.map((activity) => activity.id).join(":")}
											disclosureKey={disclosureKey ? `${disclosureKey}:sub:${group.activities.map((activity) => activity.id).join(":")}` : undefined}
											disclosureOverrides={overrides}
											onDisclosureChange={updateOverride}
											activities={group.activities}
											nodesByActivityID={nodesByActivityID}
										/>
								  ))}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
			<FailedCalls activities={activities} folded={!open} />
		</motion.div>
	);
}

/** The calls that failed, kept on screen while the disclosure holding them is folded. */
function FailedCalls({ activities, folded }: { activities: ConversationActivity[]; folded: boolean }) {
	const reducedMotion = useReducedMotion();
	const failed = activities.filter(isFailedCall);
	return (
		<AnimatePresence initial={false}>
			{folded && failed.length > 0 ? (
				<motion.div
					initial={{ height: 0, opacity: 0 }}
					animate={{ height: "auto", opacity: 1 }}
					exit={{ height: 0, opacity: 0 }}
					transition={{ duration: reducedMotion ? 0 : 0.16, ease: [0.22, 1, 0.36, 1] }}
					className="overflow-hidden"
				>
					<div className="flex flex-col gap-1 pt-1">
						{failed.map((activity) => <ActivityRow key={activity.id} activity={activity} />)}
					</div>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}

function newestSequence(activities: readonly ConversationActivity[]): number {
	return activities.reduce((newest, activity) => Math.max(newest, activity.sequence), 0);
}

/**
 * The rows of a run. Back-to-back commands that finished and read the same
 * ("Checked the browser console" twice) show as one row; opening it still
 * shows every call.
 */
function renderActivityRows(
	activities: readonly ConversationActivity[],
	nodesByActivityID: ReadonlyMap<string, ActivityNode>,
): ReactNode[] {
	const rows: ReactNode[] = [];
	let index = 0;
	while (index < activities.length) {
		const activity = activities[index]!;
		const node = nodesByActivityID.get(activity.id);
		if (node && node.children.length > 0) {
			rows.push(<ActivityTree key={activity.id} node={node} />);
			index += 1;
			continue;
		}
		const label = repeatableLabel(activity);
		let end = index + 1;
		while (
			label &&
			end < activities.length &&
			!nodesByActivityID.get(activities[end]!.id)?.children.length &&
			repeatableLabel(activities[end]!) === label
		) {
			end += 1;
		}
		const last = activities[end - 1]!;
		rows.push(<ActivityRow key={activity.id} activity={last} repeats={activities.slice(index, end - 1)} />);
		index = end;
	}
	return rows;
}

/** The label two finished commands must share to fold into one row, or undefined. */
function repeatableLabel(activity: ConversationActivity): string | undefined {
	if (activity.activityKind !== "command" || activity.status !== "completed") return undefined;
	const step = activityCommandStep(activity);
	return [step.done, step.target].filter(Boolean).join(" ");
}

type ActivitySubgroupData = { key: string; activities: ConversationActivity[] };

function groupSimilarActivities(activities: ConversationActivity[]): ActivitySubgroupData[] {
	const groups: ActivitySubgroupData[] = [];
	for (const activity of activities) {
		const key = activityGroupKey(activity);
		const previous = groups.at(-1);
		if (previous?.key === key) previous.activities.push(activity);
		else groups.push({ key, activities: [activity] });
	}
	return groups;
}

function activityGroupKey(activity: ConversationActivity): string {
	const parentSuffix = activity.detail?.parentProviderItemId ? ":nested" : "";
	if (activity.activityKind === "command") {
		return `command:${commandCategory(activity.detail?.command ?? activity.summary)}${parentSuffix}`;
	}
	if (activity.activityKind === "mcp_tool") {
		return `mcp_tool:${activityToolStep(activity)?.icon ?? "other"}${parentSuffix}`;
	}
	if (activity.activityKind === "ao_action") {
		return `ao_action:${aoActionName(activity) || "unknown"}${parentSuffix}`;
	}
	return `${activity.activityKind}${parentSuffix}`;
}

function ActivitySubgroup({
	activities,
	nodesByActivityID,
	disclosureKey,
	disclosureOverrides,
	onDisclosureChange,
}: {
	activities: ConversationActivity[];
	nodesByActivityID: ReadonlyMap<string, ActivityNode>;
	disclosureKey?: string;
	disclosureOverrides: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const [localOpen, setLocalOpen] = useState(false);
	const reducedMotion = useReducedMotion();
	const entering = useChatEntrance(`sub:${activities[0]?.id ?? ""}`, newestSequence(activities));
	const open = disclosureKey ? disclosureOverrides[disclosureKey] ?? false : localOpen;
	const updateOverride = (next: boolean) => {
		if (disclosureKey && onDisclosureChange) onDisclosureChange(disclosureKey, next);
		else setLocalOpen(next);
	};
	const hasNestedAgent = activities.some((activity) =>
		nodesByActivityID.get(activity.id)?.children.length,
	);
	const isStructural = activities.some((activity) => Boolean(activity.detail?.parentProviderItemId));
	if (activities.length === 1 || hasNestedAgent || isStructural) {
		return (
			<>
				{renderActivityRows(activities, nodesByActivityID)}
			</>
		);
	}
	const summary = summarizeSubgroup(activities);

	return (
		<div className="activity-subgroup">
			<Pressable
				onClick={() => updateOverride(!open)}
				aria-expanded={open}
				className={cn(ACTIVITY_SUMMARY_BUTTON_CLASS, "activity-subgroup-toggle")}
			>
				<span className="activity-summary-label text-xs text-muted-foreground">
					<ActivityTransition value={summary} inline enter={entering}>
						{summary}
					</ActivityTransition>
				</span>
				<ChevronRight aria-hidden="true" className={cn("size-3 shrink-0 transition-transform", open && "rotate-90")} />
			</Pressable>
			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.16, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden"
					>
						<div className="flex flex-col gap-1">
							{renderActivityRows(activities, nodesByActivityID)}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
			<FailedCalls activities={activities} folded={!open} />
		</div>
	);
}

function summarizeSubgroup(activities: ConversationActivity[]): string {
	const first = activities[0];
	if (!first) return "Related calls";
	const exploration = summarizeExploration(activities);
	if (exploration) return exploration;
	if (first.activityKind === "file_change") {
		const files = activities.reduce(
			(count, activity) => count + Math.max(1, fileChangeFiles(activity).length),
			0,
		);
		return `Changed ${files} ${files === 1 ? "file" : "files"}`;
	}
	if (first.activityKind === "mcp_tool") {
		const verb = activities.every(isExploratoryTool) ? "Explored" : "Used";
		return `${verb} ${activities.length} ${activities.length === 1 ? "tool call" : "tool calls"}`;
	}
	if (first.activityKind === "auto_review") {
		return `Checked ${activities.length} ${activities.length === 1 ? "decision" : "decisions"}`;
	}
	if (first.activityKind === "ao_action") return summarizeAOActions(activities);
	if (first.activityKind === "plan") return "Updated plan";
	const category = commandCategory(first.detail?.command ?? first.summary);
	const verb = category === "read" || category === "search" ? "Explored" : "Ran";
	return `${verb} ${activities.length} ${activities.length === 1 ? "tool call" : "tool calls"}`;
}

function summarizeExploration(activities: ConversationActivity[]): string | undefined {
	if (activities.length === 0) return undefined;
	const counts = new Map<string, number>();
	for (const activity of activities) {
		const step: CommandStep | undefined = activity.activityKind === "command"
			? activityCommandStep(activity)
			: activity.activityKind === "mcp_tool" ? activityToolStep(activity) : undefined;
		if (!step) return undefined;
		let kind: string;
		let count = 1;
		switch (step.done) {
			case "Read":
			case "Read file":
				kind = "read";
				break;
			case "Listed":
			case "Listed files":
				kind = "list";
				break;
			case "Searched for":
			case "Searched files":
				kind = step.target ? "search-pattern" : "search";
				break;
			case "Found files":
				kind = step.target ? "find-pattern" : "find";
				break;
			case "Searched the web":
			case "Searched the web for":
				kind = "web-search";
				break;
			case "Fetched":
				kind = "fetch";
				break;
			default: {
				const read = /^Read (\d+) files$/.exec(step.done);
				if (!read) return undefined;
				kind = "read";
				count = Number(read[1]);
			}
		}
		counts.set(kind, (counts.get(kind) ?? 0) + count);
	}
	return [...counts].map(([kind, count]) => {
		switch (kind) {
			case "read": return `Read ${count} ${count === 1 ? "file" : "files"}`;
			case "list": return count === 1 ? "Listed files" : `Listed files ${count} times`;
			case "search-pattern": return `Searched for ${count} ${count === 1 ? "pattern" : "patterns"}`;
			case "search": return count === 1 ? "Searched files" : `Searched files ${count} times`;
			case "find-pattern": return `Found files matching ${count} ${count === 1 ? "pattern" : "patterns"}`;
			case "find": return count === 1 ? "Found files" : `Found files ${count} times`;
			case "web-search": return count === 1 ? "Searched the web" : `Searched the web ${count} times`;
			case "fetch": return `Fetched ${count} ${count === 1 ? "page" : "pages"}`;
			default: return "";
		}
	}).join(" · ");
}

type ActivityNode = { activity: ConversationActivity; children: ActivityNode[] };

function ActivityTree({ node }: { node: ActivityNode }) {
	return (
		<div className="flex flex-col">
			<ActivityRow activity={node.activity} />
			{node.children.length > 0 ? <NestedAgentRun nodes={node.children} /> : null}
		</div>
	);
}

function NestedAgentRun({ nodes }: { nodes: ActivityNode[] }) {
	const [open, setOpen] = useState(false);
	const reducedMotion = useReducedMotion();
	const count = countNodes(nodes);
	const running = nodes.some(nodeRunning);
	return (
		<div className="mb-2 overflow-hidden bg-background/40">
			<Pressable
				onClick={() => setOpen(!open)}
				aria-label={`Subagent ${count} ${count === 1 ? "step" : "steps"}`}
				aria-expanded={open}
				className="activity-row-toggle flex min-h-8 w-full select-none items-center gap-2 text-xs text-muted-foreground hover:text-foreground"
			>
				<span className="font-medium text-foreground/80">Subagent</span>
				<span>{count} {count === 1 ? "step" : "steps"}</span>
				{running ? <Loader2 aria-hidden="true" className="size-3 animate-spin" /> : null}
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform", open && "rotate-90")} />
			</Pressable>
			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.16, ease: [0.22, 1, 0.36, 1] }}
						className="border-t border-border/70"
					>
					{nodes.map((node) => <ActivityTree key={node.activity.id} node={node} />)}
					</motion.div>
				) : null}
			</AnimatePresence>
			<FailedCalls activities={nodes.flatMap(nodeActivities)} folded={!open} />
		</div>
	);
}

function buildHierarchy(activities: ConversationActivity[]): ActivityNode[] {
	const byProvider = new Map<string, ActivityNode>();
	const nodes = activities.map((activity) => {
		const node = { activity, children: [] } satisfies ActivityNode;
		if (activity.providerItemId) byProvider.set(activity.providerItemId, node);
		return node;
	});
	const roots: ActivityNode[] = [];
	for (const node of nodes) {
		const parentId = node.activity.detail?.parentProviderItemId;
		const parent = parentId ? byProvider.get(parentId) : undefined;
		if (parent && !wouldCreateCycle(node, parent, byProvider)) parent.children.push(node);
		else roots.push(node);
	}
	return roots;
}

function wouldCreateCycle(
	node: ActivityNode,
	parent: ActivityNode,
	byProvider: Map<string, ActivityNode>,
): boolean {
	const visited = new Set<ActivityNode>([node]);
	let current: ActivityNode | undefined = parent;
	while (current) {
		if (visited.has(current)) return true;
		visited.add(current);
		const parentId: string | undefined = current.activity.detail?.parentProviderItemId;
		current = parentId ? byProvider.get(parentId) : undefined;
	}
	return false;
}

function countNodes(nodes: ActivityNode[]): number {
	return nodes.reduce((count, node) => count + 1 + countNodes(node.children), 0);
}

function nodeActivities(node: ActivityNode): ConversationActivity[] {
	return [node.activity, ...node.children.flatMap(nodeActivities)];
}

function nodeRunning(node: ActivityNode): boolean {
	return node.activity.status === "running" || node.children.some(nodeRunning);
}

function summarize(activities: ConversationActivity[]): string {
	let toolCalls = 0;
	let aoActions = 0;
	let changedFiles = 0;
	let exploratory = true;
	let hasPlan = false;

	for (const activity of activities) {
		if (activity.activityKind === "plan") {
			hasPlan = true;
			continue;
		}
		if (activity.activityKind === "ao_action") {
			aoActions += 1;
			exploratory = false;
			continue;
		}
		toolCalls += 1;
		const category = commandCategory(activity.detail?.command ?? activity.summary);
		if (activity.activityKind === "file_change") {
			changedFiles += Math.max(1, fileChangeFiles(activity).length);
			exploratory = false;
		} else if (
			!isExploratoryTool(activity) &&
			(activity.activityKind !== "command" || (category !== "read" && category !== "search"))
		) {
			exploratory = false;
		}
	}

	if (toolCalls === 0) {
		if (aoActions > 0) return summarizeAOActions(activities.filter((activity) => activity.activityKind === "ao_action"));
		return hasPlan ? "Updated plan" : `${activities.length} steps`;
	}
	if (aoActions === 0 && changedFiles === 0 && !hasPlan) {
		const exploration = summarizeExploration(activities);
		if (exploration) return exploration;
	}
	const label = `${exploratory ? "Explored" : "Ran"} ${toolCalls} ${toolCalls === 1 ? "tool call" : "tool calls"}`;
	const details = [
		changedFiles > 0
			? `${changedFiles} ${changedFiles === 1 ? "file changed" : "files changed"}`
			: undefined,
		aoActions > 0 ? summarizeAOActions(activities.filter((activity) => activity.activityKind === "ao_action")) : undefined,
		hasPlan ? "updated plan" : undefined,
	].filter(Boolean);
	return details.length ? `${label} · ${details.join(" · ")}` : label;
}
