import type { ReactNode } from "react";
import type { AgentProvider } from "../../types/workspace";
import {
	CheckCheck,
	CircleAlert,
	CircleCheck,
	GitMerge,
	GitPullRequest,
	GitPullRequestClosed,
	Info,
	QuestionMark,
} from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";

/**
 * What happened, in the sidebar's own marks, so a toast and the session's row
 * agree: an amber question is the agent asking you, a red alert is something
 * to fix, a green pull request is ready, purple is merged.
 */
export type ToastMarker = "input" | "attention" | "ready" | "merged" | "closed" | "done" | "success" | "error" | "info";

const MARKERS = {
	input: { Icon: QuestionMark, tone: "text-status-in-review" },
	attention: { Icon: CircleAlert, tone: "text-destructive" },
	ready: { Icon: GitPullRequest, tone: "text-status-ready" },
	merged: { Icon: GitMerge, tone: "text-status-merged" },
	closed: { Icon: GitPullRequestClosed, tone: "text-error" },
	done: { Icon: CheckCheck, tone: "text-success" },
	success: { Icon: CircleCheck, tone: "text-success" },
	error: { Icon: CircleAlert, tone: "text-error" },
	info: { Icon: Info, tone: "text-muted-foreground" },
} as const satisfies Record<ToastMarker, { Icon: unknown; tone: string }>;

/**
 * The tile at the start of a toast:
 * - `tile`: the session's agent with what happened as a badge on its corner,
 *   or the mark alone when no agent is involved.
 * - `node`: anything else.
 */
export type ToastArt =
	| { kind: "tile"; marker: ToastMarker; provider?: AgentProvider }
	| { kind: "node"; node: ReactNode };

/** The tile for a plain note. */
export function toneArt(tone: "info" | "success" | "error"): ToastArt {
	return { kind: "tile", marker: tone };
}

/**
 * The tile sits one padding inside the card's corner, so its radius is the
 * card's less that padding (the card's own is the controls' radius plus it).
 */
export function ToastArtView({ art }: { art: ToastArt }) {
	if (art.kind === "node") return <span aria-hidden="true" className="toast-tile self-start">{art.node}</span>;
	const { Icon, tone } = MARKERS[art.marker];
	return (
		<span aria-hidden="true" className="toast-tile self-start" data-marker={art.marker}>
			{art.provider ? (
				<>
					<AgentAvatar className="size-5" decorative provider={art.provider} />
					<span className="toast-tile-badge">
						<Icon className={cn("size-3", tone)} />
					</span>
				</>
			) : (
				<Icon className={cn("size-icon-base", tone)} />
			)}
		</span>
	);
}
