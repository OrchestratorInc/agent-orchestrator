import { useEffect, useMemo, useRef, useState } from "react";
import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { readCloudSessionMentions, useCloudInboxSupport } from "../hooks/useCloudNotifications";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudInboxQueryKey, MENTIONED } from "../lib/cloud-notifications";
import type { CloudCpNotificationListResponse } from "../lib/cloud-cp/types";
import { useUiStore } from "../stores/ui-store";

/**
 * Reads the mention notifications a cloud session raised for this person
 * while it is on screen in the focused window: opening the session reads
 * them, and so does a mention that arrives while they watch (so no badge
 * shows). Settings covering the session does not count. It only reads the
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
		.filter((row) => row.orgId === orgId && row.sessionId === sessionId && row.type === MENTIONED && row.status === "unread")
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

/** Whether the person can see this window: it is visible and has focus. */
function useWindowAttention(): boolean {
	const [attentive, setAttentive] = useState(windowAttentive);
	useEffect(() => {
		const update = () => setAttentive(windowAttentive());
		window.addEventListener("focus", update);
		window.addEventListener("blur", update);
		document.addEventListener("visibilitychange", update);
		update();
		return () => {
			window.removeEventListener("focus", update);
			window.removeEventListener("blur", update);
			document.removeEventListener("visibilitychange", update);
		};
	}, []);
	return attentive;
}

function windowAttentive(): boolean {
	return document.visibilityState === "visible" && document.hasFocus();
}
