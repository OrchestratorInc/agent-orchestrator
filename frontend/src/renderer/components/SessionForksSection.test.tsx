import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { SessionForksSection } from "./SessionForksSection";

const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));

vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: () => ({ GET: getMock }),
}));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

const session = { id: "p-9", workspaceId: "p", title: "Fork of Fix login", status: "idle", mode: "chat" } as unknown as WorkspaceSession;

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<SessionForksSection session={session} />
		</QueryClientProvider>,
	);
}

describe("SessionForksSection", () => {
	beforeEach(() => useUiStore.setState({ developerMode: true }));
	afterEach(() => {
		vi.clearAllMocks();
		useUiStore.setState({ developerMode: false });
	});

	it("links a fork to its original and shows the original's PRs read-only", async () => {
		getMock.mockResolvedValue({ data: {
			allowed: true,
			forkedFrom: { sessionId: "p-1", title: "Fix login", sourceExists: true },
			sourcePRs: [{ url: "https://github.com/a/b/pull/7", number: 7, merged: false, closed: false }],
		} });
		renderSection();
		expect(await screen.findByText(/This session was forked from/)).toHaveTextContent("This session was forked from Fix login.");
		fireEvent.click(screen.getByRole("button", { name: "Fix login" }));
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-1");
		expect(screen.getByRole("link", { name: "Original's PR #7" })).toHaveAttribute("href", "https://github.com/a/b/pull/7");
	});

	it("marks a fork whose agent was given its conversation as text", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forkedFrom: { sessionId: "p-1", title: "Fix login", sourceExists: true, textHandoff: true } } });
		renderSection();
		expect(await screen.findByText("Conversation copied as text")).toBeInTheDocument();
	});

	it("says when the original has since been deleted, without a link", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forkedFrom: { sessionId: "p-1", title: "Fix login", sourceExists: false } } });
		renderSection();
		expect(await screen.findByText(/This session was forked from/)).toHaveTextContent("This session was forked from Fix login.");
		expect(screen.getByText("The original session has since been deleted.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Fix login" })).toBeNull();
	});

	it("says which sessions were forked from an original, with links", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forks: [
			{ sessionId: "p-10", title: "Fork of Fix login", exists: true },
			{ sessionId: "p-11", title: "Fork 2 of Fix login", exists: false },
		] } });
		renderSection();
		expect(await screen.findByText("2 sessions were forked from this one:")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Fork of Fix login" }));
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("p-10");
		expect(screen.getByText("Fork 2 of Fix login (deleted)")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Fork 2 of Fix login/ })).toBeNull();
	});

	it("names a single fork in the singular", async () => {
		getMock.mockResolvedValue({ data: { allowed: true, forks: [{ sessionId: "p-10", title: "Fork of Fix login", exists: true }] } });
		renderSection();
		expect(await screen.findByText("1 session was forked from this one:")).toBeInTheDocument();
	});

	it("keeps the start of a long fork name, cutting its end, and shows it all on hover", async () => {
		const long = "Fork 3 of Create a file a.txt containing the word hello and then append a second line";
		getMock.mockResolvedValue({ data: { allowed: true,
			forkedFrom: { sessionId: "p-1", title: long, sourceExists: true },
			forks: [{ sessionId: "p-12", title: long, exists: true }],
		} });
		renderSection();
		const [origin, fork] = await screen.findAllByRole("button", { name: long });
		// A left-aligned, shrinkable button whose label truncates at its end.
		expect(fork).toHaveClass("justify-start", "min-w-0", "shrink");
		expect(fork.querySelector("span.truncate")).toHaveTextContent(long);
		expect(fork).toHaveAttribute("title", long);
		// In the sentence, a long name wraps with it instead of spilling out.
		expect(origin).toHaveClass("inline", "whitespace-normal");
	});

	it("renders nothing for a session that is neither a fork nor forked", async () => {
		getMock.mockResolvedValue({ data: { allowed: true } });
		const { container } = renderSection();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(container.querySelector("[data-testid=session-forks]")).toBeNull();
	});
});
