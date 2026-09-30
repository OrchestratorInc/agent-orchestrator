import { ChevronDown } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";
import { AgentAvatar } from "../AgentAvatar";

/**
 * Shared settings container for one agent provider. Provider identity and
 * provider-level actions live in the header; account rows stay below.
 */
export function AgentProviderGroup({
	provider,
	name,
	summary,
	action,
	expanded,
	onExpandedChange,
	collapsible = true,
	collapseLocked = false,
	children,
}: {
	provider: string;
	name: string;
	summary?: string;
	action?: ReactNode;
	expanded: boolean;
	onExpandedChange: (expanded: boolean) => void;
	collapsible?: boolean;
	collapseLocked?: boolean;
	children: ReactNode;
}) {
	const headingId = useId();
	const contentId = useId();
	const [animationsReady, setAnimationsReady] = useState(false);
	useEffect(() => setAnimationsReady(true), []);
	const identity = <>
		<AgentAvatar className="size-8 shrink-0" decorative provider={provider} />
		<div className="min-w-0">
			<span id={headingId} className="block truncate text-sm font-medium text-foreground">{name}</span>
			{summary ? <p className="mt-0.5 text-xs text-muted-foreground">{summary}</p> : null}
		</div>
	</>;

	return (
		<section
			aria-labelledby={headingId}
			className="overflow-hidden rounded-lg border border-border bg-[var(--color-bg-settings-row)]"
			data-agent-provider={provider}
		>
			<header className="flex min-h-14 items-center justify-between gap-3 px-4 py-3">
				{collapsible ? <button
					type="button"
					aria-controls={contentId}
					aria-expanded={expanded}
					className="flex min-w-0 flex-1 items-center gap-3 rounded-sm text-left transition-colors hover:bg-settings-row-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default"
					disabled={collapseLocked}
					onClick={() => onExpandedChange(!expanded)}
				>
					{identity}
					<ChevronDown
						aria-hidden="true"
						className={`ml-auto size-4 shrink-0 text-muted-foreground ${animationsReady ? "transition-transform motion-reduce:transition-none" : ""} ${expanded ? "" : "-rotate-90"}`}
					/>
				</button> : <div className="flex min-w-0 flex-1 items-center gap-3">{identity}</div>}
				{action ? <div className="shrink-0">{action}</div> : null}
			</header>
			<div
				id={contentId}
				aria-hidden={!expanded}
				inert={!expanded}
				className={`grid ${animationsReady ? "transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none" : ""} ${expanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]"}`}
			>
				<div className={`min-h-0 overflow-hidden ${expanded ? "border-t border-border" : ""}`}>
					{children}
				</div>
			</div>
		</section>
	);
}
