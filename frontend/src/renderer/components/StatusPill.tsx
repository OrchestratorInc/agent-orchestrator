import { cn } from "@/lib/utils";

type StatusPillProps = {
	label: string;
	tone: string;
	breathe?: boolean;
	className?: string;
	leading?: "none" | "normal";
};

export function StatusPill({ label, tone, breathe, className, leading = "normal" }: StatusPillProps) {
	return (
		<span
			className={cn(
				"inline-flex shrink-0 items-center gap-1.75 whitespace-nowrap rounded-md px-2.75 py-1.25 text-sm-md font-semibold",
				leading === "none" && "leading-none",
				className,
			)}
			style={{
				color: tone,
				background: `color-mix(in srgb, ${tone} 7%, transparent)`,
				boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${tone} 25%, transparent)`,
			}}
		>
			<span
				className={cn("h-1.5 w-1.5 rounded-full", breathe && "animate-status-pulse")}
				style={{ background: tone }}
			/>
			{label}
		</span>
	);
}
