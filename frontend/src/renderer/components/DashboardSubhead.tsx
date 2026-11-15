import type { ReactNode } from "react";

export function DashboardSubhead({
	title,
	subtitle,
	count,
	actions,
}: {
	title: string;
	subtitle: string;
	count?: number;
	actions?: ReactNode;
}) {
	return (
		<div className="flex items-center gap-3 px-4.5 pt-5.5">
			<div className="flex min-w-0 items-baseline gap-3">
				<h1 className="text-heading font-bold tracking-tight-xl text-foreground">{title}</h1>
				{typeof count === "number" && <span className="font-mono text-control text-passive">{count}</span>}
				<span className="text-md-sm text-passive">{subtitle}</span>
			</div>
			{actions ? <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div> : null}
		</div>
	);
}
