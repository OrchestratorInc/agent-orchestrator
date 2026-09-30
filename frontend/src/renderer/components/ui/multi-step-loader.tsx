import type { CSSProperties } from "react";
import { Check } from "lucide-react";
import { cn } from "../../lib/utils";

export function MultiStepLoader({
	ariaLabel,
	className,
	activeIndex,
	duration = 380,
	percent,
	steps,
}: {
	ariaLabel: string;
	className?: string;
	activeIndex: number;
	duration?: number;
	percent?: number;
	steps: readonly string[];
}) {
	if (steps.length === 0) return null;
	const completed = percent ?? (steps.length === 1 ? 100 : Math.round(activeIndex / (steps.length - 1) * 100));

	return (
		<div aria-label={ariaLabel} className={cn("flex w-80 max-w-[calc(100%-2rem)] flex-col gap-5", className)} role="status">
			<ol className="flex flex-col gap-3">
				{steps.map((step, index) => (
					<li aria-current={index === activeIndex ? "step" : undefined} className="flex min-h-7 items-center gap-3 text-sm leading-5" key={step}>
						<span aria-hidden="true" className="grid size-5 shrink-0 place-items-center">
							{index < activeIndex || (index === activeIndex && completed === 100) ? (
								<Check className="size-4 text-[#60a5fa]" data-testid="multi-step-loader-check" strokeWidth={2} />
							) : (
								<span className={cn("size-2 rounded-full", index === activeIndex ? "multi-step-loader__dot bg-[#60a5fa]" : "bg-muted-foreground/40")} data-testid={index === activeIndex ? "multi-step-loader-active-dot" : undefined} />
							)}
						</span>
						{index === activeIndex && completed !== 100 ? (
							<span data-testid="multi-step-loader-step">
								<span
									className="multi-step-loader__step multi-step-loader__shimmer font-medium"
									key={activeIndex}
									style={{ "--multi-step-loader-duration": `${duration}ms` } as CSSProperties}
								>
									{step}
								</span>
							</span>
						) : <span className={index < activeIndex ? "text-foreground/80" : "text-muted-foreground/55"}>{step}</span>}
					</li>
				))}
			</ol>
			<div className="flex w-full items-center gap-3">
				<div aria-label={ariaLabel} aria-valuemax={100} aria-valuemin={0} aria-valuenow={completed} className="relative h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[#60a5fa]/10" role="progressbar">
					<span className="absolute inset-y-0 left-0 rounded-full bg-[#60a5fa]/55 transition-[width] duration-500 ease-out" data-testid="multi-step-loader-completed" style={{ width: `${completed}%` }} />
				</div>
				<span aria-hidden="true" className="w-8 shrink-0 text-right font-mono text-xs tabular-nums text-muted-foreground/65" data-testid="multi-step-loader-percent">{Math.round(completed)}%</span>
			</div>
		</div>
	);
}
