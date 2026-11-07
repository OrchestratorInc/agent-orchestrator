import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpNotification, CloudCpNotificationEvent } from "../lib/cloud-cp/types";
import { isLegacyCloudNeedsYou, publishLiveCloudNotificationEvent } from "../lib/cloud-notifications";
import {
	applyNotificationsCleared,
	type NotificationDTO,
	type NotificationListStatus,
} from "../lib/notifications";
import { useUiStore } from "../stores/ui-store";
import { NotificationCenter, NotificationRuntime } from "./NotificationCenter";
import { TooltipProvider } from "./ui/tooltip";

const {
	clearAllMock,
	clearOneMock,
	cloudNotificationsMock,
	connectMock,
	decideChatApprovalMock,
	fetchNextPageMock,
	markAllMock,
	navigateMock,
	notificationQueryMock,
	paramsMock,
	restoreSessionMock,
	workspaceQueryMock,
} = vi.hoisted(() => ({
	clearAllMock: vi.fn(),
	clearOneMock: vi.fn(),
	cloudNotificationsMock: vi.fn(),
	connectMock: vi.fn(),
	decideChatApprovalMock: vi.fn(),
	fetchNextPageMock: vi.fn(),
	markAllMock: vi.fn(),
	navigateMock: vi.fn(),
	notificationQueryMock: vi.fn(),
	paramsMock: vi.fn(),
	restoreSessionMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
}));

const allNotifications: NotificationDTO[] = [
	{
		id: "ntf_2",
		sessionId: "sess-2",
		projectId: "proj-1",
		prUrl: "https://github.com/acme/app/pull/67",
		type: "ready_to_merge",
		title: "Fix checkout totals · PR #67",
		body: "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback.",
		status: "unread",
		createdAt: "2026-07-21T11:00:00Z",
		target: { kind: "pr", sessionId: "sess-2", prUrl: "https://github.com/acme/app/pull/67" },
	},
	{
		id: "ntf_1",
		sessionId: "sess-1",
		projectId: "proj-1",
		prUrl: "",
		type: "needs_input",
		title: "Checkout flow needs input",
		body: "The agent is waiting for your response.",
		status: "unread",
		createdAt: "2026-07-21T10:00:00Z",
		target: { kind: "session", sessionId: "sess-1" },
	},
	{
		id: "ntf_4",
		sessionId: "sess-4",
		projectId: "proj-1",
		prUrl: "",
		type: "needs_input",
		title: "Docs sweep needs input",
		body: "The agent is still waiting for your response.",
		status: "read",
		createdAt: "2026-07-20T09:00:00Z",
		target: { kind: "session", sessionId: "sess-4" },
	},
	{
		id: "ntf_dead",
		sessionId: "sess-dead",
		projectId: "proj-1",
		prUrl: "",
		// needs_input: an agent is genuinely paused waiting on this session, so a
		// terminated backing session should still require restore before opening.
		type: "needs_input",
		title: "PR #9 merged",
		body: "The agent session has ended.",
		status: "read",
		createdAt: "2026-07-19T09:00:00Z",
		target: { kind: "session", sessionId: "sess-dead" },
	},
];

const unreadNotifications = allNotifications.filter((item) => item.status === "unread");

const cloudNotifications: CloudCpNotification[] = [
	{
		id: "cntf_ready",
		source: "cloud",
		orgId: "org-1",
		projectId: "cproj-1",
		sessionId: "csess-1",
		type: "ready_to_merge",
		title: "Pull request ready to merge",
		body: "acme/cloud#8 is ready to merge.",
		status: "unread",
		createdAt: "2026-07-21T10:30:00Z",
		updatedAt: "2026-07-21T10:30:00Z",
	},
	{
		id: "cntf_gone",
		source: "cloud",
		orgId: "org-1",
		projectId: "cproj-1",
		sessionId: "csess-gone",
		type: "review_feedback",
		title: "Review changes requested",
		body: "acme/cloud#2 has unresolved review feedback.",
		status: "read",
		createdAt: "2026-07-20T12:00:00Z",
		updatedAt: "2026-07-20T12:00:00Z",
	},
];

const cloudMarkAllReadMock = vi.fn();
const cloudMarkReadMock = vi.fn();
const cloudMarkUnreadMock = vi.fn();
const cloudSetDoneMock = vi.fn();
const cloudSnoozeMock = vi.fn();
const cloudRefetchMock = vi.fn();
const cloudClearAllMock = vi.fn();
const cloudClearOneMock = vi.fn();

/** What useCloudNotifications answers on a control plane from before team notifications. */
function cloudNotificationsResult(items: CloudCpNotification[]) {
	const needsYou = items.filter(isLegacyCloudNeedsYou);
	return {
		mode: "legacy" as const,
		needsYou,
		updates: items.filter((item) => !isLegacyCloudNeedsYou(item)),
		all: items,
		needsYouCount: needsYou.length,
		unreadCount: items.filter((item) => item.status === "unread").length,
		isLoading: false,
		isError: false,
		refetch: cloudRefetchMock,
		markRead: cloudMarkReadMock,
		markUnread: cloudMarkUnreadMock,
		setDone: cloudSetDoneMock,
		snooze: cloudSnoozeMock,
		markAllRead: cloudMarkAllReadMock,
		clearAll: cloudClearAllMock,
		clearOne: cloudClearOneMock,
	};
}

const emptyCloudResult = cloudNotificationsResult([]);

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock, useParams: () => paramsMock() }));

vi.mock("../hooks/useNotificationsQuery", () => ({
	useClearAllNotificationsMutation: () => ({ isPending: false, mutateAsync: clearAllMock }),
	useClearNotificationMutation: () => ({ isPending: false, mutateAsync: clearOneMock, variables: undefined }),
	useMarkAllNotificationsReadMutation: () => ({ isPending: false, mutateAsync: markAllMock }),
	useNotificationsQuery: (status: NotificationListStatus, enabled?: boolean) => notificationQueryMock(status, enabled),
}));

vi.mock("../hooks/useCloudNotifications", () => ({
	useCloudNotifications: () => cloudNotificationsMock(),
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { decideChatApproval: decideChatApprovalMock }, ready: true, baseUrl: "https://cloud.test", userId: "user-1" }),
}));

vi.mock("../hooks/useRestoreSession", () => ({
	useRestoreSession: () => restoreSessionMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => workspaceQueryMock(),
	useRemoteWorkspaces: () => ({ data: [], failedHostIds: [], loadedProjectHostIds: [], loadedSessionHostIds: [], refetch: vi.fn() }),
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("../lib/notifications", async (importOriginal) => ({
	...((await importOriginal()) as object),
	createNotificationsTransport: (...args: unknown[]) => {
		connectMock(...args);
		return { connect: () => undefined };
	},
}));

function renderNotificationCenter(
	queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<NotificationCenter />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

function rowOf(title: string) {
	return screen.getByText(title).closest<HTMLElement>("[role=listitem]")!;
}

function swipeRight(element: HTMLElement) {
	for (const deltaX of [-20, -40, -60]) element.dispatchEvent(new WheelEvent("wheel", { deltaX, deltaMode: WheelEvent.DOM_DELTA_PIXEL }));
}

/** Opens the bell on the All tab, where every row shows whatever it blocks. */
async function clickOpen(tab: "Needs you" | "Updates" | "All" | null = "All") {
	const trigger = screen.getByRole("button", { name: /unread notifications/ });
	await userEvent.click(trigger);
	const panel = await screen.findByRole("dialog", { name: "Notifications" });
	if (tab) await userEvent.click(within(panel).getByRole("tab", { name: new RegExp(`^${tab}`) }));
	return trigger;
}

function notificationQueryResult(
	status: NotificationListStatus,
	overrides: Partial<{
		hasNextPage: boolean;
		isError: boolean;
		isFetchNextPageError: boolean;
		isFetchingNextPage: boolean;
		isLoading: boolean;
	}> = {},
) {
	const hasNextPage = overrides.hasNextPage ?? false;
	const notifications = status === "unread" ? unreadNotifications : status === "all" ? allNotifications : [];
	return {
		data: {
			pageParams: [""],
			pages: [
				{
					notifications,
					nextCursor: hasNextPage ? "older" : undefined,
					unreadCount: unreadNotifications.length,
					unresolvedCount: 2,
				},
			],
		},
		fetchNextPage: fetchNextPageMock,
		hasNextPage,
		isError: false,
		isFetchNextPageError: false,
		isFetchingNextPage: false,
		isLoading: false,
		...overrides,
	};
}

const stableUnreadQuery = notificationQueryResult("unread");
const stableAllQuery = notificationQueryResult("all");

beforeEach(() => {
	clearAllMock
		.mockReset()
		.mockResolvedValue({ clearId: "clear-1", clearEpoch: "epoch-1", clearSequence: 1, clearedCount: 4 });
	clearOneMock.mockReset().mockResolvedValue(allNotifications[0]);
	cloudMarkAllReadMock.mockReset().mockResolvedValue(undefined);
	cloudMarkReadMock.mockReset().mockResolvedValue(undefined);
	cloudMarkUnreadMock.mockReset().mockResolvedValue(undefined);
	cloudSetDoneMock.mockReset().mockResolvedValue(undefined);
	cloudSnoozeMock.mockReset().mockResolvedValue(undefined);
	cloudRefetchMock.mockReset().mockResolvedValue(undefined);
	decideChatApprovalMock.mockReset().mockResolvedValue({ ok: true });
	cloudClearAllMock.mockReset().mockResolvedValue(undefined);
	cloudClearOneMock.mockReset().mockResolvedValue(undefined);
	cloudNotificationsMock.mockReset().mockReturnValue(emptyCloudResult);
	connectMock.mockReset();
	paramsMock.mockReset().mockReturnValue({});
	useUiStore.setState({ visibleTerminalKindBySession: {} });
	fetchNextPageMock.mockReset().mockResolvedValue(undefined);
	markAllMock.mockReset().mockResolvedValue(0);
	navigateMock.mockReset();
	restoreSessionMock.mockReset().mockResolvedValue({ status: "success" });
	workspaceQueryMock.mockReset().mockReturnValue({
		data: [
			{
				id: "proj-1",
				name: "acme/app",
				sessions: [
					{ id: "sess-1", isTerminated: false, status: "needs_input", title: "Checkout flow" },
					{ id: "sess-2", isTerminated: false, status: "ready_to_merge", title: "Checkout flow" },
					{ id: "sess-4", isTerminated: false, status: "needs_input", title: "Docs sweep" },
					{ id: "sess-dead", isTerminated: true, status: "terminated", title: "Old PR" },
				],
			},
		],
		isError: false,
		isPending: false,
		isSuccess: true,
		refetch: vi.fn(),
	});
	notificationQueryMock.mockReset().mockImplementation((status: NotificationListStatus) =>
		status === "unread" ? stableUnreadQuery : status === "all" ? stableAllQuery : notificationQueryResult(status),
	);
	vi.spyOn(window, "open").mockImplementation(() => null);
});

// The runtime tells the transport which session the user is actually watching.
// Being on the session route is not enough: the pane shows one terminal at a
// time, so a shell or reviewer tab hides the agent while the URL is unchanged.
describe("NotificationRuntime", () => {
	function renderRuntime() {
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={queryClient}>
				<NotificationRuntime />
			</QueryClientProvider>,
		);
		return connectMock.mock.calls[0][1] as () => string | undefined;
	}

	it("reports the session while its agent terminal is the one on screen", () => {
		paramsMock.mockReturnValue({ sessionId: "sess-1" });
		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": "worker" } });

		expect(renderRuntime()()).toBe("sess-1");
	});

	it.each(["shell", "reviewer"] as const)("reports nothing while a %s terminal covers the agent", (kind) => {
		paramsMock.mockReturnValue({ sessionId: "sess-1" });
		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": kind } });

		expect(renderRuntime()()).toBeUndefined();
	});

	it("reports nothing off a session route", () => {
		paramsMock.mockReturnValue({});
		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": "worker" } });

		expect(renderRuntime()()).toBeUndefined();
	});

	// The transport connects once and outlives navigation, so the getter has to
	// read live state rather than close over the value it was created with.
	it("tracks tab switches without reconnecting the stream", () => {
		paramsMock.mockReturnValue({ sessionId: "sess-1" });
		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": "worker" } });
		const getVisibleAgentSessionId = renderRuntime();

		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": "shell" } });
		expect(getVisibleAgentSessionId()).toBeUndefined();

		useUiStore.setState({ visibleTerminalKindBySession: { "sess-1": "worker" } });
		expect(getVisibleAgentSessionId()).toBe("sess-1");
		expect(connectMock).toHaveBeenCalledTimes(1);
	});
});

describe("NotificationCenter", () => {
	it("subscribes to workspace metadata only while the panel is open", async () => {
		renderNotificationCenter();

		expect(workspaceQueryMock).not.toHaveBeenCalled();
		await clickOpen(null);
		expect(workspaceQueryMock).toHaveBeenCalledTimes(1);
	});

	it("uses the compact topbar bell and unread badge sizing", () => {
		renderNotificationCenter();
		const trigger = screen.getByRole("button", { name: /unread notifications/ });
		const bell = trigger.querySelector("svg");
		const badge = trigger.querySelector("span:not([data-nav-row-highlight])");

		expect(bell?.classList.contains("size-icon-base")).toBe(true);
		expect(badge?.classList.contains("h-3")).toBe(true);
		expect(badge?.classList.contains("min-w-3")).toBe(true);
		expect(badge?.classList.contains("text-[7px]")).toBe(true);
	});

	it("opens once on click without a hover/focus remount and dismisses outside", async () => {
		renderNotificationCenter();
		const trigger = screen.getByRole("button", { name: /unread notifications/ });
		fireEvent.mouseEnter(trigger);
		fireEvent.focus(trigger);
		expect(screen.queryByRole("dialog", { name: "Notifications" })).not.toBeInTheDocument();

		await clickOpen();

		expect(screen.queryByText(/last 7 days/i)).not.toBeInTheDocument();
		fireEvent.pointerDown(document.body);
		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Notifications" })).not.toBeInTheDocument());
	});

	it("supports tab navigation inside the panel and restores focus to the bell", async () => {
		renderNotificationCenter();
		const trigger = screen.getByRole("button", { name: /unread notifications/ });
		trigger.focus();
		await userEvent.keyboard("{Enter}");

		const panel = await screen.findByRole("dialog", { name: "Notifications" });
		expect(panel).toContainElement(document.activeElement as HTMLElement | null);
		await userEvent.keyboard("{Escape}");
		await waitFor(() => expect(trigger).toHaveFocus());
	});

	it("lists every notification under All, beside Needs you and Updates, with no unread or unresolved tabs", async () => {
		renderNotificationCenter();
		await clickOpen();

		const panel = within(screen.getByRole("dialog", { name: "Notifications" }));
		expect(panel.getByRole("list")).toHaveClass("board-scrollbar", "overflow-y-auto");
		expect(panel.getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Needs you3", "Updates", "All"]);
		expect(panel.queryByRole("tab", { name: "Unread" })).not.toBeInTheDocument();
		expect(panel.queryByText("Unseen")).not.toBeInTheDocument();
		expect(panel.queryByText("Unresolved")).not.toBeInTheDocument();

		const rows = panel.getAllByRole("listitem");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringContaining("Fix checkout totals · PR #67"),
			expect.stringContaining("Checkout flow needs input"),
			expect.stringContaining("Docs sweep needs input"),
			expect.stringContaining("PR #9 merged"),
		]);
	});

	it("clears notification history from the panel header", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));

		expect(clearAllMock).toHaveBeenCalledTimes(1);
	});

	it("keeps the row's Open session tooltip off its action buttons", async () => {
		cloudNotificationsMock.mockReturnValue(cloudNotificationsResult(cloudNotifications));
		renderNotificationCenter();
		await clickOpen();

		const localClear = screen.getByRole("button", { name: "Clear notification: Checkout flow needs input" });
		expect(localClear.closest("[title='Open session']")).not.toBeNull();
		expect(localClear).toHaveAttribute("title", "");
		expect(screen.getByRole("button", { name: "Clear notification: Pull request ready to merge" })).toHaveAttribute("title", "");
		expect(screen.getByRole("button", { name: "This session was archived. Restore it?" })).toHaveAttribute("title", "");
	});

	it("clears one notification without opening its session", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear notification: Checkout flow needs input" }));

		expect(clearOneMock).toHaveBeenCalledWith(expect.objectContaining({ id: "ntf_1" }));
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("allows other notifications to clear while one delete is pending", async () => {
		let resolveFirst: (notification: NotificationDTO) => void = () => undefined;
		let resolveSecond: (notification: NotificationDTO) => void = () => undefined;
		const firstDelete = new Promise<NotificationDTO>((resolve) => {
			resolveFirst = resolve;
		});
		const secondDelete = new Promise<NotificationDTO>((resolve) => {
			resolveSecond = resolve;
		});
		clearOneMock.mockReturnValueOnce(firstDelete).mockReturnValueOnce(secondDelete);
		renderNotificationCenter();
		await clickOpen();

		const first = screen.getByRole("button", { name: "Clear notification: Checkout flow needs input" });
		const second = screen.getByRole("button", { name: "Clear notification: Docs sweep needs input" });
		await userEvent.click(first);

		expect(first).toBeDisabled();
		expect(second).toBeEnabled();
		await userEvent.click(second);
		expect(clearOneMock).toHaveBeenCalledTimes(2);
		expect(first).toBeDisabled();
		expect(second).toBeDisabled();

		resolveSecond(allNotifications[2]);
		await waitFor(() => expect(second).toBeEnabled());
		expect(first).toBeDisabled();

		resolveFirst(allNotifications[1]);
		await waitFor(() => expect(first).toBeEnabled());
	});

	it("keeps the row visible and reports a failed single clear", async () => {
		clearOneMock.mockRejectedValueOnce(new Error("single clear failed"));
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear notification: Checkout flow needs input" }));

		expect(await screen.findByText("single clear failed")).toBeInTheDocument();
		expect(screen.getByText("Checkout flow needs input")).toBeInTheDocument();
	});

	it("clears a notification swiped right without opening its session", async () => {
		renderNotificationCenter();
		await clickOpen();

		swipeRight(rowOf("Checkout flow needs input"));

		await waitFor(() => expect(clearOneMock).toHaveBeenCalledWith(expect.objectContaining({ id: "ntf_1" })));
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("brings a swiped row back when its clear fails", async () => {
		clearOneMock.mockRejectedValueOnce(new Error("single clear failed"));
		renderNotificationCenter();
		await clickOpen();
		const row = rowOf("Checkout flow needs input");

		swipeRight(row);

		expect(await screen.findByText("single clear failed")).toBeInTheDocument();
		await waitFor(() => expect(row.style.height).toBe(""));
		expect(row.style.opacity).toBe("");
		expect(row.style.transform).toBe("");
	});

	it("does not open the session when a row drag ends", async () => {
		renderNotificationCenter();
		await clickOpen();
		const body = screen.getByText("The agent is waiting for your response.");

		fireEvent.pointerDown(body, { pointerId: 1, clientX: 10, clientY: 10 });
		fireEvent.pointerMove(window, { pointerId: 1, clientX: 40, clientY: 10 });
		fireEvent.pointerUp(window, { pointerId: 1, clientX: 40, clientY: 10 });
		fireEvent.click(body);

		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("keeps the panel contents when clear-all fails", async () => {
		clearAllMock.mockRejectedValueOnce(new Error("clear failed"));
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));

		expect(await screen.findByText("clear failed")).toBeInTheDocument();
		expect(screen.getByText("Checkout flow needs input")).toBeInTheDocument();
	});

	// Opening acknowledges loaded unread ids only, so later unread pages stay
	// fetchable and highlighted when they appear in the all-list.
	it("acknowledges loaded unread ids on open and keeps showing them highlighted", async () => {
		renderNotificationCenter();
		await clickOpen();

		expect(markAllMock.mock.calls[0][0]).toEqual(expect.arrayContaining(["ntf_1", "ntf_2"]));
		expect(markAllMock.mock.calls[0][0]).toHaveLength(2);
		expect(screen.queryByRole("button", { name: "Mark all notifications read" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Mark notification read" })).not.toBeInTheDocument();
		expect(screen.getByText("Checkout flow needs input")).toBeInTheDocument();
		expect(screen.getByText("Checkout flow needs input").className).toContain("font-medium");
		expect(screen.getByLabelText("Fix checkout totals · PR #67").className).toContain("font-medium");
		expect(screen.getByText("Docs sweep needs input").className).not.toContain("font-medium");
	});

	it("retries mark-read after a transient failure without closing the panel", async () => {
		markAllMock.mockRejectedValueOnce(new Error("network down")).mockResolvedValueOnce(2);
		renderNotificationCenter();
		await clickOpen();

		expect(await screen.findByText("network down")).toBeInTheDocument();
		expect(markAllMock).toHaveBeenCalledTimes(1);

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));

		await waitFor(() => expect(markAllMock).toHaveBeenCalledTimes(2));
		expect(markAllMock.mock.calls[1][0]).toEqual(expect.arrayContaining(["ntf_1", "ntf_2"]));
		expect(markAllMock.mock.calls[1][0]).toHaveLength(2);
		await waitFor(() => expect(screen.queryByText("network down")).not.toBeInTheDocument());
		expect(screen.getByText("Checkout flow needs input").className).toContain("font-medium");
	});

	it("keeps later unread pages highlighted when they load after open", async () => {
		const laterUnread: NotificationDTO = {
			id: "ntf_later",
			sessionId: "sess-1",
			projectId: "proj-1",
			prUrl: "",
			type: "needs_input",
			title: "Later unread needs input",
			body: "Still waiting after the first page.",
			status: "unread",
			createdAt: "2026-07-18T09:00:00Z",
			target: { kind: "session", sessionId: "sess-1" },
		};
		const allState = {
			pages: [
				{
					notifications: allNotifications,
					nextCursor: "older" as string | undefined,
					unreadCount: 3,
					unresolvedCount: 2,
				},
			],
		};
		fetchNextPageMock.mockImplementation(async () => {
			allState.pages = [
				{
					notifications: allNotifications,
					nextCursor: "older",
					unreadCount: 3,
					unresolvedCount: 2,
				},
				{
					notifications: [laterUnread],
					nextCursor: undefined,
					unreadCount: 3,
					unresolvedCount: 2,
				},
			];
		});
		// Unread cache only has the first page; unreadCount still includes the
		// later row that will arrive through the all list.
		const unreadPage = {
			notifications: unreadNotifications,
			nextCursor: "older-unread",
			unreadCount: 3,
			unresolvedCount: 2,
		};
		const unreadQuery = {
			...notificationQueryResult("unread", { hasNextPage: true }),
			data: {
				pageParams: [""],
				pages: [unreadPage],
			},
		};
		// Mirror mutation onSuccess: decrement the badge from updatedCount even
		// when the acknowledged id is absent from the unread cache.
		markAllMock.mockImplementation(async (ids: string[]) => {
			unreadPage.unreadCount = Math.max(0, unreadPage.unreadCount - ids.length);
			unreadPage.notifications = unreadPage.notifications.map((item) =>
				ids.includes(item.id) && item.status === "unread" ? { ...item, status: "read" as const } : item,
			);
			return ids.length;
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		notificationQueryMock.mockImplementation((status: NotificationListStatus) => {
			if (status === "unread") return unreadQuery;
			return {
				data: {
					pageParams: allState.pages.map((_, index) => (index === 0 ? "" : "older")),
					pages: allState.pages,
				},
				fetchNextPage: fetchNextPageMock,
				hasNextPage: allState.pages.length < 2,
				isError: false,
				isFetchNextPageError: false,
				isFetchingNextPage: false,
				isLoading: false,
			};
		});

		const view = render(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<NotificationCenter />
				</TooltipProvider>
			</QueryClientProvider>,
		);
		await clickOpen();
		expect(markAllMock.mock.calls[0][0]).toEqual(expect.arrayContaining(["ntf_1", "ntf_2"]));
		expect(markAllMock.mock.calls[0][0]).toHaveLength(2);
		await waitFor(() => expect(screen.getByRole("button", { name: "1 unread notifications" })).toBeInTheDocument());
		markAllMock.mockClear();

		const list = screen.getByRole("list");
		Object.defineProperties(list, {
			clientHeight: { configurable: true, value: 420 },
			scrollHeight: { configurable: true, value: 600 },
			scrollTop: { configurable: true, value: 130 },
		});
		fireEvent.scroll(list);
		await waitFor(() => expect(fetchNextPageMock).toHaveBeenCalled());

		view.rerender(
			<QueryClientProvider client={queryClient}>
				<TooltipProvider>
					<NotificationCenter />
				</TooltipProvider>
			</QueryClientProvider>,
		);

		expect(await screen.findByText("Later unread needs input")).toHaveClass("font-medium");
		await waitFor(() => expect(markAllMock).toHaveBeenCalledWith(["ntf_later"]));
		await waitFor(() => expect(screen.getByRole("button", { name: "Notifications" })).toBeInTheDocument());
		expect(screen.queryByRole("button", { name: /unread notifications/ })).not.toBeInTheDocument();
	});

	it("surfaces a failed all-list load instead of claiming success", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "all"
				? { ...notificationQueryResult(status, { isError: true }), data: undefined }
				: notificationQueryResult(status),
		);
		renderNotificationCenter();
		await userEvent.click(screen.getByRole("button", { name: /notifications/i }));

		expect(await screen.findByText("Could not load notifications.")).toBeInTheDocument();
		expect(screen.queryByText("No notifications yet.")).not.toBeInTheDocument();
	});

	it("shows the empty state when the all list loaded with nothing", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) => ({
			...notificationQueryResult(status),
			data: { pageParams: [""], pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }] },
		}));
		renderNotificationCenter();
		await userEvent.click(screen.getByRole("button", { name: /notifications/i }));

		// Nothing blocks the person, so the bell opens on Updates.
		expect(await screen.findByText("No updates yet.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Clear all" })).toBeDisabled();
		await userEvent.click(screen.getByRole("tab", { name: "All" }));
		expect(screen.getByText("No notifications yet.")).toBeInTheDocument();
	});

	it("surfaces a failed refresh over an unrelated cached empty page", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) => ({
			...notificationQueryResult(status, { isError: status === "all" }),
			data: { pageParams: [""], pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }] },
		}));
		renderNotificationCenter();
		await userEvent.click(screen.getByRole("button", { name: /notifications/i }));

		expect(await screen.findByText("Could not load notifications.")).toBeInTheDocument();
		expect(screen.queryByText("No notifications yet.")).not.toBeInTheDocument();
	});

	it("keeps the confirmed empty state when a background refresh fails after clear", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) => ({
			...notificationQueryResult(status, { isError: status === "all" }),
			data: { pageParams: [""], pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }] },
		}));
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		applyNotificationsCleared(queryClient, {
			clearId: "clear-1",
			clearEpoch: "epoch-1",
			clearSequence: 1,
		});
		renderNotificationCenter(queryClient);
		await userEvent.click(screen.getByRole("button", { name: /notifications/i }));
		await userEvent.click(await screen.findByRole("tab", { name: "All" }));

		expect(await screen.findByText("No notifications yet.")).toBeInTheDocument();
		expect(screen.queryByText("Could not load notifications.")).not.toBeInTheDocument();
	});

	it("navigates to the session from anywhere on the row, including the body text", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByText("The agent is waiting for your response."));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
		});
	});

	it("opens the session from the non-link portion of a PR row title", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByLabelText("Fix checkout totals · PR #67"));
		expect(window.open).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-2" },
		});
	});

	it("opens the linked PR number in the system browser without opening the session", async () => {
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("link", { name: "Open PR #67" }));

		expect(openExternal).toHaveBeenCalledWith("https://github.com/acme/app/pull/67");
		expect(navigateMock).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("lets Enter on the nested PR link act on the link without opening the session", async () => {
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderNotificationCenter();
		await clickOpen();

		screen.getByRole("link", { name: "Open PR #67" }).focus();
		await userEvent.keyboard("{Enter}");

		expect(openExternal).toHaveBeenCalledWith("https://github.com/acme/app/pull/67");
		expect(navigateMock).not.toHaveBeenCalled();
		openExternal.mockRestore();
	});

	it("shows the PR title prominently and does not repeat the session name in metadata", async () => {
		renderNotificationCenter();
		await clickOpen();

		const title = screen.getByLabelText("Fix checkout totals · PR #67");
		const row = title.closest('[role="listitem"]');
		expect(row).not.toBeNull();
		expect(row).toHaveTextContent("acme/app");
		expect(row?.textContent?.match(/Checkout flow/g)).toHaveLength(1);
	});

	it("normalizes legacy ready notifications without rewriting stored history", async () => {
		const legacyReady = {
			...allNotifications[0],
			title: "Checkout flow is ready to merge",
			body: "CI passed with no blocking review feedback.",
		};
		notificationQueryMock.mockImplementation((status: NotificationListStatus) => ({
			...notificationQueryResult(status),
			data: {
				pageParams: [""],
				pages: [
					{
						notifications: [legacyReady],
						unreadCount: 1,
						unresolvedCount: 1,
					},
				],
			},
		}));
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByLabelText("PR #67 is ready to merge")).toBeInTheDocument();
		expect(
			screen.getByText(
				"PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback.",
			),
		).toBeInTheDocument();
		expect(screen.queryByText("Checkout flow is ready to merge")).not.toBeInTheDocument();
	});

	it("opens the session with the keyboard from a focused row", async () => {
		renderNotificationCenter();
		await clickOpen();

		const row = within(screen.getByRole("dialog", { name: "Notifications" })).getAllByRole("button", {
			name: /Checkout flow needs input/,
		})[0];
		row.focus();
		await userEvent.keyboard("{Enter}");

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-1" },
		});
	});

	it("does not open terminated sessions from the row and restores them instead", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByText("The agent session has ended."));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(restoreSessionMock).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("button", { name: "This session was archived. Restore it?" }));
		await waitFor(() => expect(restoreSessionMock).toHaveBeenCalledWith("sess-dead"));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-dead" },
		});
	});

	// A PR outcome describes work that already finished — there's no paused
	// agent to resume, so a terminated session behind one should stay directly
	// viewable instead of gated behind restore.
	it("opens a terminated session directly for a PR-outcome notification, with no restore option", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "all"
				? {
						...notificationQueryResult(status),
						data: {
							pageParams: [""],
							pages: [
								{
									notifications: [
										{
											id: "ntf_dead_pr",
											sessionId: "sess-dead",
											projectId: "proj-1",
											prUrl: "https://github.com/acme/app/pull/9",
											type: "pr_merged",
											title: "PR #9 merged",
											body: "The agent session has ended.",
											status: "read",
											createdAt: "2026-07-19T09:00:00Z",
											target: { kind: "session", sessionId: "sess-dead" },
										},
									],
									unreadCount: 0,
									unresolvedCount: 0,
								},
							],
						},
					}
				: notificationQueryResult(status),
		);
		renderNotificationCenter();
		await clickOpen();

		expect(screen.queryByRole("button", { name: "This session was archived. Restore it?" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByText("The agent session has ended."));
		expect(restoreSessionMock).not.toHaveBeenCalled();
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj-1", sessionId: "sess-dead" },
		});
	});

	it("does not open sessions while workspace facts are still loading", async () => {
		workspaceQueryMock.mockReturnValue({
			data: undefined,
			isError: false,
			isPending: true,
			isSuccess: false,
			refetch: vi.fn(),
		});
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByText("The agent is waiting for your response."));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "This session was archived. Restore it?" })).not.toBeInTheDocument();
	});

	it("does not treat terminated sessions as live when the workspace query fails", async () => {
		const refetch = vi.fn();
		workspaceQueryMock.mockReturnValue({
			data: undefined,
			isError: true,
			isPending: false,
			isSuccess: false,
			refetch,
		});
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByText("Could not load sessions. Restore and open may be unavailable.")).toBeInTheDocument();

		await userEvent.click(screen.getByText("The agent is waiting for your response."));
		expect(navigateMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByText("The agent session has ended."));
		expect(navigateMock).not.toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "This session was archived. Restore it?" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(refetch).toHaveBeenCalledTimes(1);
	});

	it("loads earlier history near the end of the scroll viewport", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			notificationQueryResult(status, { hasNextPage: true }),
		);
		renderNotificationCenter();
		await clickOpen();

		const list = screen.getByRole("list");
		Object.defineProperties(list, {
			clientHeight: { configurable: true, value: 420 },
			scrollHeight: { configurable: true, value: 600 },
			scrollTop: { configurable: true, value: 130 },
		});
		fireEvent.scroll(list);

		expect(fetchNextPageMock).toHaveBeenCalledTimes(1);
	});

	it("offers a retry when loading earlier notifications fails", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			notificationQueryResult(status, {
				hasNextPage: true,
				isError: true,
				isFetchNextPageError: true,
			}),
		);
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByText("Couldn’t load earlier notifications.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(fetchNextPageMock).toHaveBeenCalledTimes(1);
	});

	it("shows the full notification body instead of clamping it", async () => {
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByText("The agent is waiting for your response.")).not.toHaveClass("line-clamp-2");
	});
});

describe("NotificationCenter cloud rows", () => {
	const cloudResult = cloudNotificationsResult(cloudNotifications);

	beforeEach(() => {
		cloudNotificationsMock.mockReturnValue(cloudResult);
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj-1",
					name: "acme/app",
					sessions: [
						{ id: "sess-1", isTerminated: false, status: "needs_input", title: "Checkout flow" },
						{ id: "sess-2", isTerminated: false, status: "ready_to_merge", title: "Checkout flow" },
						{ id: "sess-4", isTerminated: false, status: "needs_input", title: "Docs sweep" },
						{ id: "sess-dead", isTerminated: true, status: "terminated", title: "Old PR" },
					],
				},
				{
					id: "cproj-1",
					name: "acme/cloud",
					kind: "cloud",
					sessions: [{ id: "csess-1", isTerminated: false, status: "working", title: "Cloud checkout" }],
				},
			],
			isError: false,
			isPending: false,
			isSuccess: true,
			refetch: vi.fn(),
		});
	});

	it("interleaves cloud rows by time with a Cloud tag, meta line, and clear action", async () => {
		renderNotificationCenter();
		await clickOpen();

		const rows = within(screen.getByRole("list")).getAllByRole("listitem");
		expect(rows.map((row) => row.getAttribute("data-notification-source") ?? "local")).toEqual([
			"local",
			"cloud",
			"local",
			"cloud",
			"local",
			"local",
		]);
		const cloudRow = rows[1];
		expect(within(cloudRow).getByText("Cloud")).toBeInTheDocument();
		expect(within(cloudRow).getByText("acme/cloud")).toBeInTheDocument();
		expect(within(cloudRow).getByText("Cloud checkout")).toBeInTheDocument();
		expect(within(cloudRow).getByRole("button", { name: "Clear notification: Pull request ready to merge" })).toBeInTheDocument();
	});

	it("opens the cloud session through its project route", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByText("acme/cloud#8 is ready to merge."));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "cproj-1", sessionId: "csess-1" },
		});
	});

	it("does not open a cloud row whose session is not in the workspace", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByText("acme/cloud#2 has unresolved review feedback."));
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("clears one cloud row without opening its session", async () => {
		renderNotificationCenter();
		await clickOpen();

		const cloudRow = screen.getByText("acme/cloud#8 is ready to merge.").closest("[role='listitem']") as HTMLElement;
		await userEvent.click(within(cloudRow).getByRole("button", { name: /^Clear notification/ }));
		expect(cloudClearOneMock).toHaveBeenCalledWith(cloudNotifications[0]);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("clears local and cloud notifications together", async () => {
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
		expect(clearAllMock).toHaveBeenCalledTimes(1);
		expect(cloudClearAllMock).toHaveBeenCalledTimes(1);
	});

	it("acknowledges exactly the loaded unread cloud rows when the panel opens", async () => {
		renderNotificationCenter();
		await clickOpen();

		await waitFor(() => expect(cloudMarkReadMock).toHaveBeenCalledWith(["cntf_ready"]));
		expect(cloudMarkReadMock).toHaveBeenCalledTimes(1);
		expect(cloudMarkAllReadMock).not.toHaveBeenCalled();
	});

	it("counts cloud unread notifications in the bell badge", () => {
		renderNotificationCenter();

		expect(screen.getByRole("button", { name: `${unreadNotifications.length + 1} unread notifications` })).toBeInTheDocument();
	});

	it("still clears local history when it failed to load", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "all"
				? { ...notificationQueryResult(status, { isError: true }), data: undefined, isSuccess: false, refetch: vi.fn() }
				: notificationQueryResult(status),
		);
		renderNotificationCenter();
		await clickOpen();

		await userEvent.click(screen.getByRole("button", { name: "Clear all" }));
		expect(clearAllMock).toHaveBeenCalledTimes(1);
		expect(cloudClearAllMock).toHaveBeenCalledTimes(1);
	});

	describe("with a cloud row older than all loaded local history", () => {
		const olderCloud: CloudCpNotification = {
			...cloudNotifications[1],
			id: "cntf_old",
			body: "acme/cloud#1 was merged.",
			type: "pr_merged",
			createdAt: "2026-07-18T09:00:00Z",
			updatedAt: "2026-07-18T09:00:00Z",
		};
		const withOlder = cloudNotificationsResult([...cloudNotifications, olderCloud]);

		it("holds it back while older local pages remain", async () => {
			cloudNotificationsMock.mockReturnValue(withOlder);
			notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
				status === "all" ? notificationQueryResult(status, { hasNextPage: true }) : notificationQueryResult(status),
			);
			renderNotificationCenter();
			await clickOpen();

			expect(screen.getByText("acme/cloud#2 has unresolved review feedback.")).toBeInTheDocument();
			expect(screen.queryByText("acme/cloud#1 was merged.")).not.toBeInTheDocument();
		});

		it("shows it once local history is exhausted", async () => {
			cloudNotificationsMock.mockReturnValue(withOlder);
			renderNotificationCenter();
			await clickOpen();

			const rows = within(screen.getByRole("list")).getAllByRole("listitem");
			expect(within(rows[rows.length - 1]).getByText("acme/cloud#1 was merged.")).toBeInTheDocument();
		});
	});

	it("waits for local history before showing cloud rows", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "all"
				? { ...notificationQueryResult(status, { isLoading: true }), data: undefined }
				: notificationQueryResult(status),
		);
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByText("Loading notifications…")).toBeInTheDocument();
		expect(screen.queryByText("acme/cloud#8 is ready to merge.")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Clear all" })).toBeDisabled();
	});

	it("keeps a local load failure visible next to cloud rows", async () => {
		const refetch = vi.fn();
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "all"
				? { ...notificationQueryResult(status, { isError: true }), data: undefined, refetch }
				: notificationQueryResult(status),
		);
		renderNotificationCenter();
		await clickOpen();

		expect(screen.getByText("Could not load notifications.")).toBeInTheDocument();
		expect(screen.getByText("acme/cloud#8 is ready to merge.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(refetch).toHaveBeenCalledTimes(1);
	});
});

describe("NotificationCenter on a control plane with team notifications", () => {
	const approval: CloudCpNotification = {
		id: "cn-approve", source: "cloud", orgId: "org-2", projectId: "cproj-1", sessionId: "csess-1",
		type: "approval_needed", tier: 0, title: "Cloud checkout needs approval", body: "pnpm prisma migrate dev",
		status: "unread", reason: "owner", actor: { userId: "u2", name: "Ben Ortiz" },
		metadata: {
			requestId: "req-1",
			decisions: [
				{ id: "always", label: "Always", kind: "allow_always" },
				{ id: "allow", label: "Allow", kind: "allow_once" },
				{ id: "reject", label: "Reject", kind: "reject_once" },
			],
		},
		createdAt: "2026-07-21T11:00:00Z", updatedAt: "2026-07-21T11:00:00Z",
	};
	const handled: CloudCpNotification = {
		id: "cn-handled", source: "cloud", orgId: "org-2", projectId: "cproj-1", sessionId: "csess-1",
		type: "approval_needed", tier: 0, title: "Cloud checkout needed approval", body: "pnpm test", status: "read",
		resolvedAt: "2026-07-21T10:05:00Z", resolution: "handled", resolvedBy: { userId: "u2", name: "Ben Ortiz" },
		createdAt: "2026-07-21T10:00:00Z", updatedAt: "2026-07-21T10:05:00Z",
	};
	const cleared: CloudCpNotification = { ...handled, id: "cn-cleared", title: "Old approval", resolution: "cleared", resolvedBy: undefined, createdAt: "2026-07-21T09:00:00Z" };
	const merged: CloudCpNotification = {
		id: "cn-merged", source: "cloud", orgId: "org-2", projectId: "cproj-1", sessionId: "csess-1",
		type: "pr_merged", tier: 2, title: "PR #12 merged", body: "Ben merged it.", status: "unread", reason: "following",
		createdAt: "2026-07-21T12:00:00Z", updatedAt: "2026-07-21T12:00:00Z",
	};

	function inboxResult(needsYou: CloudCpNotification[], updates: CloudCpNotification[], needsYouCount = needsYou.length) {
		const all = [...needsYou, ...updates].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
		return {
			...cloudNotificationsResult(all),
			mode: "inbox" as const,
			needsYou,
			updates,
			all,
			needsYouCount,
		};
	}

	const emptyLocal = (status: NotificationListStatus) => ({
		...notificationQueryResult(status),
		data: { pageParams: [""], pages: [{ notifications: [], unreadCount: 0, unresolvedCount: 0 }] },
	});

	beforeEach(() => {
		notificationQueryMock.mockImplementation(emptyLocal);
		cloudNotificationsMock.mockReturnValue(inboxResult([approval], [merged, handled, cleared], 2));
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "cproj-1", name: "acme/cloud", kind: "cloud", sessions: [{ id: "csess-1", isTerminated: false, status: "working", title: "Cloud checkout" }] }],
			isError: false, isPending: false, isSuccess: true, refetch: vi.fn(),
		});
	});

	async function openBell() {
		await userEvent.click(screen.getByRole("button", { name: /notifications/i }));
		return within(await screen.findByRole("dialog", { name: "Notifications" }));
	}

	it("badges the bell with what blocks the person, not every unread row", () => {
		renderNotificationCenter();
		expect(screen.getByRole("button", { name: "2 unread notifications" })).toBeInTheDocument();
	});

	it("opens on Needs you with the server's count and moves resolved rows to Updates", async () => {
		renderNotificationCenter();
		const panel = await openBell();

		expect(panel.getByRole("tab", { name: /^Needs you/ })).toHaveAttribute("aria-selected", "true");
		expect(panel.getByRole("tab", { name: /^Needs you/ })).toHaveTextContent("Needs you2");
		expect(panel.getAllByRole("listitem").map((row) => row.textContent)).toEqual([expect.stringContaining("Cloud checkout needs approval")]);

		await userEvent.click(panel.getByRole("tab", { name: /^Updates/ }));
		expect(panel.getByText("Handled by Ben Ortiz")).toBeInTheDocument();
		expect(panel.getByText("No longer needed")).toBeInTheDocument();
		expect(panel.queryByText("Cloud checkout needs approval")).not.toBeInTheDocument();

		await userEvent.click(panel.getByRole("tab", { name: "All" }));
		expect(panel.getAllByRole("listitem")).toHaveLength(4);
	});

	it("keeps why and who whole in a row's footer and leaves out a session name that repeats its project", async () => {
		const orchestrator: CloudCpNotification = {
			...merged, id: "cn-orch", sessionId: "csess-orch", title: "Orchestrator opened a pull request",
			reason: "owner", actor: { userId: "u2", name: "Ben Ortiz" },
		};
		cloudNotificationsMock.mockReturnValue(inboxResult([], [orchestrator]));
		workspaceQueryMock.mockReturnValue({
			data: [{ id: "cproj-1", name: "acme/cloud", kind: "cloud", sessions: [{ id: "csess-orch", isTerminated: false, status: "idle", title: "acme/cloud" }] }],
			isError: false, isPending: false, isSuccess: true, refetch: vi.fn(),
		});
		renderNotificationCenter();
		const panel = await openBell();

		const row = panel.getAllByRole("listitem")[0];
		expect(within(row).getAllByText("acme/cloud")).toHaveLength(1);
		expect(within(row).getByText("Owner")).toHaveClass("shrink-0");
		expect(within(row).getByText("Ben Ortiz")).toHaveClass("shrink-0");
		expect(within(row).getByText("acme/cloud")).toHaveClass("truncate");
	});

	it("opens on Updates when nothing blocks the person", async () => {
		cloudNotificationsMock.mockReturnValue(inboxResult([], [merged]));
		renderNotificationCenter();
		const panel = await openBell();

		expect(panel.getByRole("tab", { name: /^Updates/ })).toHaveAttribute("aria-selected", "true");
		expect(panel.getByText("PR #12 merged")).toBeInTheDocument();
	});

	it("reads nothing when the bell opens; Mark all read does", async () => {
		renderNotificationCenter();
		const panel = await openBell();

		await waitFor(() => expect(panel.getAllByRole("listitem")).toHaveLength(1));
		expect(cloudMarkReadMock).not.toHaveBeenCalled();
		expect(cloudMarkAllReadMock).not.toHaveBeenCalled();

		await userEvent.click(panel.getByRole("button", { name: "Mark all read" }));
		expect(cloudMarkAllReadMock).toHaveBeenCalledTimes(1);
	});

	it("says why the person got a row and who caused it", async () => {
		renderNotificationCenter();
		const panel = await openBell();
		const row = panel.getByText("Cloud checkout needs approval").closest<HTMLElement>("[role=listitem]")!;
		expect(within(row).getByText("Owner")).toBeInTheDocument();
		expect(within(row).getByText("Ben Ortiz")).toBeInTheDocument();
	});

	it("reads a row when it is clicked, and opens its session", async () => {
		renderNotificationCenter();
		const panel = await openBell();
		await userEvent.click(panel.getByRole("tab", { name: /^Updates/ }));

		await userEvent.click(panel.getByText("Ben merged it."));

		expect(cloudMarkReadMock).toHaveBeenCalledWith(["cn-merged"]);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "cproj-1", sessionId: "csess-1" } });
	});

	it("marks a row done, snoozes it, and marks it unread on the server", async () => {
		renderNotificationCenter();
		const panel = await openBell();
		await userEvent.click(panel.getByRole("tab", { name: /^Updates/ }));
		const mergedRow = panel.getByText("PR #12 merged").closest<HTMLElement>("[role=listitem]")!;
		const handledRow = panel.getByText("Cloud checkout needed approval").closest<HTMLElement>("[role=listitem]")!;

		await userEvent.click(within(mergedRow).getByRole("button", { name: "Done" }));
		expect(cloudClearOneMock).toHaveBeenCalledWith(merged);
		expect(navigateMock).not.toHaveBeenCalled();

		await userEvent.click(within(mergedRow).getByRole("button", { name: "Mark read" }));
		expect(cloudMarkReadMock).toHaveBeenCalledWith(["cn-merged"]);
		await userEvent.click(within(handledRow).getByRole("button", { name: "Mark unread" }));
		expect(cloudMarkUnreadMock).toHaveBeenCalledWith(["cn-handled"]);

		const before = Date.now();
		await userEvent.click(within(mergedRow).getByRole("button", { name: "Snooze" }));
		await userEvent.click(await screen.findByRole("menuitem", { name: "For 1 hour" }));
		expect(cloudSnoozeMock).toHaveBeenCalledWith(["cn-merged"], expect.any(Date));
		const until = (cloudSnoozeMock.mock.calls[0]?.[1] as Date).getTime();
		expect(until - before).toBeGreaterThanOrEqual(60 * 60 * 1000 - 1000);
		expect(until - before).toBeLessThanOrEqual(60 * 60 * 1000 + 5000);
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("answers an approval in place with the chat's choices", async () => {
		renderNotificationCenter();
		const panel = await openBell();
		const row = panel.getByText("Cloud checkout needs approval").closest<HTMLElement>("[role=listitem]")!;

		// The row itself is a button that opens the session; these are the actions in it.
		const actions = within(row).getAllByRole("button").filter((button) => !button.hasAttribute("role"));
		expect(actions.map((button) => button.textContent).filter((text) => text)).toEqual([
			"Deny", "Allow once", "Always allow this command", "Open",
		]);
		await userEvent.click(within(row).getByRole("button", { name: "Allow once" }));

		expect(decideChatApprovalMock).toHaveBeenCalledWith("org-2", "csess-1", "req-1", "allow");
		await waitFor(() => expect(cloudRefetchMock).toHaveBeenCalled());
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("keeps an approval it may not answer and says who can", async () => {
		decideChatApprovalMock.mockRejectedValue(new CloudCpError("Forbidden", { status: 403, code: "forbidden" }));
		renderNotificationCenter();
		const panel = await openBell();
		const row = panel.getByText("Cloud checkout needs approval").closest<HTMLElement>("[role=listitem]")!;

		await userEvent.click(within(row).getByRole("button", { name: "Deny" }));

		expect(await within(row).findByText("Only the person driving or the owner can answer this")).toBeInTheDocument();
		expect(panel.getByText("Cloud checkout needs approval")).toBeInTheDocument();
		expect(within(row).getByRole("button", { name: "Allow once" })).toBeEnabled();
	});

	it("clears every loaded cloud row on the server with Clear all", async () => {
		renderNotificationCenter();
		const panel = await openBell();
		await userEvent.click(panel.getByRole("button", { name: "Clear all" }));
		expect(cloudClearAllMock).toHaveBeenCalledTimes(1);
	});

	it("puts local questions in Needs you beside the cloud's", async () => {
		notificationQueryMock.mockImplementation((status: NotificationListStatus) =>
			status === "unread" ? stableUnreadQuery : status === "all" ? stableAllQuery : notificationQueryResult(status));
		renderNotificationCenter();
		const panel = await openBell();

		// Three unanswered local questions and the cloud's two blocking rows.
		expect(panel.getByRole("tab", { name: /^Needs you/ })).toHaveTextContent("Needs you5");
		expect(panel.getByText("Checkout flow needs input")).toBeInTheDocument();
		const readyBody = "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback.";
		expect(panel.queryByText(readyBody)).not.toBeInTheDocument();
		await userEvent.click(panel.getByRole("tab", { name: /^Updates/ }));
		expect(panel.getByText(readyBody)).toBeInTheDocument();
	});
});

describe("NotificationRuntime desktop notifications for cloud", () => {
	const row = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
		id: "cn-1", source: "cloud", orgId: "org-2", projectId: "cproj-1", sessionId: "csess-1", type: "approval_needed",
		title: "Cloud checkout needs approval", body: "pnpm test", status: "unread", tier: 0, desktop: true, eventId: "approval:req-1",
		createdAt: "2026-07-21T11:00:00Z", updatedAt: "2026-07-21T11:00:00Z", ...overrides,
	});
	const event = (notification: CloudCpNotification, kind: CloudCpNotificationEvent["kind"] = "notification_created"): CloudCpNotificationEvent => ({
		sequence: 1, orgId: notification.orgId, recipientUserId: "u1", kind, notification, createdAt: notification.updatedAt,
	});

	function renderRuntime() {
		render(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<NotificationRuntime />
			</QueryClientProvider>,
		);
	}

	let show: ReturnType<typeof vi.fn<typeof aoBridge.notifications.show>>;
	beforeEach(() => {
		show = vi.fn<typeof aoBridge.notifications.show>().mockResolvedValue(undefined);
		vi.spyOn(aoBridge.notifications, "show").mockImplementation(show);
		vi.spyOn(document, "hasFocus").mockReturnValue(false);
	});
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("shows a live blocking row through the desktop path, one per session", () => {
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(show).toHaveBeenCalledWith({
			id: "cloud-notification:cn-1",
			title: "Cloud checkout needs approval",
			body: "pnpm test",
			type: "approval_needed",
			watched: false,
			tag: "cloud-session:csess-1",
		});
	});

	it("skips rows the person doesn't want on the desktop, or that no longer wait on them", () => {
		renderRuntime();
		act(() => {
			publishLiveCloudNotificationEvent(event(row({ id: "a", desktop: false })));
			publishLiveCloudNotificationEvent(event(row({ id: "b", desktop: undefined })));
			publishLiveCloudNotificationEvent(event(row({ id: "c", status: "read" })));
			publishLiveCloudNotificationEvent(event(row({ id: "d", resolvedAt: "2026-07-21T11:05:00Z" })));
			publishLiveCloudNotificationEvent(event(row({ id: "e", doneAt: "2026-07-21T11:05:00Z" })));
			publishLiveCloudNotificationEvent(event(row({ id: "f", snoozedUntil: "2999-01-01T00:00:00Z" })));
			publishLiveCloudNotificationEvent(event(row({ id: "g" }), "notification_resolved"));
		});
		expect(show).not.toHaveBeenCalled();
	});

	it("notifies once per thing that raised a row, however often it changes", () => {
		renderRuntime();
		act(() => {
			publishLiveCloudNotificationEvent(event(row()));
			// Marked unread on another device: same source event.
			publishLiveCloudNotificationEvent(event(row({ updatedAt: "2026-07-21T11:10:00Z" }), "notification_updated"));
		});
		expect(show).toHaveBeenCalledTimes(1);
		// The approval moved on to the next request.
		act(() => publishLiveCloudNotificationEvent(event(row({ eventId: "approval:req-2" }), "notification_updated")));
		expect(show).toHaveBeenCalledTimes(2);
		expect(show.mock.calls.map(([payload]) => payload.tag)).toEqual(["cloud-session:csess-1", "cloud-session:csess-1"]);
	});

	it("marks the notification watched while that session is on screen in a focused window", () => {
		paramsMock.mockReturnValue({ sessionId: "csess-1" });
		vi.mocked(document.hasFocus).mockReturnValue(true);
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		expect(show).toHaveBeenCalledWith(expect.objectContaining({ watched: true }));
	});

	it("opens the cloud session when its desktop notification is clicked", () => {
		let click: (id: string) => void = () => undefined;
		vi.spyOn(aoBridge.notifications, "onClick").mockImplementation((listener) => {
			click = listener;
			return () => undefined;
		});
		renderRuntime();
		act(() => publishLiveCloudNotificationEvent(event(row())));
		act(() => click("cloud-notification:cn-1"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "cproj-1", sessionId: "csess-1" } });
	});

	it("adds the cloud's blocking count to the dock badge only on a control plane with team notifications", () => {
		const setBadge = vi.spyOn(aoBridge.notifications, "setBadge").mockResolvedValue(undefined);
		cloudNotificationsMock.mockReturnValue({ ...cloudNotificationsResult([]), mode: "inbox", needsYouCount: 4 });
		renderRuntime();
		// Two unread local rows plus four blocking cloud rows.
		expect(setBadge).toHaveBeenLastCalledWith(6);

		setBadge.mockClear();
		cloudNotificationsMock.mockReturnValue({ ...cloudNotificationsResult([cloudNotifications[0]]), mode: "legacy" });
		renderRuntime();
		expect(setBadge).toHaveBeenLastCalledWith(2);
	});
});
