import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronDown, ListChecks } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { Pressable } from "../ui/pressable";
import type { ConversationPlan, PlanStepStatus } from "../../types/conversation";
import { syncEdgeFade as syncViewportEdgeFade } from "../../hooks/useEdgeFade";

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

	useEffect(() => {
		if (wasAllDone.current === allDone) return;
		wasAllDone.current = allDone;
		setExpanded(!allDone);
	}, [allDone]);

	const syncEdgeFade = useCallback(() => {
		if (viewportRef.current) syncViewportEdgeFade(viewportRef.current);
	}, []);

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
	const currentStep = !expanded && !allDone ? plan.steps[activeStep]?.text : undefined;

	return (
		<section
			aria-label="Agent plan"
			aria-busy={Boolean(live) && !allDone}
			className="overflow-hidden rounded-xl border border-border"
			data-turn-plan="true"
		>
			<Pressable
				onClick={() => setExpanded((open) => !open)}
				aria-expanded={expanded}
				aria-label={expanded ? "Collapse plan" : "Expand plan"}
				className="group/plan flex h-11 w-full items-center gap-2.5 px-3.5"
			>
				<PlanMark done={allDone} />
				<span className="shrink-0 text-sm font-medium text-foreground">Plan</span>
				<span className="min-w-0 flex-1 truncate text-sm text-muted-foreground" title={currentStep}>
					{currentStep}
				</span>
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
			</Pressable>

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
							className="edge-fade scrollbar-none relative max-h-60 overflow-y-auto"
						>
							<ol className="flex flex-col px-3.5 pb-2.5">
								{plan.steps.map((step, index) => (
									<li
										key={`${index}-${step.text}`}
										data-plan-step={index}
										className="flex items-start gap-2.5 py-1"
										aria-label={`${step.text} — ${STEP_LABEL[step.status]}`}
									>
										<StepMark status={step.status} live={Boolean(live)} />
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

function activeStepIndex(plan: ConversationPlan): number {
	const inProgress = plan.steps.findIndex((step) => step.status === "in_progress");
	if (inProgress >= 0) return inProgress;
	const next = plan.steps.findIndex((step) => step.status === "pending");
	return next >= 0 ? next : plan.steps.length - 1;
}

function StepMark({ status, live }: { status: PlanStepStatus; live: boolean }) {
	const shown = (mark: PlanStepStatus) =>
		cn("transition-opacity duration-200 motion-reduce:transition-none", status === mark ? "opacity-100" : "opacity-0");
	return (
		<span aria-hidden="true" className="flex h-6 w-4 shrink-0 items-center justify-center">
			<svg viewBox="0 0 16 16" className="size-4 overflow-visible">
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
						className={cn(
							"origin-center animate-spin transition-opacity duration-200 [transform-box:fill-box] motion-reduce:animate-none motion-reduce:transition-none",
							!live && "opacity-0",
						)}
					/>
				</g>
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
