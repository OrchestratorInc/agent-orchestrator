/**
 * The inspector's Subagents tab: every subagent this chat launched, and one child's
 * conversation at a time.
 *
 * The team card in the chat says who is working; this is where the reader follows one
 * of them closely while the parent chat stays in view. It is read-only on purpose: a
 * subagent takes its instructions from the parent agent, not from the reader.
 */

import { useMemo } from "react";
import { ArrowLeft, Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import {
	elapsedLabel,
	StateGlyph,
	SubagentAvatar,
	SubagentRowButton,
	SubagentTypeLabel,
	useNow,
} from "./SubagentTeam";
import { SubagentTranscript } from "./SubagentTranscript";
import { readSubagent, type Subagent, type SubagentRef } from "./subagent-team";

/**
 * What a chat host gives the timeline so it can show subagents in the inspector.
 * `container` is the tab's body while the tab is showing, `reveal` brings the tab up,
 * and `onAvailableChange` says whether the chat has any subagents to list at all.
 */
export type SubagentPanelHost = {
	container: HTMLElement | null;
	reveal: () => void;
	onAvailableChange: (available: boolean) => void;
};

export function SubagentPanel({
	subagents,
	openId,
	onOpen,
	onBack,
	harness,
	disclosureOverrides,
	onDisclosureChange,
}: {
	subagents: readonly SubagentRef[];
	/** The child whose conversation is showing; the list shows when unset or gone. */
	openId?: string;
	onOpen: (launchId: string) => void;
	onBack: () => void;
	harness?: string;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const agents = useMemo(
		() => subagents.map((ref) => readSubagent(ref.launch, ref.steps, ref.live)),
		[subagents],
	);
	const now = useNow(agents.some((agent) => agent.state === "working"));
	const open = openId ? agents.find((agent) => agent.launch.id === openId) : undefined;

	return (
		<div
			data-testid="subagent-panel"
			className="cursor-chat-surface flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-background [font-size:14px]"
		>
			{open ? (
				<SubagentConversation
					agent={open}
					harness={harness}
					now={now}
					onBack={onBack}
					disclosureOverrides={disclosureOverrides}
					onDisclosureChange={onDisclosureChange}
				/>
			) : (
				<SubagentList agents={agents} harness={harness} now={now} onOpen={onOpen} />
			)}
		</div>
	);
}

/** Working children first, then the finished ones; the newest at the top of each. */
function SubagentList({
	agents,
	harness,
	now,
	onOpen,
}: {
	agents: readonly Subagent[];
	harness?: string;
	now: number;
	onOpen: (launchId: string) => void;
}) {
	const { t } = useTranslation();
	const newestFirst = [...agents].reverse();
	const working = newestFirst.filter((agent) => agent.state === "working");
	const finished = newestFirst.filter((agent) => agent.state !== "working");

	return (
		<div className="cursor-chat-timeline scrollbar-none flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overflow-x-hidden px-3 py-3">
			{agents.length === 0 ? (
				<p className="px-1 text-xs text-muted-foreground">{t("chat.subagent.panel.empty")}</p>
			) : null}
			<SubagentGroup label={t("chat.subagent.state.working")} agents={working} harness={harness} now={now} onOpen={onOpen} />
			<SubagentGroup label={t("chat.subagent.panel.finished")} agents={finished} harness={harness} now={now} onOpen={onOpen} />
		</div>
	);
}

function SubagentGroup({
	label,
	agents,
	harness,
	now,
	onOpen,
}: {
	label: string;
	agents: readonly Subagent[];
	harness?: string;
	now: number;
	onOpen: (launchId: string) => void;
}) {
	if (agents.length === 0) return null;
	return (
		<section aria-label={label} className="flex min-w-0 flex-col gap-1.5">
			<h3 className="px-1 text-caption text-muted-foreground">
				{label}
				<span className="text-muted-foreground/60"> · </span>
				<span className="tabular-nums">{agents.length}</span>
			</h3>
			<ul className="cursor-chat-explore-box flex min-w-0 flex-col overflow-hidden rounded-lg border">
				{agents.map((agent) => (
					<li key={agent.launch.id} className="min-w-0 border-t border-border/60 first:border-t-0">
						<SubagentRowButton agent={agent} harness={harness} now={now} onClick={() => onOpen(agent.launch.id)} />
					</li>
				))}
			</ul>
		</section>
	);
}

/** One child's conversation under a header that leads back to the list. */
function SubagentConversation({
	agent,
	harness,
	now,
	onBack,
	disclosureOverrides,
	onDisclosureChange,
}: {
	agent: Subagent;
	harness?: string;
	now: number;
	onBack: () => void;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const { t } = useTranslation();
	const elapsed = elapsedLabel(agent, now);
	const backLabel = t("chat.subagent.panel.back");

	return (
		<>
			<div className="flex min-w-0 shrink-0 items-center gap-2 border-b border-border py-1.5 pl-1.5 pr-3">
				<Tooltip>
					<TooltipTrigger asChild>
						<Button type="button" variant="ghost" size="icon-sm" aria-label={backLabel} onClick={onBack}>
							<ArrowLeft aria-hidden="true" className="size-3.5" />
						</Button>
					</TooltipTrigger>
					<TooltipContent>{backLabel}</TooltipContent>
				</Tooltip>
				<SubagentAvatar harness={harness} state={agent.state} />
				<span className="flex min-w-0 flex-1 items-baseline gap-2">
					<h3 className="min-w-0 truncate text-xs font-medium text-foreground" title={agent.title}>
						{agent.title}
					</h3>
					<SubagentTypeLabel agent={agent} />
				</span>
				<StateGlyph state={agent.state} />
				{elapsed ? <span className="text-caption tabular-nums text-muted-foreground/70">{elapsed}</span> : null}
			</div>
			{/* Scrolls like the chat beside it: no painted track, and long words wrap instead of widening it. */}
			<div className="cursor-chat-timeline scrollbar-none min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-4 py-4 wrap-break-word">
				<SubagentTranscript
					agent={agent}
					disclosureKey={`subagent-panel:${agent.launch.id}`}
					disclosureOverrides={disclosureOverrides}
					onDisclosureChange={onDisclosureChange}
				/>
			</div>
			<p className="flex shrink-0 items-center gap-1.5 border-t border-border px-4 py-2 text-caption text-muted-foreground">
				<Lock aria-hidden="true" className="size-3 shrink-0" />
				{t("chat.subagent.readOnly")}
			</p>
		</>
	);
}
