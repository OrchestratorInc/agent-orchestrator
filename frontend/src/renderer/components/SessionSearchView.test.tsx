import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { getMock, navigateMock, workspaceQueryMock, remoteSearchMock, NO_REMOTES } = vi.hoisted(() => ({
	getMock: vi.fn(),
	navigateMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	remoteSearchMock: vi.fn(),
	NO_REMOTES: { answers: [], settled: true },
}));

// Search across machines is its own hook with its own tests; here it says what
// the other machines answered, and records what the page asked them.
vi.mock("../hooks/useRemoteMachineSearch", () => ({
	useRemoteMachineSearch: (...args: unknown[]) => remoteSearchMock(...args),
}));

// The local daemon's client is mocked, not host resolution: a search on this
// machine must go through clientForSessionHost's local path (clientForHost
// throws for the local host), and only an unmocked host-clients proves it.
vi.mock("../lib/api-client", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/api-client")>(),
	apiClient: { GET: getMock },
	apiErrorMessage: () => "search failed",
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...await importOriginal<typeof import("@tanstack/react-router")>(),
	useNavigate: () => navigateMock,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => workspaceQueryMock(),
}));

import { SessionSearchView } from "./SessionSearchView";
import { peekSessionSearchReveal, resetSessionSearchReveals } from "../lib/session-search-reveal";
import { sessionUiKey } from "../lib/hosts";
import { useUiStore } from "../stores/ui-store";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const result = {
	sessionId: "proj-1",
	projectId: "proj",
	harness: "claude-code",
	kind: "user",
	messageId: "msg-1",
	seq: 4,
	snippet: "the flaky login test",
	highlights: [{ start: 4, end: 9 }],
	rank: -0.4,
	createdAt: "2026-08-09T12:00:00Z",
	sessionName: "Fix the login test",
	projectName: "proj",
	sessionState: "terminated",
};

function response(overrides: Record<string, unknown> = {}) {
	return {
		data: {
			query: "flaky",
			results: [result],
			total: 1,
			limit: 25,
			offset: 0,
			coverage: { state: "ready", indexedSources: 2, indexedDocs: 9 },
			...overrides,
		},
	};
}

describe("SessionSearchView", () => {
	beforeEach(() => {
		resetSessionSearchReveals();
		useUiStore.setState({ chatMessageFocusRequest: null });
		getMock.mockReset();
		navigateMock.mockReset();
		remoteSearchMock.mockReset();
		remoteSearchMock.mockReturnValue(NO_REMOTES);
		workspaceQueryMock.mockReturnValue({
			data: [
				{
					id: "proj",
					name: "proj",
					sessions: [{ id: "proj-1", provider: "claude-code", title: "Fix the login test" }],
				},
			],
		});
	});

	it("explains the surface before anything is typed", () => {
		render(<SessionSearchView />, { wrapper });
		expect(screen.getByRole("searchbox", { name: "Search past sessions" })).toBeInTheDocument();
		expect(screen.getByText("Search everything your agents have done")).toBeInTheDocument();
		// Other panels may load settings; no SEARCH is sent before there is a query.
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/search/sessions", expect.anything());
	});

	it("renders a result with the match highlighted and its session and time", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());

		expect(screen.getByText("Archived")).toBeInTheDocument();
		expect(screen.getByText("proj")).toBeInTheDocument();
		const snippet = screen.getByTestId("session-search-snippet");
		const mark = snippet.querySelector("mark");
		// Offsets are byte spans into "the flaky login test": 4..9 is "flaky".
		expect(mark?.textContent).toBe("flaky");
		expect(snippet.textContent).toBe("the flaky login test");
	});

	it("opens the session with the matched message handed to the timeline", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());
		await user.click(screen.getByRole("button", { name: /Fix the login test/ }));

		expect(navigateMock).toHaveBeenCalledWith({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId: "proj", sessionId: "proj-1" },
		});
		expect(peekSessionSearchReveal("proj-1")).toEqual({
			messageId: "msg-1",
			// The matched word, which the opened message highlights exactly.
			text: "flaky",
			// The query travels with the target: the opened session's find bar
			// starts with it, so Enter / Shift+Enter step through the matches.
			query: "flaky",
		});
	});

	it("opens a native-transcript result without inventing a message anchor", async () => {
		getMock.mockResolvedValue(
			response({
				results: [{ ...result, kind: "assistant", messageId: "", sessionName: "Native session" }],
			}),
		);
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Native session")).toBeInTheDocument());
		await user.click(screen.getByRole("button", { name: /Native session/ }));

		expect(navigateMock).toHaveBeenCalled();
		expect(peekSessionSearchReveal("proj-1")).toBeUndefined();
	});

	it("says what the index cannot see instead of implying nothing exists", async () => {
		getMock.mockResolvedValue(
			response({
				results: [],
				total: 0,
				coverage: {
					state: "backfilling",
					pendingSources: 3,
					unsupportedSources: 1,
					unindexedHarnesses: ["aider"],
				},
			}),
		);
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("No sessions matched.")).toBeInTheDocument());
		expect(screen.getByText(/still backfilling/)).toBeInTheDocument();
		expect(screen.getByText(/aider/)).toBeInTheDocument();
	});

	it("sends the project, agent and date filters to the daemon", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());

		// The date range is what lets a person narrow an archive by when they
		// remember working on it, so it has to reach the request, not just render.
		const from = screen.getByLabelText("From");
		const to = screen.getByLabelText("To");
		fireEvent.change(from, { target: { value: "2026-01-01" } });
		fireEvent.change(to, { target: { value: "2026-06-01" } });

		await waitFor(() => {
			const lastCall = getMock.mock.calls.at(-1);
			expect(lastCall?.[1]).toMatchObject({
				params: { query: expect.objectContaining({ from: "2026-01-01", to: "2026-06-01" }) },
			});
		});
	});

	it("reports how many matched and offers the next page", async () => {
		// A full first page of the 40 sessions that match.
		const firstPage = Array.from({ length: 25 }, (_, index) => ({
			...result,
			sessionId: `proj-${index}`,
			messageId: `msg-${index}`,
			sessionName: index === 0 ? result.sessionName : `Session ${index}`,
		}));
		getMock.mockResolvedValue(response({ results: firstPage, total: 40 }));
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());

		// The count is matches, not the page length: "25 of 40", not "25 of 25".
		expect(screen.getByTestId("session-search-count")).toHaveTextContent("25 of 40 sessions");
		await user.click(screen.getByRole("button", { name: /Show 15 more/ }));

		// The next page is its own request at the next offset, not a bigger one.
		await waitFor(() => {
			const lastCall = getMock.mock.calls.at(-1);
			expect(lastCall?.[1]).toMatchObject({
				params: { query: expect.objectContaining({ limit: 25, offset: 25 }) },
			});
		});
	});

	// Found in review: only the first page's error reached the view. A failed
	// second page left the first 25 rows with no error and a "Show more" that
	// skipped to offset 50, showing later results past the missing page.
	it("keeps the loaded page when the next one fails, and retries it before paging on", async () => {
		const all = Array.from({ length: 60 }, (_, index) => ({
			...result,
			sessionId: `proj-${index}`,
			messageId: `msg-${index}`,
			sessionName: `Session ${index}`,
		}));
		let secondPageFails = true;
		getMock.mockImplementation(async (path: string, options?: { params?: { query?: { limit?: number; offset?: number } } }) => {
			if (path !== "/api/v1/search/sessions") return response();
			const { limit = 25, offset = 0 } = options?.params?.query ?? {};
			if (offset === 25 && secondPageFails) return { error: { message: "daemon unavailable" } };
			return response({ results: all.slice(offset, offset + limit), total: all.length, limit, offset });
		});
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Session 24")).toBeInTheDocument());
		await user.click(screen.getByRole("button", { name: /Show 35 more/ }));

		// The page keeps its rows, says the next page failed, and offers a retry
		// instead of a "Show more" that would skip it.
		await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Could not load more sessions."), { timeout: 5000 });
		expect(screen.getByText("Session 24")).toBeInTheDocument();
		expect(screen.queryByText("Session 25")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Show \d+ more/ })).not.toBeInTheDocument();

		secondPageFails = false;
		await user.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(screen.getByText("Session 49")).toBeInTheDocument());
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Show 10 more/ })).toBeInTheDocument();
	});

	// The daemon clamps every request to 100 rows (SessionSearchMaxLimit). Paging
	// by a growing limit asked for 125 on the fifth page, got the same first 100,
	// and every later "Show more" repeated them.
	it("pages past the daemon's 100-row limit to the last session", async () => {
		const all = Array.from({ length: 130 }, (_, index) => ({
			...result,
			sessionId: `proj-${index}`,
			messageId: `msg-${index}`,
			sessionName: `Session ${index}`,
		}));
		getMock.mockImplementation(async (path: string, options?: { params?: { query?: { limit?: number; offset?: number } } }) => {
			if (path !== "/api/v1/search/sessions") return response();
			const { limit = 25, offset = 0 } = options?.params?.query ?? {};
			return response({ results: all.slice(offset, offset + Math.min(limit, 100)), total: all.length, limit, offset });
		});
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Session 24")).toBeInTheDocument());
		for (let page = 1; page < 6; page++) {
			await user.click(screen.getByRole("button", { name: /Show \d+ more/ }));
			const lastShown = Math.min((page + 1) * 25, all.length) - 1;
			await waitFor(() => expect(screen.getByText(`Session ${lastShown}`)).toBeInTheDocument());
		}

		// Every session once, in order, and nothing left to show.
		for (const session of all) expect(screen.getAllByText(session.sessionName)).toHaveLength(1);
		expect(screen.getByTestId("session-search-count")).toHaveTextContent("130 of 130 sessions");
		expect(screen.queryByRole("button", { name: /Show \d+ more/ })).not.toBeInTheDocument();
		// No request asked for more than one page.
		const searches = getMock.mock.calls.filter(([path]) => path === "/api/v1/search/sessions");
		expect(searches.every(([, options]) => options.params.query.limit === 25)).toBe(true);
		expect(new Set(searches.map(([, options]) => options.params.query.offset ?? 0))).toEqual(new Set([0, 25, 50, 75, 100, 125]));
	});

	it("opens a reviewer-chat match in the reviewer chat and says it is one", async () => {
		getMock.mockResolvedValue(response({ results: [{ ...result, messageId: "review-msg", reviewId: "review-1" }] }));
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await user.click(await screen.findByText("Fix the login test"));
		expect(screen.queryByText("Reviewer")).not.toBeNull();
		expect(useUiStore.getState().chatMessageFocusRequest).toMatchObject({
			sessionId: result.sessionId, messageId: "review-msg", reviewId: "review-1",
		});
		expect(peekSessionSearchReveal(result.sessionId)).toBeUndefined();
	});

	it("lists a session once with how many of its messages matched", async () => {
		getMock.mockResolvedValue(response({ results: [{ ...result, sessionMatches: 7 }] }));
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());

		// The row shows the session's best-matching message; the count is every
		// matched message, which the opened session's find bar steps through.
		expect(screen.getByTestId("session-search-match-count")).toHaveTextContent("7 matches");
		expect(screen.getByTestId("session-search-snippet").querySelector("mark")?.textContent).toBe("flaky");
	});

	it("says one match in the singular, and nothing for a name-only match", async () => {
		getMock.mockResolvedValue(response({ results: [{ ...result, sessionMatches: 1 }] }));
		const user = userEvent.setup();
		const view = render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByTestId("session-search-match-count")).toHaveTextContent("1 match"));
		view.unmount();

		getMock.mockResolvedValue(response({ results: [{ ...result, kind: "title", messageId: undefined, sessionMatches: 0 }] }));
		render(<SessionSearchView />, { wrapper });
		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "login");
		await waitFor(() => expect(screen.getAllByText("Fix the login test").length).toBeGreaterThan(0));
		expect(screen.queryByTestId("session-search-match-count")).not.toBeInTheDocument();
	});

	it("switches between relevance and recency ordering", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());
		await user.click(screen.getByRole("button", { name: "Sort results" }));
		await user.click(await screen.findByRole("menuitem", { name: "Newest" }));

		await waitFor(() => {
			const lastCall = getMock.mock.calls.at(-1);
			expect(lastCall?.[1]).toMatchObject({
				params: { query: expect.objectContaining({ sort: "recent" }) },
			});
		});
	});

	// Found in review: "Show more" was decided by how many distinct rows were on
	// screen. A newer session arriving between pages moves one row onto the next
	// page, the repeat is dropped, and 49 of 50 rows hid "Show more" though the
	// last page was full and 150 sessions remained.
	it("keeps Show more when a session moves between pages", async () => {
		const all = Array.from({ length: 200 }, (_, index) => ({
			...result,
			sessionId: `proj-${index}`,
			messageId: `msg-${index}`,
			sessionName: `Session ${index}`,
		}));
		getMock.mockImplementation(async (path: string, options?: { params?: { query?: { limit?: number; offset?: number } } }) => {
			if (path !== "/api/v1/search/sessions") return response();
			const { limit = 25, offset = 0 } = options?.params?.query ?? {};
			// By the second page a newer session has pushed everything down one.
			const rows = offset === 0 ? all.slice(0, limit) : all.slice(offset - 1, offset - 1 + limit);
			return response({ results: rows, total: all.length, limit, offset });
		});
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Session 24")).toBeInTheDocument());
		await user.click(screen.getByRole("button", { name: /Show \d+ more/ }));
		await waitFor(() => expect(screen.getByText("Session 48")).toBeInTheDocument());
		// Session 24 came back on the second page and is shown once.
		expect(screen.getAllByText("Session 24")).toHaveLength(1);
		expect(screen.getByRole("button", { name: /Show \d+ more/ })).toBeInTheDocument();
	});

	// Found in review: two machines' short lists can together outrun one page.
	// With 20 matches here and 20 on another machine, both ran out on the first
	// request, the merge held 40, the page showed 25, and "Show more" was hidden
	// because every machine had run out, stranding the other 15.
	it("keeps Show more for fetched rows past the page when every machine ran out", async () => {
		const local = Array.from({ length: 20 }, (_, index) => ({ ...result, sessionId: `proj-${index}`, messageId: `msg-${index}`, sessionName: `Local ${index}` }));
		getMock.mockResolvedValue(response({ results: local, total: 20 }));
		const remote = Array.from({ length: 20 }, (_, index) => ({ ...result, sessionId: `air-${index}`, sessionName: `Air ${index}` }));
		remoteSearchMock.mockReturnValue({
			answers: [{ hostId: "h_air", label: "MacBook Air", outcome: "searched", results: remote, total: 20, exhausted: true }],
			settled: true,
		});
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByTestId("session-search-count")).toHaveTextContent("25 of 40 sessions"));
		await user.click(screen.getByRole("button", { name: /Show 15 more/ }));
		await waitFor(() => expect(screen.getByTestId("session-search-count")).toHaveTextContent("40 of 40 sessions"));
		expect(screen.queryByRole("button", { name: /Show \d+ more/ })).not.toBeInTheDocument();
	});

	it("keeps Show more while another machine has more, after this one ran out", async () => {
		getMock.mockResolvedValue(response({ results: [result], total: 1 }));
		const remote = Array.from({ length: 25 }, (_, index) => ({
			...result,
			sessionId: `air-${index}`,
			sessionName: `Air session ${index}`,
			createdAt: "2026-08-08T12:00:00Z",
		}));
		remoteSearchMock.mockReturnValue({
			answers: [{ hostId: "h_air", label: "MacBook Air", outcome: "searched", results: remote, total: 60, exhausted: false }],
			settled: true,
		});
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Air session 0")).toBeInTheDocument());
		expect(screen.getByRole("button", { name: /Show \d+ more/ })).toBeInTheDocument();
	});

	it("searches every connected machine and opens a remote session there", async () => {
		getMock.mockResolvedValue(response());
		remoteSearchMock.mockReturnValue({ settled: true, answers: [
			{ hostId: "h_azure", label: "AzureLinux", outcome: "searched", total: 1, results: [{ ...result, messageId: "remote-msg", sessionName: "Fix the login test on azure" }] },
			{ hostId: "h_desk", label: "Desk", outcome: "offline", total: 0, results: [] },
		] });
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test on azure")).toBeInTheDocument());
		expect(screen.getByText("Fix the login test")).toBeInTheDocument();
		expect(screen.getByTestId("search-result-machine").textContent).toBe("AzureLinux");
		expect(screen.getByTestId("search-machines-not-searched").textContent).toBe("Not searched on Desk (offline).");
		// The count is every machine's, not just this one's.
		expect(screen.getByTestId("session-search-count").textContent).toContain("2");

		await user.click(screen.getByRole("button", { name: /Fix the login test on azure/ }));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "h_azure", projectId: "proj", sessionId: "proj-1" },
		});
		// This machine also has a proj-1: the remote match is keyed by host and id,
		// so the local session never takes it.
		expect(peekSessionSearchReveal(sessionUiKey("proj-1", "h_azure"))).toMatchObject({ messageId: "remote-msg" });
		expect(peekSessionSearchReveal("proj-1")).toBeUndefined();
	});

	it("asks the other machines with the same words and dates, and only this one for a project", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-01-01" } });
		await waitFor(() => expect(remoteSearchMock.mock.calls.at(-1)).toEqual([
			expect.objectContaining({ query: "flaky", from: "2026-01-01" }),
			true,
		]));

		// A project id names a project on this machine.
		await user.click(screen.getByRole("button", { name: "Filter by project" }));
		await user.click(await screen.findByRole("menuitem", { name: "proj" }));
		await waitFor(() => expect(remoteSearchMock.mock.calls.at(-1)?.[1]).toBe(false));
	});

	it("does not query a single character", async () => {
		getMock.mockResolvedValue(response());
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "f");
		// Other panels may load settings; no SEARCH is sent before there is a query.
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/search/sessions", expect.anything());
		expect(screen.getByText(/at least 2 characters/i)).toBeInTheDocument();
	});
});