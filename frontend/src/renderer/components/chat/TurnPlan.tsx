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
import { ChevronDown, ListChecks } from "lucide-react";
import { cn } from "../../lib/utils";
import type { ConversationPlan, PlanStepStatus } from "../../types/conversation";
import { syncEdgeFade as syncViewportEdgeFade } from "./edge-fade";

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
		if (viewportRef.current) syncViewportEdgeFade(viewportRef.current);
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
	const total = plan.steps.length;
	// Folded, the header still says where the agent is.
	const currentStep = !expanded && !allDone ? plan.steps[activeStep]?.text : undefined;

	return (
		<section
			aria-label="Agent plan"
			// Busy while the agent can still tick steps off, so assistive tech knows the
			// list is changing rather than final.
			aria-busy={Boolean(live) && !allDone}
			className="overflow-hidden rounded-xl border border-border"
			data-turn-plan="true"
		>
			{/* The whole header is the toggle, so the target is the row rather than an icon. */}
			<button
				type="button"
				onClick={() => setExpanded((open) => !open)}
				aria-expanded={expanded}
				aria-label={expanded ? "Collapse plan" : "Expand plan"}
				className="group/plan flex h-11 w-full items-center gap-2.5 px-3.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
			>
				<PlanMark done={allDone} />
				<span className="shrink-0 text-sm font-medium text-foreground">Plan</span>
				<span className="min-w-0 flex-1 truncate text-sm text-muted-foreground" title={currentStep}>
					{currentStep}
				</span>
				{/* Tabular so the count does not jitter the header as it climbs. The tick
				    already says the plan is done, so the count stays neutral. */}
				<span className="shrink-0 text-xs font-medium tabular-nums text-muted-foreground">
					{done}/{total}
				</span>
				<ChevronDown
					aria-hidden="true"
					className={cn(
						"size-4 shrink-0 text-muted-foreground/50 transition-[transform,color] duration-200 group-hover/plan:text-foreground motion-reduce:transition-none",
						expanded && "rotate-180",
					)}
				/>
			</button>

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
							<p className="px-3.5 pb-1.5 text-xs leading-relaxed text-muted-foreground">
								{plan.explanation}
							</p>
						) : null}

						<div
							ref={viewportRef}
							onScroll={syncEdgeFade}
							className="edge-fade-viewport scrollbar-none relative max-h-60 overflow-y-auto"
						>
							{/* A real list, so a screen reader hears "list, 5 items" and the status of each
							    step is spoken rather than left to a colour. */}
							<ol className="flex flex-col px-3.5 pb-2.5">
								{plan.steps.map((step, index) => (
									<li
										key={`${index}-${step.text}`}
										data-plan-step={index}
										className="flex items-start gap-2.5 py-1"
										aria-label={`${step.text} — ${STEP_LABEL[step.status]}`}
									>
										<StepMark status={step.status} />
										{/* The strike is always there and only its colour changes, so a step
										    being ticked off fades its line in, even across a wrapped line. */}
										<span
											className={cn(
												"min-w-0 flex-1 text-sm leading-6 line-through transition-[color,text-decoration-color] duration-300 motion-reduce:transition-none",
												step.status === "completed"
													? "text-muted-foreground/60 decoration-muted-foreground/50"
													: "decoration-transparent",
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
					</motion.div>
				) : null}
			</AnimatePresence>
		</section>
	);
});

/** The header's mark: the list icon while there is work left, a filled tick once it is all done. */
function PlanMark({ done }: { done: boolean }) {
	return (
		<span aria-hidden="true" className="relative grid size-[18px] shrink-0 place-items-center">
			<ListChecks
				className={cn(
					"absolute size-4 text-muted-foreground transition-[opacity,scale] duration-200 motion-reduce:transition-none",
					done ? "scale-75 opacity-0" : "scale-100 opacity-100",
				)}
			/>
			<svg
				viewBox="0 0 18 18"
				className={cn(
					// A finished plan is completed work, not an actionable success, so the
					// tick takes the success hue at reduced strength rather than full ready green.
					"absolute size-[18px] text-success/70 transition-[opacity,scale] duration-200 motion-reduce:transition-none",
					done ? "scale-100 opacity-100" : "scale-75 opacity-0",
				)}
			>
				<circle cx="9" cy="9" r="9" fill="currentColor" />
				<path
					d="M5.6 9.3 7.9 11.6 12.5 6.8"
					fill="none"
					stroke="currentColor"
					strokeWidth="2"
					strokeLinecap="round"
					strokeLinejoin="round"
					className="text-background"
				/>
			</svg>
		</span>
	);
}

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
 * each time a step finished. All three marks are drawn and only the current one is
 * shown, so a step that ticks over cross-fades in place instead of popping.
 */
function StepMark({ status }: { status: PlanStepStatus }) {
	const shown = (mark: PlanStepStatus) =>
		cn("transition-opacity duration-200 motion-reduce:transition-none", status === mark ? "opacity-100" : "opacity-0");
	return (
		<span aria-hidden="true" className="flex h-6 w-4 shrink-0 items-center justify-center">
			<svg viewBox="0 0 16 16" className="size-4 overflow-visible">
				{/* Not started: a dashed ring, present but quiet. */}
				<circle
					cx="8"
					cy="8"
					r="6.75"
					fill="none"
					stroke="currentColor"
					strokeWidth="1.25"
					strokeDasharray="2.6 2.7"
					className={cn("text-muted-foreground/45", shown("pending"))}
				/>
				{/* In progress: a faint ring with an arc turning round it, neutral like the
				    chat's own Working spinner. */}
				<g className={cn("text-foreground", shown("in_progress"))}>
					<circle cx="8" cy="8" r="6.75" fill="none" stroke="currentColor" strokeOpacity="0.18" strokeWidth="1.5" />
					<circle
						cx="8"
						cy="8"
						r="6.75"
						fill="none"
						stroke="currentColor"
						strokeWidth="1.5"
						strokeLinecap="round"
						strokeDasharray="12 43"
						className="origin-center animate-spin [transform-box:fill-box] motion-reduce:animate-none"
					/>
				</g>
				{/* Done: an outlined circle whose tick draws itself in as the step finishes. */}
				<g className={cn("text-muted-foreground/70", shown("completed"))}>
					<circle cx="8" cy="8" r="6.75" fill="none" stroke="currentColor" strokeWidth="1.25" />
					<path
						d="M5.3 8.2 7.2 10.1 10.8 6.3"
						pathLength={1}
						fill="none"
						stroke="currentColor"
						strokeWidth="1.5"
						strokeLinecap="round"
						strokeLinejoin="round"
						strokeDasharray={1}
						strokeDashoffset={status === "completed" ? 0 : 1}
						className="transition-[stroke-dashoffset] delay-75 duration-300 ease-out motion-reduce:transition-none"
					/>
				</g>
			</svg>
		</span>
	);
}
