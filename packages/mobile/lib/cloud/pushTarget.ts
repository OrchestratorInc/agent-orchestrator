import type { Href } from "expo-router";
import { CLOUD_HOST_ID } from "./mapping";

/**
 * Where a tapped Orchestrator Cloud notification goes: its session, else its project,
 * else the cloud inbox. Null for a notification a paired machine sent, which
 * keeps its own routing.
 */
export function cloudPushTarget(data: { source?: string; sessionId?: string; projectId?: string }): Href | null {
	if (data.source !== "cloud") return null;
	if (data.sessionId) return { pathname: "/session/[id]", params: { id: data.sessionId, hostId: CLOUD_HOST_ID } };
	if (data.projectId) return { pathname: "/project/[id]", params: { id: data.projectId, hostId: CLOUD_HOST_ID } };
	return "/notifications";
}
