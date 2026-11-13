import { describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { subscribeMyNotificationEventsBridged, subscribeNotificationEventsBridged, type CloudCpStreamBridge } from "./stream-bridge";
import type { CloudCpNotificationEvent } from "./types";

function fakeBridge() {
	let listener: ((event: CloudCpStreamEvent) => void) | undefined;
	const bridge: CloudCpStreamBridge & { emit: (event: CloudCpStreamEvent) => void } = {
		openStream: vi.fn(async () => ({ streamId: "stream-1" })),
		closeStream: vi.fn(),
		onStreamEvent: vi.fn((_id, next) => {
			listener = next;
			return () => { listener = undefined; };
		}),
		emit: (event) => listener?.(event),
	};
	return bridge;
}

const event = { sequence: 9, orgId: "org-guest", recipientUserId: "u-1", kind: "notification_created", notification: { id: "n-1" }, createdAt: "2026-11-13T10:00:00Z" } as unknown as CloudCpNotificationEvent;

describe("the caller's notification stream", () => {
	it("streams /me events from every organization after a sequence", async () => {
		const bridge = fakeBridge();
		const received: CloudCpNotificationEvent[] = [];
		const done = subscribeMyNotificationEventsBridged({ baseUrl: "https://cp.test/", after: 41, bridge, onEvent: (next) => received.push(next) });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		expect(bridge.openStream).toHaveBeenCalledWith({
			baseUrl: "https://cp.test",
			path: "/api/cloud/v1/me/notification-events?after=41",
			method: "GET",
			headers: { Accept: "text/event-stream" },
		});
		bridge.emit({ type: "chunk", data: `data: ${JSON.stringify(event)}\n\n` });
		bridge.emit({ type: "end" });
		await done;
		expect(received).toEqual([event]);
	});

	it("keeps the organization's stream where it was", async () => {
		const bridge = fakeBridge();
		const done = subscribeNotificationEventsBridged({ baseUrl: "https://cp.test", orgId: "org 1", bridge, onEvent: vi.fn() });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		expect(bridge.openStream).toHaveBeenCalledWith(expect.objectContaining({ path: "/api/cloud/v1/orgs/org%201/notification-events" }));
		bridge.emit({ type: "end" });
		await done;
	});
});
