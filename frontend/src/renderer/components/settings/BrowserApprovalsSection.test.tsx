import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "../../lib/api-client";
import { BrowserApprovalsSection } from "./BrowserApprovalsSection";

vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: [{ id: "proj", sessions: [{ id: "ao-7", title: "Checkout flow" }] }] }),
}));

const policy = {
	mode: "risky",
	defaultMode: "risky",
	sites: [{ site: "mail.example", category: "message", createdAt: "2026-11-01T10:00:00Z" }],
};
const entries = [
	{
		id: "apr_1", time: "2026-11-02T10:00:00Z", decidedAt: "2026-11-02T10:00:04Z", kind: "browser-action", sessionId: "ao-7",
		site: "shop.example", action: 'Click "Place order" on shop.example', category: "payment", decision: "deny",
	},
];

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<BrowserApprovalsSection />
		</QueryClientProvider>,
	);
}

describe("BrowserApprovalsSection", () => {
	afterEach(() => vi.restoreAllMocks());

	it("lists remembered sites and the audit log, and forgets a site", async () => {
		vi.spyOn(apiClient, "GET").mockImplementation((async (path: string) =>
			path === "/api/v1/approvals/policy" ? { data: policy } : { data: { entries } }) as never);
		const del = vi.spyOn(apiClient, "DELETE").mockResolvedValue({ data: { ...policy, sites: [] } } as never);
		renderSection();
		expect(await screen.findByText("mail.example")).toBeInTheDocument();
		expect(screen.getByText("Message")).toBeInTheDocument();
		const [entry] = await screen.findAllByTestId("approval-audit-entry");
		expect(entry).toHaveTextContent('Click "Place order" on shop.example');
		expect(entry).toHaveTextContent("Checkout flow · ao-7");
		expect(entry).toHaveTextContent("Denied");
		fireEvent.click(screen.getByRole("button", { name: "Remove mail.example" }));
		await waitFor(() =>
			expect(del).toHaveBeenCalledWith("/api/v1/approvals/sites", { params: { query: { site: "mail.example", category: "message" } } }),
		);
	});

	it("shows the five latest decisions until Show all", async () => {
		const many = Array.from({ length: 7 }, (_, index) => ({ ...entries[0], id: `apr_${index}` }));
		vi.spyOn(apiClient, "GET").mockImplementation((async (path: string) =>
			path === "/api/v1/approvals/policy" ? { data: policy } : { data: { entries: many } }) as never);
		renderSection();
		expect(await screen.findAllByTestId("approval-audit-entry")).toHaveLength(5);
		fireEvent.click(screen.getByRole("button", { name: "Show all" }));
		expect(screen.getAllByTestId("approval-audit-entry")).toHaveLength(7);
		expect(screen.queryByRole("button", { name: "Show all" })).toBeNull();
	});

	it("shows empty states", async () => {
		vi.spyOn(apiClient, "GET").mockImplementation((async (path: string) =>
			path === "/api/v1/approvals/policy" ? { data: { ...policy, sites: [] } } : { data: { entries: [] } }) as never);
		renderSection();
		expect(await screen.findByText("No sites yet")).toBeInTheDocument();
		expect(await screen.findByText("No gated actions yet")).toBeInTheDocument();
	});
});
