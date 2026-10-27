import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

const { getMock, navigateMock, workspaceQueryMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	navigateMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
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

vi.mock("./useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => workspaceQueryMock(),
}));

import { SessionSearchView } from "./SessionSearchView";
import { peekSessionSearchReveal, resetSessionSearchReveals } from "../lib/session-search-reveal";
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
		getMock.mockResolvedValue(response({ total: 40 }));
		const user = userEvent.setup();
		render(<SessionSearchView />, { wrapper });

		await user.type(screen.getByRole("searchbox", { name: "Search past sessions" }), "flaky");
		await waitFor(() => expect(screen.getByText("Fix the login test")).toBeInTheDocument());

		// The count is matches, not the page length: "1 of 40", not "1 of 1".
		expect(screen.getByTestId("session-search-count")).toHaveTextContent("1 of 40 sessions");
		await user.click(screen.getByRole("button", { name: /Show 39 more/ }));

		await waitFor(() => {
			const lastCall = getMock.mock.calls.at(-1);
			expect(lastCall?.[1]).toMatchObject({
				params: { query: expect.objectContaining({ limit: 50 }) },
			});
		});
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