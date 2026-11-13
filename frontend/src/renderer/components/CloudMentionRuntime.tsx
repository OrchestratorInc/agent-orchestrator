import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { useCloudCp } from "../hooks/useCloudCp";
import { useNotificationPreferences } from "../hooks/useNotificationPreferences";
import { aoBridge } from "../lib/bridge";
import {
	cloudAlertKey,
	DEFAULT_NOTIFICATION_PREFERENCES,
	shouldAlertCloudNotification,
	subscribeLiveCloudNotificationEvents,
} from "../lib/cloud-notifications";
import { mentionOpenTarget, prefetchMentionedSession, requestMentionFocus, type MentionOpenTarget } from "../lib/cloud-mention-open";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import { captureRendererEvent } from "../lib/telemetry";

const CLOUD_MENTION_ALERT_PREFIX = "cloud-mention:";
const MAX_REMEMBERED = 256;

function remember<T>(map: Map<string, T>, key: string, value: T): void {
	map.delete(key);
	map.set(key, value);
	if (map.size > MAX_REMEMBERED) map.delete(map.keys().next().value!);
}

/**
 * Opens a mention: the session, at the message, from whatever the cache holds.
 * The session view picks the request up, scrolls to the message and gives it
 * a brief glow.
 */
export function useOpenMention(): (target: MentionOpenTarget, projectId?: string) => boolean {
	const navigateToSession = useNavigateToSession();
	return useCallback((target: MentionOpenTarget, projectId?: string) => {
		const project = projectId ?? target.projectId;
		if (!project) return false;
		requestMentionFocus(target);
		recordManualWorkerOpen(target.sessionId);
		void captureRendererEvent("ao.notifications.opened", { target: "session" });
		navigateToSession(project, target.sessionId);
		return true;
	}, [navigateToSession]);
}

/**
 * Mentions as they arrive on the live /me stream: the session each points at
 * is read into the cache at once (so opening it is instant), and a desktop
 * alert shows when the person's preference allows it, unless that session is
 * on screen in the focused window. A newer mention in the same session
 * replaces its alert. Lives in the shell, beside NotificationRuntime.
 */
export function CloudMentionRuntime() {
	const queryClient = useQueryClient();
	const { client, ready, baseUrl } = useCloudCp();
	const { preferences } = useNotificationPreferences(ready);
	const openMention = useOpenMention();
	const params = useParams({ strict: false }) as { hostId?: string; sessionId?: string };
	const routeRef = useRef(params);
	routeRef.current = params;
	const preferencesRef = useRef(preferences);
	preferencesRef.current = preferences;
	const alerted = useRef(new Map<string, true>());
	const shown = useRef(new Map<string, MentionOpenTarget>());

	useEffect(() => subscribeLiveCloudNotificationEvents((event) => {
		const target = mentionOpenTarget(event.notification);
		if (!target) return;
		if (event.kind === "notification_created") void prefetchMentionedSession(queryClient, client, baseUrl, target);
		if (!shouldAlertCloudNotification(event, preferencesRef.current ?? DEFAULT_NOTIFICATION_PREFERENCES)) return;
		const row = event.notification;
		const key = cloudAlertKey(row);
		if (alerted.current.has(key)) return;
		remember(alerted.current, key, true);
		const { hostId, sessionId } = routeRef.current;
		// Already looking at it: no alert (and the session reads it at once, so no badge).
		const watched = !hostId && sessionId === row.sessionId && document.visibilityState === "visible" && document.hasFocus();
		if (watched) return;
		const id = `${CLOUD_MENTION_ALERT_PREFIX}${row.id}`;
		remember(shown.current, id, target);
		void aoBridge.notifications.show({
			id,
			title: row.title,
			body: row.body || undefined,
			type: row.type,
			watched,
			tag: `cloud-session:${row.sessionId}`,
		});
	}), [baseUrl, client, queryClient]);

	useEffect(() => aoBridge.notifications.onClick((id) => {
		if (!id.startsWith(CLOUD_MENTION_ALERT_PREFIX)) return;
		const target = shown.current.get(id);
		if (target) openMention(target);
	}), [openMention]);

	return null;
}
