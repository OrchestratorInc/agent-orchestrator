import { cn } from "../../lib/utils";

/** A hairline ring, about as heavy as the 1.25px icon strokes beside it, filling clockwise from the top. */
export function ProgressRing({ percent, className }: { percent?: number; className?: string }) {
	const radius = 6.75;
	const circumference = 2 * Math.PI * radius;
	return (
		<svg aria-hidden="true" className={cn("-rotate-90", className)} viewBox="0 0 16 16">
			<circle cx="8" cy="8" r={radius} fill="none" stroke="currentColor" strokeOpacity={0.25} strokeWidth={1.25} />
			<circle
				cx="8"
				cy="8"
				r={radius}
				fill="none"
				stroke="currentColor"
				className="transition-[stroke-dasharray] duration-normal ease-out motion-reduce:transition-none"
				strokeLinecap="round"
				strokeWidth={1.25}
				style={{ strokeDasharray: `${(circumference * (percent ?? 0)) / 100} ${circumference}` }}
			/>
		</svg>
	);
}
