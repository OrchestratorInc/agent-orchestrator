import { describe, expect, it, vi } from "vitest";
import type { CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { CloudCpError } from "./errors";
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

const event: CloudCpNotificationEvent = {
	sequence: 8,
	orgId: "org-2",
	recipientUserId: "user-1",
	kind: "notification_created",
	eventId: "approval:req-1",
	notification: {
		id: "ntf-1", source: "cloud", orgId: "org-2", type: "approval_needed", title: "Fix auth needs approval",
		body: "Run pnpm test", status: "unread", createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z",
		tier: 0, desktop: true,
	},
	createdAt: "2026-11-07T10:00:00Z",
};

describe("subscribeMyNotificationEventsBridged", () => {
	it("streams the caller's events from every organization after a sequence", async () => {
		const bridge = fakeBridge();
		const received: CloudCpNotificationEvent[] = [];
		const done = subscribeMyNotificationEventsBridged({ baseUrl: "https://cp.test/", after: 7, bridge, onEvent: (next) => received.push(next) });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		expect(bridge.openStream).toHaveBeenCalledWith({
			baseUrl: "https://cp.test",
			path: "/api/cloud/v1/me/notification-events?after=7",
			method: "GET",
			headers: { Accept: "text/event-stream" },
		});
		bridge.emit({ type: "chunk", data: `id: 8\nevent: notification_created\ndata: ${JSON.stringify(event)}\n\n: keepalive\n\n` });
		bridge.emit({ type: "end" });
		await done;
		expect(received).toEqual([event]);
	});

	it("reports a control plane without the route through onError with its status", async () => {
		const bridge = fakeBridge();
		vi.mocked(bridge.openStream).mockRejectedValue(new Error("Error invoking remote method: CLOUD_CP_STREAM_ERROR 404 404 page not found"));
		const onError = vi.fn();
		await subscribeMyNotificationEventsBridged({ baseUrl: "https://cp.test", bridge, onEvent: vi.fn(), onError });
		expect(onError).toHaveBeenCalledWith(expect.any(CloudCpError));
		expect((onError.mock.calls[0]?.[0] as CloudCpError).status).toBe(404);
	});

	it("closes the stream when aborted", async () => {
		const bridge = fakeBridge();
		const controller = new AbortController();
		const done = subscribeMyNotificationEventsBridged({ baseUrl: "https://cp.test", bridge, onEvent: vi.fn(), signal: controller.signal });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		controller.abort();
		await done;
		expect(bridge.closeStream).toHaveBeenCalledWith("stream-1");
	});
});

describe("subscribeNotificationEventsBridged", () => {
	it("keeps the organization stream's path", async () => {
		const bridge = fakeBridge();
		const done = subscribeNotificationEventsBridged({ baseUrl: "https://cp.test", orgId: "org 1", bridge, onEvent: vi.fn() });
		await vi.waitFor(() => expect(bridge.onStreamEvent).toHaveBeenCalled());
		expect(bridge.openStream).toHaveBeenCalledWith(expect.objectContaining({ path: "/api/cloud/v1/orgs/org%201/notification-events" }));
		bridge.emit({ type: "end" });
		await done;
	});
});
