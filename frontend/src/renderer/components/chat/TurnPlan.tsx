/**
 * The agent's plan for a turn, as a live checklist.
 *
 * One panel per turn, not a row per revision. The provider re-sends the whole plan
 * every time it changes — a dozen times in a long turn — and the daemon overwrites
 * the turn's copy rather than appending, so this is current state. Rendering each
 * revision would read as though the agent had planned a dozen separate times.
 *
 * Position is load-bearing. It sits at the end of its turn, next to the changed
 * files, because both answer "where has this turn got to" — and because a panel that
 * grows at the end of a turn pushes nothing the reader is looking at. Ticking a step
 * changes no layout at all: the row keeps its height and only the mark and the tone
 * change, so a plan settling under a reader's eyes never moves the text they are on.
 *
 * A long plan scrolls inside a fixed window that follows the step being worked on,
 * fading at whichever edge has more to show. Once every step is done the list folds
 * to its header — the count already says how it went — and opens again if the agent
 * re-plans.
 */

import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Check, ListChecks, Loader2 } from "lucide-react";
import { cn } from "../../lib/utils";
import type { ConversationPlan, PlanStepStatus } from "../../types/conversation";

const STEP_LABEL: Record<PlanStepStatus, string> = {
	pending: "not started",
	in_progress: "in progress",
	completed: "done",
};

export const TurnPlan = memo(function TurnPlan({
	plan,
	live,
}: {
	plan: ConversationPlan;
	/** The turn is still running, so the plan can still change. */
	live?: boolean;
}) {
	const reducedMotion = useReducedMotion();
	const done = plan.steps.filter((step) => step.status === "completed").length;
	const allDone = plan.steps.length > 0 && done === plan.steps.length;
	const [expanded, setExpanded] = useState(() => !allDone);
	const wasAllDone = useRef(allDone);
	const viewportRef = useRef<HTMLDivElement>(null);
	const followed = useRef(false);
	const activeStep = activeStepIndex(plan);

	// Fold when the last step is ticked, open again if the agent re-plans. Only on
	// the change, so a reader who reopens a finished plan keeps it open.
	useEffect(() => {
		if (wasAllDone.current === allDone) return;
		wasAllDone.current = allDone;
		setExpanded(!allDone);
	}, [allDone]);

	const syncEdgeFade = useCallback(() => {
		const viewport = viewportRef.current;
		if (!viewport) return;
		viewport.toggleAttribute("data-fade-top", viewport.scrollTop > 1);
		viewport.toggleAttribute(
			"data-fade-bottom",
			viewport.scrollTop + viewport.clientHeight < viewport.scrollHeight - 1,
		);
	}, []);

	// Keep the step being worked on in view, centred so the finished step before it
	// stays visible as context. Scrolls only this window, never the transcript, and
	// lands instantly the first time so a plan scrolled back into view does not glide.
	useLayoutEffect(() => {
		const viewport = viewportRef.current;
		if (!expanded || !viewport) {
			followed.current = false;
			return;
		}
		const step = viewport.querySelector<HTMLElement>(`[data-plan-step="${activeStep}"]`);
		const overflow = viewport.scrollHeight - viewport.clientHeight;
		if (step && overflow > 0) {
			const top = Math.min(
				overflow,
				Math.max(0, step.offsetTop - (viewport.clientHeight - step.offsetHeight) / 2),
			);
			viewport.scrollTo({ top, behavior: followed.current && !reducedMotion ? "smooth" : "auto" });
		}
		followed.current = true;
		syncEdgeFade();
	}, [activeStep, expanded, plan.steps.length, reducedMotion, syncEdgeFade]);

	// Steps wrap differently as the column resizes, which changes what overflows.
	useEffect(() => {
		const viewport = viewportRef.current;
		if (!expanded || !viewport) return;
		const observer = new ResizeObserver(syncEdgeFade);
		observer.observe(viewport);
		if (viewport.firstElementChild) observer.observe(viewport.firstElementChild);
		return () => observer.disconnect();
	}, [expanded, syncEdgeFade]);

	if (plan.steps.length === 0) return null;

	return (
		<section
			aria-label="Agent plan"
			className="rounded-lg border border-border bg-surface"
			data-turn-plan="true"
		>
			<div className="flex items-center gap-2 px-3.5 py-2.5">
				<button
					type="button"
					onClick={() => setExpanded((open) => !open)}
					aria-expanded={expanded}
					aria-label={expanded ? "Collapse plan" : "Expand plan"}
					className="inline-flex size-control-sm shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					<ListChecks aria-hidden="true" className="size-4" />
				</button>
				<strong className="shrink-0 text-xs font-semibold text-foreground">Plan</strong>
				{live && done < plan.steps.length ? (
					<Loader2
						aria-label="still planning"
						className="size-3 shrink-0 animate-spin text-muted-foreground/60"
					/>
				) : null}
				<span className="flex-1" />
				{/* Tabular so the count does not jitter the header as it climbs. */}
				<span className="shrink-0 font-mono text-caption tabular-nums text-muted-foreground">
					{done}/{plan.steps.length}
				</span>
			</div>

			<AnimatePresence initial={false}>
				{expanded ? (
					<motion.div
						key="plan-body"
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] }}
						className="overflow-hidden"
					>
						{plan.explanation ? (
							<p className="px-3.5 pb-2 text-caption leading-relaxed text-muted-foreground">
								{plan.explanation}
							</p>
						) : null}

						{/* The rule sits outside the scroll window so the top fade never eats it. */}
						<div className="border-t border-border">
							<div
								ref={viewportRef}
								onScroll={syncEdgeFade}
								className="turn-plan-viewport scrollbar-none relative max-h-48 overflow-y-auto"
							>
								{/* A real list, so a screen reader hears "list, 5 items" and the status of each
								    step is spoken rather than left to a colour. */}
								<ol className="flex flex-col py-1">
									{plan.steps.map((step, index) => (
										<li
											key={`${index}-${step.text}`}
											data-plan-step={index}
											className="flex items-start gap-2.5 px-3.5 py-1"
											aria-label={`${step.text} — ${STEP_LABEL[step.status]}`}
										>
											<StepMark status={step.status} />
											<span
												className={cn(
													"min-w-0 flex-1 text-xs leading-[1.45]",
													step.status === "completed" && "text-muted-foreground/70 line-through",
													step.status === "in_progress" && "text-foreground",
													step.status === "pending" && "text-muted-foreground",
												)}
											>
												{step.text}
											</span>
										</li>
									))}
								</ol>
							</div>
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</section>
	);
});

/** The step the window follows: the one in progress, else the next to start, else the last. */
function activeStepIndex(plan: ConversationPlan): number {
	const inProgress = plan.steps.findIndex((step) => step.status === "in_progress");
	if (inProgress >= 0) return inProgress;
	const next = plan.steps.findIndex((step) => step.status === "pending");
	return next >= 0 ? next : plan.steps.length - 1;
}

/**
 * The mark for one step, in a fixed-size slot.
 *
 * Same box whatever the state, because these change while the reader is looking at
 * them: a spinner that is a pixel taller than a tick would nudge every line below it
 * each time a step finished.
 */
function StepMark({ status }: { status: PlanStepStatus }) {
	return (
		<span
			aria-hidden="true"
			className="mt-[3px] flex size-3 shrink-0 items-center justify-center"
		>
			{status === "completed" ? (
				<Check className="size-3 text-success" />
			) : status === "in_progress" ? (
				<Loader2 className="size-3 animate-spin text-status-working" />
			) : (
				<span className="size-[7px] rounded-full border border-muted-foreground/50" />
			)}
		</span>
	);
}
