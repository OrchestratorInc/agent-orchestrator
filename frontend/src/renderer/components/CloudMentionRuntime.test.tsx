import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpNotification, CloudCpNotificationEvent } from "../lib/cloud-cp/types";
import { clearMentionFocus, peekMentionFocus } from "../lib/cloud-mention-open";
import { publishLiveCloudNotificationEvent } from "../lib/cloud-notifications";
import { CloudMentionRuntime } from "./CloudMentionRuntime";

const mocks = vi.hoisted(() => ({
	show: vi.fn(),
	clickListeners: [] as Array<(id: string) => void>,
	navigate: vi.fn(),
	params: { sessionId: undefined as string | undefined, hostId: undefined as string | undefined },
	preferences: undefined as { mentions: boolean; teamMentions: boolean } | undefined,
	prefetch: vi.fn(async () => undefined),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		notifications: {
			show: mocks.show,
			onClick: (listener: (id: string) => void) => {
				mocks.clickListeners.push(listener);
				return () => { mocks.clickListeners.splice(mocks.clickListeners.indexOf(listener), 1); };
			},
		},
	},
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate, useParams: () => mocks.params }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, client: {}, baseUrl: "https://cp.test", userId: "u-priya" }) }));
vi.mock("../hooks/useNotificationPreferences", () => ({ useNotificationPreferences: () => ({ preferences: mocks.preferences }) }));
vi.mock("../lib/cloud-mention-open", async (original) => ({
	...await original<typeof import("../lib/cloud-mention-open")>(),
	prefetchMentionedSession: mocks.prefetch,
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

const row = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
	id: "n-1",
	source: "cloud",
	orgId: "org-1",
	projectId: "project-1",
	sessionId: "session-1",
	type: "mentioned",
	title: "Chen Li mentioned you",
	body: "Can you check the retry backoff before we merge?",
	status: "unread",
	eventId: "e-1",
	metadata: { authorUserId: "u-chen", authorName: "Chen Li", sequence: 42 },
	createdAt: "2026-11-13T10:00:00Z",
	updatedAt: "2026-11-13T10:00:00Z",
	...overrides,
});
const event = (notification: CloudCpNotification): CloudCpNotificationEvent => ({
	sequence: 1, orgId: notification.orgId, recipientUserId: "u-priya", kind: "notification_created", notification, createdAt: notification.createdAt,
});

function renderRuntime() {
	const queryClient = new QueryClient();
	const ui = () => <QueryClientProvider client={queryClient}><CloudMentionRuntime /></QueryClientProvider>;
	const view = render(ui());
	return { ...view, refresh: () => view.rerender(ui()) };
}

beforeEach(() => {
	mocks.show.mockReset();
	mocks.navigate.mockReset();
	mocks.prefetch.mockClear();
	mocks.params = { sessionId: undefined, hostId: undefined };
	mocks.preferences = undefined;
	const pending = peekMentionFocus();
	if (pending) clearMentionFocus(pending.sessionId, pending.nonce);
	vi.spyOn(document, "hasFocus").mockReturnValue(false);
});

describe("CloudMentionRuntime", () => {
	it("alerts on the desktop for a mention, one alert per session, and reads the session into the cache", () => {
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(mocks.prefetch).toHaveBeenCalledWith(expect.anything(), expect.anything(), "https://cp.test", expect.objectContaining({ sessionId: "session-1", sequence: 42 }));
		expect(mocks.show).toHaveBeenCalledWith({
			id: "cloud-mention:n-1",
			title: "Chen Li mentioned you",
			body: "Can you check the retry backoff before we merge?",
			type: "mentioned",
			watched: false,
			tag: "cloud-session:session-1",
		});
	});

	it("alerts again for a newer mention in the same session, never twice for the same one", () => {
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(mocks.show).toHaveBeenCalledTimes(1);
		act(() => publishLiveCloudNotificationEvent(event(row({ eventId: "e-2", body: "And the cap?" }))));
		expect(mocks.show).toHaveBeenCalledTimes(2);
	});

	it("follows the person's two switches; the session is still read into the cache", () => {
		mocks.preferences = { mentions: false, teamMentions: true };
		const view = renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(mocks.show).not.toHaveBeenCalled();
		expect(mocks.prefetch).toHaveBeenCalledTimes(1);
		act(() => publishLiveCloudNotificationEvent(event(row({ id: "n-2", eventId: "e-3", title: "Chen Li mentioned Platform", metadata: { sequence: 43, teamId: "t-1", teamName: "Platform" } }))));
		expect(mocks.show).toHaveBeenCalledTimes(1);
		mocks.preferences = { mentions: true, teamMentions: false };
		view.refresh();
		act(() => publishLiveCloudNotificationEvent(event(row({ id: "n-3", eventId: "e-4", metadata: { sequence: 44, teamId: "t-1" } }))));
		expect(mocks.show).toHaveBeenCalledTimes(1);
	});

	it("does not alert while that session is on screen in the focused window", () => {
		mocks.params = { sessionId: "session-1", hostId: undefined };
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(mocks.show).not.toHaveBeenCalled();
		// Another session on screen still alerts (the main process holds toasts while focused).
		act(() => publishLiveCloudNotificationEvent(event(row({ id: "n-9", eventId: "e-9", sessionId: "session-2" }))));
		expect(mocks.show).toHaveBeenCalledTimes(1);
	});

	it("never alerts for other kinds", () => {
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row({ type: "pr_merged" }))));
		expect(mocks.show).not.toHaveBeenCalled();
		expect(mocks.prefetch).not.toHaveBeenCalled();
	});

	it("opens the session at the message when the alert is clicked", () => {
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		act(() => { for (const listener of mocks.clickListeners) listener("cloud-mention:n-1"); });
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "project-1", sessionId: "session-1" } });
		expect(peekMentionFocus()).toMatchObject({ sessionId: "session-1", sequence: 42 });
	});
});
