import { cn } from "../../lib/utils";

const HEIGHTS = ["h-0.5", "h-1", "h-1.5", "h-2", "h-2.5", "h-3", "h-3.5", "h-4"];

/** Recent microphone levels as a small bar meter, newest on the right. */
export function DictationLevels({ levels, muted }: { levels: number[]; muted: boolean }) {
	return (
		<div aria-hidden="true" className="flex h-4 shrink-0 items-center gap-0.5">
			{levels.map((level, index) => (
				<span
					key={index}
					className={cn(
						"w-0.5 rounded-full transition-[height] duration-75",
						muted ? "bg-muted-foreground/40" : "bg-foreground/70",
						HEIGHTS[Math.round(level * (HEIGHTS.length - 1))],
					)}
				/>
			))}
		</div>
	);
}
