import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpFeedAway, CloudCpFeedItem, CloudCpFeedQuery, CloudCpFeedResponse } from "../lib/cloud-cp/types";
import { recordTeamCollabSupport, resetTeamCollabSupport } from "../lib/cloud-team-collab";
import { useUiStore } from "../stores/ui-store";
import { TeamFeedView } from "./TeamFeedView";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	getMyFeed: vi.fn(),
	getSession: vi.fn(),
	markMyFeedSeen: vi.fn(),
	navigateToSession: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, client: mocks, baseUrl: "https://cp.test", userId: "user-me" }),
}));
vi.mock("../lib/navigate-to-session", () => ({
	useNavigateToSession: () => mocks.navigateToSession,
}));

const NOW = Date.now();
const minutesAgo = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

function item(sessionId: string, overrides: Partial<CloudCpFeedItem> = {}): CloudCpFeedItem {
	return {
		orgId: "org-1", orgName: "Northwind", projectId: "project-1", projectName: "storefront", sessionId,
		title: `Session ${sessionId}`, owner: { userId: "user-asha", name: "Asha Rao" }, status: "idle",
		line: "Waiting for its next message", people: [], following: false, updatedAt: minutesAgo(5), ...overrides,
	};
}

const NO_NEWS: CloudCpFeedAway = { since: minutesAgo(600), needsYou: 0, prsMerged: [], failed: [], started: { count: 0, names: [] } };

function page(items: CloudCpFeedItem[], extra: Partial<CloudCpFeedResponse> = {}): CloudCpFeedResponse {
	return { items, page: { hasMore: false }, ...extra };
}

let client: QueryClient;
function renderFeed() {
	client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TooltipProvider>
				<TeamFeedView />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	vi.clearAllMocks();
	resetTeamCollabSupport();
	mocks.markMyFeedSeen.mockResolvedValue({ seenAt: "2026-11-08T00:00:00Z" });
	mocks.getMyFeed.mockResolvedValue(page([]));
});
afterEach(() => resetTeamCollabSupport());

describe("TeamFeedView", () => {
	it("opens on Team, and each tab asks for its own sessions", async () => {
		renderFeed();
		const tabs = screen.getAllByRole("tab");
		expect(tabs.map((tab) => tab.textContent)).toEqual(["Team", "Mine", "Following"]);
		expect(screen.getByRole("tab", { name: "Team" })).toHaveAttribute("aria-selected", "true");
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "team", limit: 30 }, expect.anything()));
		fireEvent.click(screen.getByRole("tab", { name: "Mine" }));
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "mine", limit: 30 }, expect.anything()));
		expect(screen.getByRole("tab", { name: "Mine" })).toHaveAttribute("aria-selected", "true");
		fireEvent.click(screen.getByRole("tab", { name: "Following" }));
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "following", limit: 30 }, expect.anything()));
	});

	it.each([
		["team", "Team", "Nothing from your team yet."],
		["mine", "Mine", "Sessions you start or prompt show here."],
		["following", "Following", "Sessions you follow show here."],
	])("says what %s would show when it's empty", async (_scope, tab, message) => {
		renderFeed();
		fireEvent.click(screen.getByRole("tab", { name: tab }));
		expect(await screen.findByText(message)).toBeInTheDocument();
	});

	it("groups sessions that need someone, then working ones, then the rest, with counts", async () => {
		mocks.getMyFeed.mockResolvedValue(page([
			item("a", { status: "review" }),
			item("b", { status: "needs", line: "Waiting for approval to run pnpm prisma migrate dev" }),
			item("c", { status: "working" }),
			item("d", { status: "needs" }),
			item("e", { status: "merged" }),
		]));
		renderFeed();
		const needs = await screen.findByRole("region", { name: "Needs someone · 2" });
		expect(within(needs).getAllByRole("button").map((row) => row.getAttribute("data-feed-row"))).toEqual(["b", "d"]);
		expect(screen.getByRole("region", { name: "Working now · 1" })).toBeInTheDocument();
		const recent = screen.getByRole("region", { name: "Recent" });
		expect(within(recent).getAllByRole("button").map((row) => row.getAttribute("data-feed-row"))).toEqual(["a", "e"]);
		// No filter chips in this version.
		expect(screen.queryByText(/Any status|All projects/)).toBeNull();
	});

	it("draws a row as status, owner, title, follow mark, one line, up to three others, and the time", async () => {
		mocks.getMyFeed.mockResolvedValue(page([
			item("a", {
				status: "working", title: "Refactor session store", following: true, line: "renaming src/auth/session.ts",
				worker: { index: 2, of: 4, orchestratorTitle: "Auth cleanup" },
				people: [{ userId: "1", name: "Ben Ortiz" }, { userId: "2", name: "Chen Wei" }, { userId: "3", name: "Dana Kim" }, { userId: "4", name: "Eli Park" }],
				updatedAt: minutesAgo(3),
			}),
		]));
		const { container } = renderFeed();
		const row = await screen.findByRole("button", { name: /Refactor session store/ });
		expect(within(row).getByRole("img", { name: "Working" })).toBeInTheDocument();
		expect(within(row).getByText("Refactor session store")).toBeInTheDocument();
		expect(within(row).getByRole("img", { name: "You follow this" })).toBeInTheDocument();
		expect(within(row).getByText("Worker 2 of 4 · renaming src/auth/session.ts")).toBeInTheDocument();
		expect(within(row).getByRole("img", { name: "Also in it: Ben Ortiz, Chen Wei, Dana Kim, and Eli Park" }).querySelectorAll("[data-person-avatar]")).toHaveLength(3);
		expect(within(row).getByText("3m ago")).toBeInTheDocument();
		expect(row).toHaveAttribute("title", "storefront · Refactor session store");
		expect(container.querySelectorAll("[data-feed-status]")).toHaveLength(1);
	});

	it("draws a worker row whose orchestrator you can't open as a plain line", async () => {
		mocks.getMyFeed.mockResolvedValue(page([item("a", { status: "working", title: "Fix flaky test", line: "running npm test", worker: undefined })]));
		renderFeed();
		const row = await screen.findByRole("button", { name: /Fix flaky test/ });
		expect(within(row).getByText("running npm test")).toBeInTheDocument();
		expect(within(row).queryByText(/Worker/)).toBeNull();
	});

	it.each([
		["needs", "Needs someone"], ["working", "Working"], ["review", "In review"], ["ready", "Ready to merge"],
		["merged", "Merged"], ["failed", "Failed"], ["idle", "Idle"],
	] as const)("marks a %s session", async (status, label) => {
		mocks.getMyFeed.mockResolvedValue(page([item("a", { status })]));
		renderFeed();
		expect(await screen.findByRole("img", { name: label })).toHaveAttribute("data-feed-status", status);
	});

	it("opens the session when a row is clicked", async () => {
		mocks.getMyFeed.mockResolvedValue(page([item("a", { projectId: "project-9" })]));
		renderFeed();
		fireEvent.click(await screen.findByRole("button", { name: /Session a/ }));
		expect(mocks.navigateToSession).toHaveBeenCalledWith("project-9", "a");
	});

	it("shows While you were away only when something changed", async () => {
		mocks.getMyFeed.mockResolvedValue(page([item("a")], { away: NO_NEWS }));
		renderFeed();
		await screen.findByRole("button", { name: /Session a/ });
		expect(screen.queryByText("While you were away")).toBeNull();
	});

	it("lists what changed since the last visit, every line clickable", async () => {
		const requestBell = vi.spyOn(useUiStore.getState(), "requestNotificationCenterOpen");
		mocks.getMyFeed.mockResolvedValue(page([item("s-merged", { projectId: "project-2" }), item("s-failed")], {
			away: {
				since: minutesAgo(600),
				needsYou: 2,
				prsMerged: [{ number: 476, url: "https://github.test/pr/476", sessionId: "s-merged", orgId: "org-1" }, { number: 478, url: "", sessionId: "s-other", orgId: "org-1" }],
				failed: [{ sessionId: "s-failed", orgId: "org-1", title: "Upgrade Next.js to 16" }],
				started: { count: 5, names: ["Ben", "Dana", "Omar", "Riya"] },
			},
		}));
		renderFeed();
		const card = await screen.findByRole("region", { name: "While you were away" });
		expect(card).toHaveTextContent("2need you");
		expect(card).toHaveTextContent("2PRs merged:#476#478");
		expect(card).toHaveTextContent("1stopped with an error:Upgrade Next.js to 16");
		expect(card).toHaveTextContent("5sessions started by Ben, Dana, Omar, and Riya");
		expect(card).toHaveTextContent(/since /);
		fireEvent.click(within(card).getByText("need you"));
		expect(useUiStore.getState().notificationCenterOpenRequest).toBeGreaterThan(0);
		fireEvent.click(within(card).getByRole("button", { name: "#476" }));
		expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-2", "s-merged");
		// A session not on this page is looked up in its organization for its project.
		mocks.getSession.mockResolvedValue({ session: { id: "s-other", orgId: "org-1", projectId: "project-7" } });
		fireEvent.click(within(card).getByRole("button", { name: "#478" }));
		expect(mocks.getSession).toHaveBeenCalledWith("org-1", "s-other");
		await waitFor(() => expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-7", "s-other"));
		// And when that fails, it opens through the session's own route.
		mocks.getSession.mockRejectedValue(new CloudCpError("Not found", { status: 404, code: "SESSION_NOT_FOUND" }));
		fireEvent.click(within(card).getByRole("button", { name: "#478" }));
		await waitFor(() => expect(mocks.navigateToSession).toHaveBeenLastCalledWith(undefined, "s-other"));
		fireEvent.click(within(card).getByRole("button", { name: "Upgrade Next.js to 16" }));
		expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-1", "s-failed");
		requestBell.mockRestore();
	});

	it("shows new sessions on the Team tab from the started line", async () => {
		mocks.getMyFeed.mockImplementation(async (query: CloudCpFeedQuery) => page([], query.scope === "mine"
			? { away: { ...NO_NEWS, started: { count: 1, names: ["Ben"] } } } : {}));
		renderFeed();
		fireEvent.click(screen.getByRole("tab", { name: "Mine" }));
		fireEvent.click(await screen.findByText("session started by Ben"));
		expect(screen.getByRole("tab", { name: "Team" })).toHaveAttribute("aria-selected", "true");
	});

	it("marks caught up: records the visit and drops the card", async () => {
		mocks.getMyFeed.mockResolvedValue(page([], { away: { ...NO_NEWS, needsYou: 1 } }));
		renderFeed();
		await screen.findByRole("region", { name: "While you were away" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Mark caught up" })));
		expect(mocks.markMyFeedSeen).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(screen.queryByRole("region", { name: "While you were away" })).toBeNull());
	});

	it("records the visit when the page is left", async () => {
		const view = renderFeed();
		await screen.findByText("Nothing from your team yet.");
		view.unmount();
		await waitFor(() => expect(mocks.markMyFeedSeen).toHaveBeenCalledTimes(1));
	});

	it("records no visit when the feed never loaded", async () => {
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Boom", { status: 500, code: "internal" }));
		const view = renderFeed();
		await screen.findByText("Couldn't load the team feed.", {}, { timeout: 4000 });
		view.unmount();
		await act(async () => undefined);
		expect(mocks.markMyFeedSeen).not.toHaveBeenCalled();
	});

	it("says when the feed failed, and tries again on Retry", async () => {
		// The feed itself fails three times; the support probe beside it answers.
		let failures = 0;
		mocks.getMyFeed.mockImplementation(async (query: CloudCpFeedQuery) => {
			if (query.scope === "team" && failures < 3) {
				failures += 1;
				throw new CloudCpError("Boom", { status: 500, code: "internal" });
			}
			return page([]);
		});
		renderFeed();
		// A failure is asked again twice by itself before it shows.
		fireEvent.click(await screen.findByRole("button", { name: "Retry" }, { timeout: 4000 }));
		expect(await screen.findByText("Nothing from your team yet.")).toBeInTheDocument();
	});

	it("says the feed isn't here on a control plane without it", async () => {
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Not found", { status: 404 }));
		renderFeed();
		expect(await screen.findByText("The team feed isn't available here yet.")).toBeInTheDocument();
	});

	it("pages with each page's nextCursor until there is no more", async () => {
		recordTeamCollabSupport("https://cp.test", "supported");
		mocks.getMyFeed.mockImplementation(async (query: CloudCpFeedQuery) => query.cursor === "c2"
			? page([item("c")])
			: query.cursor === "c1"
				? page([item("b")], { page: { hasMore: true, nextCursor: "c2" } })
				: page([item("a")], { page: { hasMore: true, nextCursor: "c1" } }));
		renderFeed();
		fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
		expect(await screen.findByRole("button", { name: /Session b/ })).toBeInTheDocument();
		expect(mocks.getMyFeed).toHaveBeenLastCalledWith({ scope: "team", limit: 30, cursor: "c1" }, expect.anything());
		fireEvent.click(screen.getByRole("button", { name: "Show more" }));
		expect(await screen.findByRole("button", { name: /Session c/ })).toBeInTheDocument();
		expect(mocks.getMyFeed).toHaveBeenLastCalledWith({ scope: "team", limit: 30, cursor: "c2" }, expect.anything());
		expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
		// One list, in page order.
		expect(screen.getAllByRole("button").map((row) => row.getAttribute("data-feed-row")).filter(Boolean)).toEqual(["a", "b", "c"]);
	});

	it("shows no Show more when a page says there is more but gives no cursor", async () => {
		recordTeamCollabSupport("https://cp.test", "supported");
		mocks.getMyFeed.mockResolvedValue(page([item("a")], { page: { hasMore: true } }));
		renderFeed();
		await screen.findByRole("button", { name: /Session a/ });
		expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
	});
});
