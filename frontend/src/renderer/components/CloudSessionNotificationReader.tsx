import { useEffect, useMemo, useRef } from "react";
import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { readCloudSessionMentions, useCloudInboxSupport } from "../hooks/useCloudNotifications";
import { useCloudCp } from "../hooks/useCloudCp";
import { ASIDE_REPLY, cloudInboxQueryKey, MENTIONED } from "../lib/cloud-notifications";
import type { CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { useUiStore } from "../stores/ui-store";
import { useWindowAttention } from "../hooks/useWindowAttention";

/**
 * Reads the mention and aside reply notifications a cloud session raised for
 * this person while it is on screen in the focused window: opening the
 * session reads them, and so does one that arrives while they watch (so no
 * badge shows; a reply's aside icon keeps its dot). Settings covering the
 * session does not count. It only reads the
 * inbox the shell keeps live, and is its own component, so the inbox and
 * window focus it watches never re-render the session view.
 */
export function CloudSessionNotificationReader({ orgId, sessionId }: { orgId: string; sessionId: string }) {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const queryClient = useQueryClient();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const supported = useCloudInboxSupport(baseUrl) === "supported";
	const inbox = useQuery<CloudCpNotificationListResponse>({ queryKey: cloudInboxQueryKey(baseUrl, userId), queryFn: skipToken }).data;
	const attentive = useWindowAttention();
	const unreadKey = useMemo(() => !supported ? "" : (inbox?.items ?? [])
		.filter((row) => row.orgId === orgId && row.sessionId === sessionId && (row.type === MENTIONED || row.type === ASIDE_REPLY) && row.status === "unread")
		.map((row) => `${row.id}:${row.updatedAt}`)
		.join("|"), [inbox?.items, orgId, sessionId, supported]);
	const asked = useRef("");
	useEffect(() => {
		if (!ready || settingsOpen || !attentive || unreadKey === "" || asked.current === unreadKey) return;
		asked.current = unreadKey;
		void readCloudSessionMentions(queryClient, client, baseUrl, userId, orgId, sessionId).catch(() => {
			// Asked again the next time the session's rows change.
			asked.current = "";
		});
	}, [attentive, baseUrl, client, orgId, queryClient, ready, sessionId, settingsOpen, unreadKey, userId]);
	return null;
}
