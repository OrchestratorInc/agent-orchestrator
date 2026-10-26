import { createContext, useContext, useEffect, useRef, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { conversationActivityQueryOptions } from "../../hooks/useConversation";
import type { ConversationActivity } from "../../types/conversation";

/** The session (and remote host) whose timeline is on screen, for reading full activity rows. */
export const ChatActivitySource = createContext<{ sessionId: string; hostId?: string } | undefined>(undefined);

export interface FullActivity {
	/** The full row once loaded, else the last full copy of this row, else the preview. */
	activity: ConversationActivity;
	/** Nothing better than the preview is on hand yet. */
	pending: boolean;
	failed: boolean;
	retry: () => void;
}

type Render = (full: FullActivity) => ReactNode;

/**
 * An opened row's body, drawn from the full activity when the page sent only a
 * preview. Mounted only while the row is open, so a collapsed row never fetches.
 * A row that settles while open keeps showing the copy it streamed until the full
 * one arrives, so it does not blink to a spinner.
 */
export function FullActivityBody({ activity, children }: { activity: ConversationActivity; children: Render }) {
	const source = useContext(ChatActivitySource);
	const lastFull = useRef<ConversationActivity | undefined>(undefined);
	useEffect(() => {
		if (!activity.detailOmitted) lastFull.current = activity;
	}, [activity]);
	if (!activity.detailOmitted || !source) {
		return children({ activity, pending: false, failed: false, retry: () => {} });
	}
	const previous = lastFull.current?.id === activity.id ? lastFull.current : undefined;
	return (
		<FetchedActivityBody activity={activity} source={source} previous={previous}>
			{children}
		</FetchedActivityBody>
	);
}

function FetchedActivityBody({ activity, source, previous, children }: {
	activity: ConversationActivity;
	source: { sessionId: string; hostId?: string };
	previous?: ConversationActivity;
	children: Render;
}) {
	const query = useQuery(conversationActivityQueryOptions(source.sessionId, activity, source.hostId));
	const shown = query.data ?? previous;
	return children({
		activity: shown ?? activity,
		pending: !shown,
		failed: query.isError && !query.data,
		retry: () => void query.refetch(),
	});
}
