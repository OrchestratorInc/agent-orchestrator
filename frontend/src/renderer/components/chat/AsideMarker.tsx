import { createContext, useContext } from "react";
import { useTranslation } from "react-i18next";
import { MessagesSquare } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { MentionTint } from "./MentionChip";

/** How a message's aside icon reads: there, lit while its aside is open, or with a dot for unseen replies. */
export type AsideIconState = "closed" | "open" | "unread";

/**
 * The asides of the conversation on screen, for the messages that started
 * them. The chat surface of a shared Cloud session provides it once its
 * control plane has asides; without it a message draws as before.
 */
export interface AsideMarkers {
	stateOf: (asideId: string) => AsideIconState;
	toggle: (asideId: string) => void;
}

export const AsideMarkersContext = createContext<AsideMarkers | undefined>(undefined);

/** Whether a message's aside draws: it has one and the conversation knows its asides. */
export function useAsideMarker(asideId: string | undefined): boolean {
	const markers = useContext(AsideMarkersContext);
	return Boolean(asideId && markers);
}

/** A message with an aside looks the same for the sender and everyone it tags: no chip is tinted. */
export const NO_MENTION_TINT: MentionTint = {};

/** The dashed box a message with an aside draws as: only its text and chips, no fill. */
export function asideBoxClass(queued?: boolean): string {
	return cn(
		"w-fit max-w-full rounded-[10px] border border-dashed border-foreground/25 bg-transparent px-3 py-2.5 text-sm leading-[1.55]",
		queued ? "text-muted-foreground" : "text-foreground",
	);
}

/**
 * The aside icon under a message that tags people: the messages-square glyph
 * and nothing else. It opens and closes the aside beside the conversation, is
 * lit while that aside is open, and carries a dot while the reader has replies
 * they haven't seen.
 */
export function AsideIcon({ asideId, className }: { asideId: string; className?: string }) {
	const { t } = useTranslation();
	const markers = useContext(AsideMarkersContext);
	if (!markers) return null;
	const state = markers.stateOf(asideId);
	const open = state === "open";
	const label = open ? t("aside.close") : state === "unread" ? t("aside.openUnread") : t("aside.open");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					aria-label={label}
					aria-pressed={open}
					className={cn("relative", className)}
					data-active={open ? "true" : undefined}
					data-aside-icon={state}
					onClick={(event) => {
						event.stopPropagation();
						markers.toggle(asideId);
					}}
					size="icon-sm"
					type="button"
					variant="quiet"
				>
					<MessagesSquare aria-hidden="true" className={cn("size-icon-base", state === "unread" && "text-foreground")} />
					{state === "unread" ? (
						<span aria-hidden="true" className="absolute top-1 right-1 size-1.5 rounded-full bg-status-working ring-2 ring-background" data-aside-unread="" />
					) : null}
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
