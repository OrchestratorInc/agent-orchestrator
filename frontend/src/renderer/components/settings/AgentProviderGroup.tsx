import { ChevronDown } from "lucide-react";
import { useId, type ReactNode } from "react";
import { AgentAvatar } from "../AgentAvatar";

/**
 * Shared settings card for one agent provider. Provider identity and
 * provider-level actions live in the header row; when expanded, the children
 * follow as direct card rows (settings-row-bar / settings-content-row) so the
 * card's own hairline divides them.
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
	const identity = <>
		<AgentAvatar className="size-7 shrink-0" decorative provider={provider} />
		<div className="min-w-0">
			<span id={headingId} className="block truncate text-sm leading-5 text-settings-label">{name}</span>
			{summary ? <p className="mt-0.5 text-pretty text-control leading-4 text-settings-muted">{summary}</p> : null}
		</div>
	</>;

	return (
		<section
			aria-labelledby={headingId}
			className="settings-grouped-rows flex w-full flex-col"
			data-agent-provider={provider}
		>
			<header className="settings-row-bar">
				{collapsible ? <button
					type="button"
					aria-expanded={expanded}
					className="flex min-w-0 flex-1 items-center gap-(--size-settings-row-icon-gap) rounded-sm text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default"
					disabled={collapseLocked}
					onClick={() => onExpandedChange(!expanded)}
				>
					{identity}
					<ChevronDown
						aria-hidden="true"
						className={`ml-auto size-4 shrink-0 text-settings-muted transition-transform ${expanded ? "" : "-rotate-90"}`}
					/>
				</button> : <div className="flex min-w-0 flex-1 items-center gap-(--size-settings-row-icon-gap)">{identity}</div>}
				{action ? <div className="shrink-0">{action}</div> : null}
			</header>
			{expanded ? children : null}
		</section>
	);
}
