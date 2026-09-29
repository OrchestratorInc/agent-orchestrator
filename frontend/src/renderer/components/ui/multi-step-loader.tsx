import { motion, useReducedMotion } from "motion/react";
import { useEffect, useState, type CSSProperties } from "react";
import { cn } from "../../lib/utils";

function elapsedTime(since: string | undefined, now: number): string {
	const started = since ? Date.parse(since) : now;
	const seconds = Math.max(0, Math.floor((now - (Number.isFinite(started) ? started : now)) / 1_000));
	return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export function MultiStepLoader({
	ariaLabel,
	className,
	activeIndex,
	activeSince,
	duration = 380,
	steps,
}: {
	ariaLabel: string;
	className?: string;
	activeIndex: number;
	activeSince?: string;
	duration?: number;
	steps: readonly string[];
}) {
	const [now, setNow] = useState(() => Date.now());
	const reduceMotion = useReducedMotion();
	useEffect(() => {
		setNow(Date.now());
		const interval = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(interval);
	}, [activeIndex, activeSince]);
	if (steps.length === 0) return null;

	return (
		<p aria-label={ariaLabel} className={cn("inline-flex min-h-7 items-center justify-center gap-3", className)} role="status">
			<span
				className="inline-flex items-center gap-3"
				data-testid="multi-step-loader-step"
			>
				<span className="relative grid size-5 shrink-0 place-items-center" aria-hidden="true">
					<span className="multi-step-loader__dot size-2 rounded-full bg-[#60a5fa]" />
				</span>
				<motion.span
					animate={reduceMotion ? undefined : { backgroundPosition: ["180% center", "-80% center"] }}
					className="multi-step-loader__step bg-[linear-gradient(100deg,var(--color-text-muted)_15%,#93c5fd_48%,var(--color-text-muted)_82%)] bg-[length:220%_100%] bg-clip-text text-sm font-medium leading-5 text-transparent"
					key={activeIndex}
					style={{ "--multi-step-loader-duration": `${duration}ms` } as CSSProperties}
					transition={{ duration: 2.8, ease: "linear", repeat: Infinity }}
				>
					{steps[activeIndex]}
				</motion.span>
			</span>
			<time aria-hidden="true" className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground/65" data-testid="multi-step-loader-timer">
				{elapsedTime(activeSince, now)}
			</time>
		</p>
	);
}
