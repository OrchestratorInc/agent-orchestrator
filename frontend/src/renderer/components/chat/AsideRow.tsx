import { EyeOff, Send } from "lucide-react";
import { createContext, useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ConversationActivity } from "../../types/conversation";
import { PersonAvatar } from "../teams/TeamVisuals";
import { Button } from "../ui/button";

/** What a shared cloud session lets its asides do, and how it names their authors. */
export interface AsideActions {
	/** The signed-in person, whose own asides read "You". */
	selfUserId?: string;
	/** A person's name from the session (who is here, who follows it), when the aside carries none. */
	nameOf?: (userId: string) => string | undefined;
	/** Puts an aside forward for whoever drives the session; absent, asides offer nothing. */
	onSuggest?: (asideId: string) => Promise<unknown>;
	/** Asides already waiting for the driver. */
	suggestedAsideIds?: ReadonlySet<string>;
}

export const AsideActionsContext = createContext<AsideActions>({});

/**
 * An aside in the timeline: a note people wrote for each other, which the
 * agent never sees. It reads as a note rather than a chat bubble (a dashed
 * outline, the author, and a quiet "Agent can't see this"), and anyone in the
 * session can put it forward to the driver with Suggest to agent.
 */
export function AsideRow({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const actions = useContext(AsideActionsContext);
	const [state, setState] = useState<"idle" | "sending" | "failed">("idle");
	const aside = activity.detail?.aside;
	if (!aside) return null;
	const mine = Boolean(actions.selfUserId) && aside.authorUserId === actions.selfUserId;
	const known = aside.authorName || (aside.authorUserId ? actions.nameOf?.(aside.authorUserId) : undefined);
	const name = mine ? t("aside.you") : known || t("chat.shared.someone");
	const suggested = actions.suggestedAsideIds?.has(aside.asideId) ?? false;
	const suggest = actions.onSuggest;
	return (
		<div
			className="group/aside flex min-w-0 flex-col gap-1 rounded-lg border border-dashed border-muted-foreground/35 px-3 py-2"
			data-aside={aside.asideId}
		>
			<div className="flex min-h-6 min-w-0 items-center gap-2 text-xs">
				<PersonAvatar name={known || name} size="xs" />
				<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
				<span className="flex-1" />
				{suggest && suggested ? (
					<span className="shrink-0 text-caption text-muted-foreground" data-aside-suggested="">{t("aside.suggested")}</span>
				) : suggest ? (
					<Button
						className="shrink-0 opacity-0 group-hover/aside:opacity-100 group-focus-within/aside:opacity-100 data-[state=failed]:opacity-100"
						data-state={state}
						disabled={state === "sending"}
						onClick={() => {
							setState("sending");
							void suggest(aside.asideId).then(() => setState("idle"), () => setState("failed"));
						}}
						size="xs"
						type="button"
						variant="quiet"
					>
						<Send aria-hidden="true" className="size-3" />
						{state === "failed" ? t("aside.suggestFailed") : t("aside.suggest")}
					</Button>
				) : null}
				<span className="inline-flex shrink-0 items-center gap-1 text-caption text-passive">
					<EyeOff aria-hidden="true" className="size-3" />
					{t("aside.hiddenFromAgent")}
				</span>
			</div>
			<p className="text-sm leading-relaxed whitespace-pre-wrap break-words text-foreground">{aside.text}</p>
		</div>
	);
}
