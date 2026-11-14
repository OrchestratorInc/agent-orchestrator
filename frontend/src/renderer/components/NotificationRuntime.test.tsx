import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpNotification } from "../lib/cloud-cp/types";
import type { NotificationDTO, NotificationsCache } from "../lib/notifications";
import { NotificationRuntime } from "./NotificationCenter";

const notifications: NotificationDTO[] = [
	{
		id: "ntf_1",
		sessionId: "sess-1",
		projectId: "proj-1",
		prUrl: "",
		type: "needs_input",
		title: "Needs input",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T10:00:00Z",
		target: { kind: "session", sessionId: "sess-1" },
	},
	{
		id: "ntf_2",
		sessionId: "sess-2",
		projectId: "proj-1",
		prUrl: "",
		type: "ready_to_merge",
		title: "Ready to merge",
		body: "",
		status: "unread",
		createdAt: "2026-06-16T11:00:00Z",
		target: { kind: "session", sessionId: "sess-2" },
	},
];

const unreadCache: NotificationsCache = {
	pages: [{ notifications, nextCursor: undefined, unreadCount: notifications.length, unresolvedCount: notifications.length }],
	pageParams: [""],
};

const { setBadge, show, clickListeners, navigateToSession, cloud } = vi.hoisted(() => ({
	setBadge: vi.fn(),
	show: vi.fn(),
	clickListeners: new Set<(id: string) => void>(),
	navigateToSession: vi.fn(),
	// The Cloud inbox CloudNotificationRuntime keeps live; absent until a test loads one.
	cloud: { data: undefined as { items: CloudCpNotification[]; unreadCount: number } | undefined, orgId: "org-1" },
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn(), useParams: () => ({}) }));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigateToSession }));
vi.mock("../hooks/useCloudNotifications", () => ({
	useCloudNotifications: () => ({ data: cloud.data, items: cloud.data?.items }),
}));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ userId: "user-1" }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: cloud.orgId } }) }));

vi.mock("../hooks/useNotificationsQuery", () => ({
	useNotificationsQuery: () => ({ data: unreadCache, isError: false }),
}));

vi.mock("../lib/notifications", async (importOriginal) => ({
	...((await importOriginal()) as object),
	createNotificationsTransport: () => ({ connect: () => undefined }),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		notifications: {
			setBadge,
			show,
			devBounce: vi.fn(),
			onClick: (listener: (id: string) => void) => {
				clickListeners.add(listener);
				return () => clickListeners.delete(listener);
			},
		},
	},
}));

function renderRuntimeOnly() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			{/* NotificationCenter (the bell) is intentionally NOT mounted here. */}
			<NotificationRuntime />
		</QueryClientProvider>,
	);
}

describe("NotificationRuntime badge sync", () => {
	it("syncs the launcher badge without the notification bell being mounted", async () => {
		setBadge.mockClear();

		renderRuntimeOnly();

		// The badge must update purely from the always-mounted runtime, independent
		// of NotificationCenter, which Linux hides from the topbar and only mounts
		// on the sessions board.
		await waitFor(() => expect(setBadge).toHaveBeenCalledWith(notifications.length));
	});
});

function cloudNotification(id: string, overrides: Partial<CloudCpNotification> = {}): CloudCpNotification {
	return {
		id,
		source: "cloud",
		orgId: "org-1",
		projectId: "cloud-proj",
		sessionId: "cloud-sess",
		type: "needs_input",
		title: `Cloud ${id}`,
		body: "Waiting on you",
		status: "unread",
		createdAt: "2026-06-16T12:00:00Z",
		updatedAt: "2026-06-16T12:00:00Z",
		...overrides,
	};
}

describe("NotificationRuntime Cloud notifications", () => {
	it("counts Cloud unread in the launcher badge, as the bell does", async () => {
		setBadge.mockClear();
		cloud.data = { items: [cloudNotification("c1")], unreadCount: 3 };
		try {
			renderRuntimeOnly();
			await waitFor(() => expect(setBadge).toHaveBeenCalledWith(notifications.length + 3));
		} finally {
			cloud.data = undefined;
		}
	});

	it("raises an OS notification for a new Cloud row without replaying the loaded inbox, and opens its session on click", () => {
		show.mockClear();
		navigateToSession.mockClear();
		cloud.orgId = "org-1";
		cloud.data = { items: [cloudNotification("old")], unreadCount: 1 };
		try {
			const view = renderRuntimeOnly();
			expect(show).not.toHaveBeenCalled();

			cloud.data = { items: [cloudNotification("new"), cloudNotification("read", { status: "read" }), cloudNotification("old")], unreadCount: 2 };
			view.rerender(
				<QueryClientProvider client={new QueryClient()}>
					<NotificationRuntime />
				</QueryClientProvider>,
			);
			expect(show).toHaveBeenCalledTimes(1);
			expect(show).toHaveBeenCalledWith({ id: "cloud-notification:new", title: "Cloud new", body: "Waiting on you", type: "needs_input", watched: false });

			act(() => { for (const listener of clickListeners) listener("cloud-notification:new"); });
			expect(navigateToSession).toHaveBeenCalledWith("cloud-proj", "cloud-sess");

			// Another organization's inbox is a new inbox, not a burst of new rows.
			cloud.orgId = "org-2";
			cloud.data = { items: [cloudNotification("other-org", { orgId: "org-2" })], unreadCount: 1 };
			view.rerender(
				<QueryClientProvider client={new QueryClient()}>
					<NotificationRuntime />
				</QueryClientProvider>,
			);
			expect(show).toHaveBeenCalledTimes(1);
			view.unmount();
		} finally {
			cloud.data = undefined;
			cloud.orgId = "org-1";
		}
	});
});
