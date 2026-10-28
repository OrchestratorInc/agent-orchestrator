/**
 * One subagent's conversation, read-only: the prompt its parent sent, what it said
 * and ran in order, and the report it handed back.
 *
 * It reads like the main chat because it is drawn with the main chat's parts: prose,
 * the same tool-call runs, and the live row at the end. The reader can watch and open
 * anything here but never send to the child: it answers to its parent, not to them.
 */

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { ActivityRun } from "./ActivityRun";
import { ChatMarkdown } from "./ChatMarkdown";
import { LiveResponseStatus, liveTaskLabel } from "./ChatTimelineItems";
import { isChildMessage, stepSections, type Subagent } from "./subagent-team";

/** A prompt longer than this starts folded, like a long message from another agent. */
const PROMPT_FOLD_CHARS = 360;
const PROMPT_FOLD_LINES = 6;

export function SubagentTranscript({
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
	const sections = useMemo(() => stepSections(agent.steps), [agent.steps]);
	const working = agent.state === "working";
	const lastRunIndex = sections.findLastIndex((section) => section.kind === "run");
	const empty = !working && sections.length === 0 && !agent.report && !agent.error;
	// The live row names the step the child is on, as a turn's does; what it writes is prose, not a step.
	const currentStep = working
		? agent.steps.findLast((step) => (step.status === "running" || step.status === "pending") && !isChildMessage(step))
		: undefined;

	return (
		<div className="flex min-w-0 flex-col gap-3">
			{agent.prompt ? <SubagentPrompt prompt={agent.prompt} /> : null}
			{sections.map((section, index) =>
				section.kind === "said" ? (
					<ChatMarkdown key={section.key} text={section.text} />
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
			{/* At the live edge, after the work, the way a turn's own live row follows its work down. */}
			{working ? (
				<LiveResponseStatus startedAt={agent.launch.createdAt} task={currentStep ? liveTaskLabel(currentStep) : undefined} />
			) : null}
			{empty ? <p className="text-caption text-muted-foreground/70">{t("chat.subagent.empty")}</p> : null}
			{agent.error ? (
				<p className="whitespace-pre-wrap text-sm leading-relaxed text-destructive">{agent.error}</p>
			) : null}
			{agent.report ? (
				<div className="flex min-w-0 flex-col gap-1 border-t border-border/60 pt-3">
					<span className="text-caption text-muted-foreground/70">{t("chat.subagent.report")}</span>
					<ChatMarkdown text={agent.report} />
				</div>
			) : null}
		</div>
	);
}

/** The parent's instructions, drawn as the message that opened this conversation. */
function SubagentPrompt({ prompt }: { prompt: string }) {
	const { t } = useTranslation();
	const foldable = prompt.length > PROMPT_FOLD_CHARS || prompt.split("\n").length > PROMPT_FOLD_LINES;
	const [expanded, setExpanded] = useState(false);
	const folded = foldable && !expanded;
	return (
		<div className="flex min-w-0 flex-col items-end gap-1">
			<span className="text-caption text-muted-foreground/70">{t("chat.subagent.sentBy")}</span>
			{/* `wrap-anywhere`, not `break-words`: a long path must shrink the fit-content width too, or it spills out. */}
			<div className="cursor-chat-human-message flex w-fit min-w-0 max-w-[min(92%,560px)] flex-col gap-1 rounded-[10px] border px-3 py-2.5 wrap-anywhere">
				<div
					data-testid="subagent-prompt"
					className={cn(folded && "max-h-36 overflow-hidden [mask-image:linear-gradient(to_bottom,black_70%,transparent)]")}
				>
					<ChatMarkdown text={prompt} />
				</div>
				{foldable ? (
					<button
						type="button"
						aria-expanded={expanded}
						onClick={() => setExpanded((current) => !current)}
						className="self-end text-caption text-muted-foreground hover:text-foreground focus-visible:text-foreground"
					>
						{t(expanded ? "chat.subagent.showLessPrompt" : "chat.subagent.showFullPrompt")}
					</button>
				) : null}
			</div>
		</div>
	);
}
