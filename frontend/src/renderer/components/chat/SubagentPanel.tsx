/**
 * The inspector's Subagents tab: every subagent this chat launched, and one child's
 * conversation at a time.
 *
 * The team card in the chat says who is working; this is where the reader follows one
 * of them closely while the parent chat stays in view. It is read-only on purpose: a
 * subagent takes its instructions from the parent agent, not from the reader.
 */

import { useMemo, useState } from "react";
import { InspectorSection } from "@aoagents/product-ui";
import { ArrowLeft, ArrowUp, ChevronDown, ChevronRight, Plus } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import {
	elapsedLabel,
	StateGlyph,
	SubagentAvatar,
	SubagentTypeLabel,
	useNow,
} from "./SubagentTeam";
import { SubagentTranscript } from "./SubagentTranscript";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { readSubagent, type Subagent, type SubagentRef } from "./subagent-team";
import type { ChatModel, TurnSettings } from "../../types/conversation";

const NO_MODELS: ChatModel[] = [];
/** A section lists this many children, newest first; the rest fold behind Show more. */
const ROWS_COLLAPSED = 10;
const NO_SETTINGS: TurnSettings = {};
const IGNORE_SETTINGS = () => {};

/**
 * What a chat host gives the timeline so it can show subagents in the inspector.
 * `container` is the tab's body while the tab is showing, `reveal` brings the tab up,
 * and `onAvailableChange` says whether the chat has any subagents to list at all.
 */
export type SubagentPanelHost = {
	container: HTMLElement | null;
	reveal: () => void;
	onAvailableChange: (available: boolean) => void;
	/** The chat's model catalog, so the read-only composer draws the side chat's model control. */
	models?: ChatModel[];
};

export function SubagentPanel({
	subagents,
	openId,
	onOpen,
	onBack,
	harness,
	models = NO_MODELS,
	disclosureOverrides,
	onDisclosureChange,
}: {
	subagents: readonly SubagentRef[];
	/** The child whose conversation is showing; the list shows when unset or gone. */
	openId?: string;
	onOpen: (launchId: string) => void;
	onBack: () => void;
	harness?: string;
	/** For the read-only composer's model control, as the side chat draws it. Empty hides it. */
	models?: ChatModel[];
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
					key={open.launch.id}
					agent={open}
					harness={harness}
					models={models}
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

	// The inspector's own body and sections, as Summary and Artifacts draw theirs.
	return (
		<div className="board-scrollbar flex min-h-0 flex-1 flex-col overflow-y-auto overflow-x-hidden pt-0.5 pb-3">
			{agents.length === 0 ? (
				<p className="px-3 pt-2 text-xs text-muted-foreground">{t("chat.subagent.panel.empty")}</p>
			) : null}
			<SubagentGroup label={t("chat.subagent.state.working")} agents={working} harness={harness} now={now} onOpen={onOpen} />
			<SubagentGroup label={t("chat.subagent.panel.finished")} agents={finished} harness={harness} now={now} onOpen={onOpen} />
		</div>
	);
}

/** One section: a Summary-style header, then a row per child, the newest ten until asked. */
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
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	if (agents.length === 0) return null;
	const folding = agents.length > ROWS_COLLAPSED;
	const rows = expanded ? agents : agents.slice(0, ROWS_COLLAPSED);
	// Folded, the last two rows fade toward the control, so the list visibly runs on.
	const fade = (index: number) =>
		folding && !expanded ? (index === rows.length - 1 ? "opacity-25" : index === rows.length - 2 ? "opacity-60" : undefined) : undefined;
	return (
		<div role="group" aria-label={label}>
			<InspectorSection surface={false} title={`${label} · ${agents.length}`}>
				<ul className="flex min-w-0 flex-col">
					{rows.map((agent, index) => (
						<li key={agent.launch.id} className={fade(index)}>
							<SubagentListRow agent={agent} harness={harness} now={now} onClick={() => onOpen(agent.launch.id)} />
						</li>
					))}
					{folding ? (
						<li>
							<Pressable
								aria-expanded={expanded}
								className="flex min-h-8 w-full min-w-0 items-center justify-center gap-1.5 rounded-md px-1 text-sm text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:bg-interactive-hover"
								onClick={() => setExpanded((current) => !current)}
							>
								<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0", expanded && "rotate-180")} />
								{expanded
									? t("shell.showLessAgents")
									: t("shell.showMoreAgents", { count: agents.length - ROWS_COLLAPSED })}
							</Pressable>
						</li>
					) : null}
				</ul>
			</InspectorSection>
		</div>
	);
}

/**
 * One child as a line, like Summary's artifact rows: its mark, name and type, then how it
 * stands and how long it took. What it is doing or found shows once it is opened.
 */
function SubagentListRow({
	agent,
	harness,
	now,
	onClick,
}: {
	agent: Subagent;
	harness?: string;
	now: number;
	onClick: () => void;
}) {
	const elapsed = elapsedLabel(agent, now);
	return (
		<Pressable
			className="group/agent flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1 text-left text-sm transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover"
			onClick={onClick}
		>
			<SubagentAvatar harness={harness} state={agent.state} centered />
			<span className="min-w-0 truncate text-foreground/90">{agent.title}</span>
			<SubagentTypeLabel agent={agent} />
			<span className="flex flex-1 items-center justify-end gap-2">
				<StateGlyph state={agent.state} />
				{elapsed ? <span className="shrink-0 text-caption tabular-nums text-muted-foreground/70">{elapsed}</span> : null}
				<ChevronRight
					aria-hidden="true"
					className="size-3 shrink-0 text-muted-foreground/40 group-hover/agent:text-muted-foreground"
				/>
			</span>
		</Pressable>
	);
}

/** One child's conversation under a header that leads back to the list. */
function SubagentConversation({
	agent,
	harness,
	models,
	now,
	onBack,
	disclosureOverrides,
	onDisclosureChange,
}: {
	agent: Subagent;
	harness?: string;
	models: ChatModel[];
	now: number;
	onBack: () => void;
	disclosureOverrides?: Readonly<Record<string, boolean>>;
	onDisclosureChange?: (key: string, open: boolean) => void;
}) {
	const { t } = useTranslation();
	const elapsed = elapsedLabel(agent, now);
	const backLabel = t("chat.subagent.panel.back");
	// The control reads "Choose a model", never the catalog's default: which model the
	// child ran is not recorded, and naming the parent's would claim it was that one.
	const composerModels = useMemo(
		() => (models.some((model) => model.default) ? models.map((model) => ({ ...model, default: false })) : models),
		[models],
	);

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
					<h3 className="min-w-0 truncate text-xs font-medium text-foreground">
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
			{/* Where a chat has its composer, this one shows the side chat's composer switched
			    off: why it takes no message, in place of "Message the agent…", over the same
			    tools, each disabled. */}
			<div className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
				<div
					data-testid="subagent-read-only-composer"
					className="cursor-chat-composer mx-auto flex w-full max-w-3xl cursor-default flex-col gap-1.5 px-3 pt-3 pb-3"
				>
					<p className="min-h-[4.5rem] py-1 pl-[7px] text-base! leading-relaxed text-muted-foreground">
						{t("chat.subagent.readOnly")}
					</p>
					<div className="flex h-7 items-center gap-1.5">
						<div className="flex min-w-0 flex-1 items-center gap-0.5">
							<Button
								type="button"
								variant="quiet"
								size="icon-sm"
								shape="pill"
								disabled
								aria-label={t("sideChat.attachAFile")}
								className="shrink-0"
							>
								<Plus aria-hidden="true" className="size-3.5" />
							</Button>
							<TurnSettingsBar
								models={composerModels}
								settings={NO_SETTINGS}
								onChange={IGNORE_SETTINGS}
								showApprovalMode={false}
								disabled
							/>
						</div>
						<Button type="button" variant="primary" size="icon-sm" shape="pill" disabled aria-label={t("sideChat.sendMessage")}>
							<ArrowUp aria-hidden="true" className="size-3.5" />
						</Button>
					</div>
				</div>
			</div>
		</>
	);
}
