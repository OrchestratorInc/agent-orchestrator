import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpFeedAway, CloudCpFeedItem, CloudCpFeedQuery, CloudCpFeedResponse } from "../lib/cloud-cp/types";
import { recordTeamCollabSupport, resetTeamCollabSupport } from "../lib/cloud-team-collab";
import { useUiStore } from "../stores/ui-store";
import { sinceLabel, TeamFeedView } from "./TeamFeedView";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	getMyFeed: vi.fn(),
	getSession: vi.fn(),
	markMyFeedSeen: vi.fn(),
	navigateToSession: vi.fn(),
}));
const account = vi.hoisted(() => ({ ready: true, signedOut: false }));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: account.ready, client: mocks, baseUrl: "https://cp.test", userId: account.ready ? "user-me" : "", signedOut: account.signedOut }),
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
	account.ready = true;
	account.signedOut = false;
	resetTeamCollabSupport();
	mocks.markMyFeedSeen.mockResolvedValue({ seenAt: "2026-11-08T00:00:00Z" });
	mocks.getMyFeed.mockResolvedValue(page([]));
});
afterEach(() => {
	resetTeamCollabSupport();
	vi.useRealTimers();
	Reflect.deleteProperty(document, "visibilityState");
});

describe("TeamFeedView", () => {
	it("opens on All, and each tab asks for its own sessions", async () => {
		renderFeed();
		const tabs = screen.getAllByRole("tab");
		expect(tabs.map((tab) => tab.textContent)).toEqual(["All", "Mine", "Following"]);
		expect(screen.getByRole("tab", { name: "All" })).toHaveAttribute("aria-selected", "true");
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "team", limit: 30 }, expect.anything()));
		fireEvent.click(screen.getByRole("tab", { name: "Mine" }));
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "mine", limit: 30 }, expect.anything()));
		expect(screen.getByRole("tab", { name: "Mine" })).toHaveAttribute("aria-selected", "true");
		fireEvent.click(screen.getByRole("tab", { name: "Following" }));
		await waitFor(() => expect(mocks.getMyFeed).toHaveBeenCalledWith({ scope: "following", limit: 30 }, expect.anything()));
	});

	it("moves between tabs with the arrow keys, and names the panel by its tab", async () => {
		renderFeed();
		const all = screen.getByRole("tab", { name: "All" });
		expect(all).toHaveAttribute("tabindex", "0");
		expect(screen.getByRole("tab", { name: "Mine" })).toHaveAttribute("tabindex", "-1");
		expect(screen.getByRole("tabpanel", { name: "All" })).toBeInTheDocument();
		all.focus();
		fireEvent.keyDown(all, { key: "ArrowRight" });
		expect(screen.getByRole("tab", { name: "Mine" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByRole("tab", { name: "Mine" })).toHaveFocus();
		expect(screen.getByRole("tabpanel", { name: "Mine" })).toBeInTheDocument();
		fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" });
		fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" });
		expect(screen.getByRole("tab", { name: "Following" })).toHaveFocus();
		fireEvent.keyDown(document.activeElement!, { key: "Home" });
		expect(screen.getByRole("tab", { name: "All" })).toHaveAttribute("aria-selected", "true");
	});

	it("says to sign in when signed out, instead of loading forever", async () => {
		account.ready = false;
		account.signedOut = true;
		renderFeed();
		expect(screen.getByText("Sign in to see what your team is doing.")).toBeInTheDocument();
		expect(screen.queryByText("Loading…")).toBeNull();
		expect(mocks.getMyFeed).not.toHaveBeenCalled();
	});

	it.each([
		["team", "All", "Nothing from your team yet."],
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
		// The row is named by its owner, and its tooltip says who started it and who else is in it.
		expect(row).toHaveAccessibleName(expect.stringContaining("Asha Rao"));
		expect(within(row).getByRole("img", { name: "Asha Rao" })).toBeInTheDocument();
		expect(row.getAttribute("title")).toBe("storefront · Refactor session store\nStarted by Asha Rao\nAlso in it: Ben Ortiz, Chen Wei, Dana Kim, and Eli Park");
		expect(within(row).getByText("Refactor session store")).toBeInTheDocument();
		expect(within(row).getByRole("img", { name: "You follow this" })).toBeInTheDocument();
		expect(within(row).getByText("Worker 2 of 4 · renaming src/auth/session.ts")).toBeInTheDocument();
		const others = within(row).getByRole("img", { name: "Also in it: Ben Ortiz, Chen Wei, Dana Kim, and Eli Park" });
		expect(others.querySelectorAll("[data-person-avatar]")).toHaveLength(3);
		// The fourth is counted, not hidden.
		expect(within(others).getByText("+1")).toBeInTheDocument();
		expect(within(row).getByText("3m ago")).toBeInTheDocument();
		expect(container.querySelectorAll("[data-feed-status]")).toHaveLength(1);
	});

	it("marks only sessions you follow but didn't start", async () => {
		mocks.getMyFeed.mockResolvedValue(page([
			item("mine", { title: "Mine", following: true, owner: { userId: "user-me", name: "Me Myself" } }),
			item("theirs", { title: "Theirs", following: true }),
			item("other", { title: "Other", following: false }),
		]));
		renderFeed();
		expect(within(await screen.findByRole("button", { name: /Mine/ })).queryByRole("img", { name: "You follow this" })).toBeNull();
		expect(within(screen.getByRole("button", { name: /Theirs/ })).getByRole("img", { name: "You follow this" })).toBeInTheDocument();
		expect(within(screen.getByRole("button", { name: /Other/ })).queryByRole("img", { name: "You follow this" })).toBeNull();
	});

	it("shows no +N with three others or fewer", async () => {
		mocks.getMyFeed.mockResolvedValue(page([item("a", { people: [{ userId: "1", name: "Ben Ortiz" }] })]));
		renderFeed();
		const row = await screen.findByRole("button", { name: /Session a/ });
		expect(row.querySelector("[data-feed-more]")).toBeNull();
		expect(row.getAttribute("title")).toBe("storefront · Session a\nStarted by Asha Rao\nAlso in it: Ben Ortiz");
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

	it("lists what changed since the last visit, every line clickable, the numbers in the sentence", async () => {
		const requestBell = vi.spyOn(useUiStore.getState(), "requestNotificationCenterOpen");
		mocks.getMyFeed.mockResolvedValue(page([item("s-merged", { projectId: "project-2" }), item("s-failed")], {
			away: {
				since: minutesAgo(600),
				needsYou: 2,
				prsMerged: [
					{ number: 476, url: "https://github.test/pr/476", sessionId: "s-merged", orgId: "org-1", projectId: "project-2" },
					{ number: 478, url: "", sessionId: "s-other", orgId: "org-1", projectId: "project-7" },
				],
				failed: [{ sessionId: "s-failed", orgId: "org-1", projectId: "project-1", title: "Upgrade Next.js to 16" }],
				started: { count: 5, names: ["Ben", "Dana", "Omar", "Riya"] },
			},
		}));
		renderFeed();
		const card = await screen.findByRole("region", { name: "While you were away" });
		expect(card).toHaveTextContent("2 need you");
		expect(card).toHaveTextContent("2 PRs merged:#476#478");
		expect(card).toHaveTextContent("1 stopped with an error:Upgrade Next.js to 16");
		expect(card).toHaveTextContent("5 sessions started by Ben, Dana, Omar, and Riya");
		// When it counts from is in the title's tooltip, not the header.
		expect(card).not.toHaveTextContent(/since/i);
		fireEvent.click(within(card).getByText("need you"));
		expect(useUiStore.getState().notificationCenterOpenRequest).toBeGreaterThan(0);
		fireEvent.click(within(card).getByRole("button", { name: "#476" }));
		expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-2", "s-merged");
		// A session not on this page opens in the project its line names, with no lookup.
		fireEvent.click(within(card).getByRole("button", { name: "#478" }));
		expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-7", "s-other");
		expect(mocks.getSession).not.toHaveBeenCalled();
		fireEvent.click(within(card).getByRole("button", { name: "Upgrade Next.js to 16" }));
		expect(mocks.navigateToSession).toHaveBeenLastCalledWith("project-1", "s-failed");
		requestBell.mockRestore();
	});

	it("says how many sessions started without a dangling 'by' when no names came", async () => {
		mocks.getMyFeed.mockResolvedValue(page([], { away: { ...NO_NEWS, started: { count: 3, names: [] } } }));
		renderFeed();
		const card = await screen.findByRole("region", { name: "While you were away" });
		expect(card).toHaveTextContent("3 sessions started");
		expect(card).not.toHaveTextContent(/by/);
	});

	it.each([
		["today", new Date(2026, 10, 8, 9, 5), "9:05 AM"],
		["yesterday", new Date(2026, 10, 7, 18, 40), "yesterday 6:40 PM"],
		["within the week", new Date(2026, 10, 4, 15, 51), "Wed 3:51 PM"],
		["a week ago on the same weekday", new Date(2026, 10, 1, 3, 51), "Nov 1"],
		["another year", new Date(2025, 10, 1, 3, 51), "Nov 1, 2025"],
	])("says since %s as a chat app does", (_when, at, label) => {
		const t = ((key: string, options: { time: string }) => key === "feed.away.yesterday" ? `yesterday ${options.time}` : key) as never;
		expect(sinceLabel(at.toISOString(), "en-US", t, new Date(2026, 10, 8, 12, 0))).toBe(label);
	});

	it("shows new sessions on the All tab from the started line", async () => {
		mocks.getMyFeed.mockImplementation(async (query: CloudCpFeedQuery) => page([], query.scope === "mine"
			? { away: { ...NO_NEWS, started: { count: 1, names: ["Ben"] } } } : {}));
		renderFeed();
		fireEvent.click(screen.getByRole("tab", { name: "Mine" }));
		fireEvent.click(await screen.findByText("session started by Ben"));
		expect(screen.getByRole("tab", { name: "All" })).toHaveAttribute("aria-selected", "true");
	});

	it("marks caught up from its ✕: records the visit and drops the card", async () => {
		mocks.getMyFeed.mockResolvedValue(page([], { away: { ...NO_NEWS, needsYou: 1 } }));
		renderFeed();
		await screen.findByRole("region", { name: "While you were away" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Mark caught up" })));
		expect(mocks.markMyFeedSeen).toHaveBeenCalledTimes(1);
		await waitFor(() => expect(screen.queryByRole("region", { name: "While you were away" })).toBeNull());
	});

	it("says calmly when Mark caught up didn't go through, instead of silently bringing the card back", async () => {
		mocks.getMyFeed.mockResolvedValue(page([], { away: { ...NO_NEWS, needsYou: 1 } }));
		mocks.markMyFeedSeen.mockRejectedValue(new CloudCpError("Boom", { status: 500, code: "internal" }));
		renderFeed();
		await screen.findByRole("region", { name: "While you were away" });
		await act(async () => fireEvent.click(screen.getByRole("button", { name: "Mark caught up" })));
		const card = await screen.findByRole("region", { name: "While you were away" });
		expect(within(card).getByRole("status")).toHaveTextContent("Couldn't mark caught up. Try again.");
	});

	it("records the visit when the page is left, as of the last read you could see", async () => {
		const view = renderFeed();
		await screen.findByText("Nothing from your team yet.");
		const before = Date.now();
		view.unmount();
		await waitFor(() => expect(mocks.markMyFeedSeen).toHaveBeenCalledTimes(1));
		const { seenAt } = mocks.markMyFeedSeen.mock.calls[0]![0] as { seenAt: string };
		expect(Date.parse(seenAt)).toBeLessThanOrEqual(before);
		expect(Date.parse(seenAt)).toBeGreaterThan(NOW - 60_000);
	});

	it("records no visit when the window stayed hidden the whole time", async () => {
		Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
		const view = renderFeed();
		await screen.findByText("Nothing from your team yet.");
		view.unmount();
		await act(async () => undefined);
		expect(mocks.markMyFeedSeen).not.toHaveBeenCalled();
	});

	it("records no visit when the last refresh failed", async () => {
		const view = renderFeed();
		await screen.findByText("Nothing from your team yet.");
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Boom", { status: 500, code: "internal" }));
		await act(async () => { await client.refetchQueries({ predicate: (query) => query.queryKey.at(-1) === "head" }); });
		// The query's listeners hear of it a tick later.
		await act(() => new Promise((resolve) => setTimeout(resolve, 10)));
		view.unmount();
		await act(async () => undefined);
		expect(mocks.markMyFeedSeen).not.toHaveBeenCalled();
	});

	it("records no visit when the feed never loaded", async () => {
		mocks.getMyFeed.mockRejectedValue(new CloudCpError("Boom", { status: 500, code: "internal" }));
		const view = renderFeed();
		await screen.findByText("Couldn't load the team feed.", {}, { timeout: 4000 });
		view.unmount();
		await act(async () => undefined);
		expect(mocks.markMyFeedSeen).not.toHaveBeenCalled();
	});

	it("refreshes only the first page every 30s, keeping what Show more loaded", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		recordTeamCollabSupport("https://cp.test", "supported");
		let head = ["a"];
		mocks.getMyFeed.mockImplementation(async (query: CloudCpFeedQuery) => query.cursor === "c1"
			? page([item("b")])
			: page(head.map((id) => item(id)), { page: { hasMore: true, nextCursor: "c1" } }));
		renderFeed();
		fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
		expect(await screen.findByRole("button", { name: /Session b/ })).toBeInTheDocument();
		const rows = () => screen.getAllByRole("button").map((row) => row.getAttribute("data-feed-row")).filter(Boolean);
		head = ["n", "a"];
		await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
		await waitFor(() => expect(rows()).toEqual(["n", "a", "b"]));
		const reads = mocks.getMyFeed.mock.calls.map(([query]) => (query as CloudCpFeedQuery).cursor ?? "first");
		// The page Show more loaded was read once, never again.
		expect(reads.filter((cursor) => cursor === "c1")).toHaveLength(1);
		expect(reads.at(-1)).toBe("first");
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
