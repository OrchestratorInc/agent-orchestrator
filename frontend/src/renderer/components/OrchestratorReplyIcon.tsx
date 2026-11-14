import type { TFunction } from "i18next";
import type { SVGProps } from "react";
import { cn } from "../lib/utils";
import type { OrchestratorReplyState } from "../stores/orchestrator-reply-store";
import { OrchestratorIcon } from "./icons";

const SURFACE = {
	sidebar: { ring: "ring-sidebar", unread: "bg-primary", working: "bg-muted-foreground" },
	topbar: { ring: "ring-background", unread: "bg-primary", working: "bg-muted-foreground" },
	filled: { ring: "ring-accent-strong", unread: "bg-accent-foreground", working: "bg-accent-foreground/60" },
} as const;

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

export function orchestratorReplyLabel(label: string, reply: OrchestratorReplyState, t: TFunction): string {
	if (reply === "unread") return `${label} · ${t("shell.orchestratorNewReply")}`;
	if (reply === "working") return `${label} · ${t("shell.orchestratorWorking")}`;
	return label;
}
