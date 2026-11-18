import type { TFunction } from "i18next";
import type { SVGProps } from "react";
import { cn } from "../lib/utils";
import type { OrchestratorReplyState } from "../stores/orchestrator-reply-store";
import { OrchestratorIcon } from "./icons";

// The ring cuts the dot out of the surface the button sits on. On a filled
// button the dot takes the label colour, since the accent is the fill.
const SURFACE = {
	sidebar: { ring: "ring-sidebar", unread: "bg-primary", working: "bg-muted-foreground" },
	topbar: { ring: "ring-background", unread: "bg-primary", working: "bg-muted-foreground" },
	filled: { ring: "ring-accent-strong", unread: "bg-accent-foreground", working: "bg-accent-foreground/60" },
} as const;

/**
 * The orchestrator mark with the same corner dot as the Browser button's
 * unseen badge: solid accent for a reply the user has not seen, a quiet
 * breathing dot while the orchestrator works.
 */
export function OrchestratorReplyIcon({ reply, surface, ...props }: SVGProps<SVGSVGElement> & {
	reply: OrchestratorReplyState;
	surface: keyof typeof SURFACE;
}) {
	const tone = SURFACE[surface];
	return (
		<span className="relative inline-flex">
			<OrchestratorIcon aria-hidden="true" {...props} />
			{reply ? (
				<span
					aria-hidden="true"
					className={cn(
						"pointer-events-none absolute -right-0.5 -top-0.5 size-1.5 rounded-full ring-2",
						tone.ring,
						reply === "unread" ? tone.unread : cn(tone.working, "animate-status-pulse motion-reduce:animate-none"),
					)}
					data-orchestrator-reply={reply}
				/>
			) : null}
		</span>
	);
}

/** Appended to the button's label so the dot is never the only signal. */
export function orchestratorReplyLabel(label: string, reply: OrchestratorReplyState, t: TFunction): string {
	if (reply === "unread") return `${label} · ${t("shell.orchestratorNewReply")}`;
	if (reply === "working") return `${label} · ${t("shell.orchestratorWorking")}`;
	return label;
}
