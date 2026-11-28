import { useEffect, useMemo, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { readCloudSessionMentions, useCloudInboxSupport } from "../hooks/useCloudNotifications";
import { useCloudCp } from "../hooks/useCloudCp";
import { useMentionsOffered } from "../hooks/useMentionsOffered";
import { useCachedQueryData } from "../hooks/useCloudScriptRuns";
import { ASIDE_REPLY, cloudInboxQueryKey, MENTIONED } from "../lib/cloud-notifications";
import type { CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { useUiStore } from "../stores/ui-store";
import { useWindowAttention } from "../hooks/useWindowAttention";
import { useMainViewVisible } from "../lib/main-view-visibility";

/**
 * Reads the mention and aside reply notifications a cloud session raised for
 * this person while it is on screen in the focused window: opening the
 * session reads them, and so does one that arrives while they watch (so no
 * badge shows; a reply's aside icon keeps its dot). Settings covering the
 * session does not count. It only reads the
 * inbox the shell keeps live, and is its own component, so the inbox and
 * window focus it watches never re-render the session view. Nothing is read
 * while the offering does not list mentions (the bell does not show those rows).
 */
export function CloudSessionNotificationReader({ orgId, sessionId, conversationShown = true }: {
	orgId: string;
	sessionId: string;
	/** The session's conversation is in front: no file or other tab covers it. */
	conversationShown?: boolean;
}) {
	// Covered by a popped-out browser or Settings, the session is not on screen either.
	const mainViewVisible = useMainViewVisible();
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const queryClient = useQueryClient();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const offered = useMentionsOffered(ready, orgId);
	const supported = useCloudInboxSupport(baseUrl) === "supported" && offered;
	// Read from the cache, never as an observer: an observer's options become the
	// query's, and a skipToken one would leave the bell's next refresh nothing to fetch with.
	const inbox = useCachedQueryData<CloudCpNotificationListResponse>(cloudInboxQueryKey(baseUrl, userId), true);
	const attentive = useWindowAttention();
	const unreadKey = useMemo(() => !supported ? "" : (inbox?.items ?? [])
		.filter((row) => row.orgId === orgId && row.sessionId === sessionId && (row.type === MENTIONED || row.type === ASIDE_REPLY) && row.status === "unread")
		.map((row) => `${row.id}:${row.updatedAt}`)
		.join("|"), [inbox?.items, orgId, sessionId, supported]);
	const asked = useRef("");
	useEffect(() => {
		if (!ready || settingsOpen || !attentive || !mainViewVisible || !conversationShown || unreadKey === "" || asked.current === unreadKey) return;
		asked.current = unreadKey;
		void readCloudSessionMentions(queryClient, client, baseUrl, userId, orgId, sessionId).catch(() => {
			// Asked again the next time the session's rows change.
			asked.current = "";
		});
	}, [attentive, baseUrl, client, conversationShown, mainViewVisible, orgId, queryClient, ready, sessionId, settingsOpen, unreadKey, userId]);
	return null;
}
