import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const SESSION = "bee71acf-6f33-4529-b1e4-661c6a8a4c15";
const ORG = "584abc42-0cab-4626-8039-e06d74ecf876";
const state = vi.hoisted(() => ({ list: vi.fn() }));

vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ ready: true, baseUrl: "https://cp.test", userId: "u", client: { listBrowserApprovals: state.list } }),
}));
vi.mock("../../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({
		data: [{ id: "c3bd963e-b623-40e0-93b0-369110e345c8", sessions: [{ id: SESSION, title: "Freestyle", cloud: { orgId: ORG, desiredState: "running", observedState: "running" } }] }],
	}),
}));
vi.mock("../../lib/approvals", async (original) => ({
	...(await original<typeof import("../../lib/approvals")>()),
	usePendingApprovals: () => ({ data: [] }),
	useRemotePendingApprovals: () => [],
}));
vi.mock("@tanstack/react-router", () => ({
	useRouterState: ({ select }: { select: (s: unknown) => unknown }) => select({ matches: [{ params: { projectId: "c3bd963e-b623-40e0-93b0-369110e345c8", sessionId: SESSION } }] }),
}));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => vi.fn() }));

import { GlobalApprovalCard, SessionApprovalDock } from "./ApprovalHost";

describe("a cloud approval on the session on screen (live staging payload)", () => {
	it("shows the card in the session's dock", async () => {
		const createdAt = new Date().toISOString().replace("Z", "924Z").replace(/\.(\d{3})924Z/, ".$1924Z");
		const expiresAt = new Date(Date.now() + 300_000).toISOString().replace(/\.(\d{3})Z/, ".$1924Z");
		state.list.mockResolvedValue({
			pending: [{
				id: "c8532982-1fd5-4610-9daa-6fb5f514e9c0", sessionId: SESSION, action: "click", category: "pay",
				reason: '"Place order" looks like a pay action', site: "", summary: 'click "Place order" on this page', createdAt, expiresAt,
			}],
		});
		render(
			<QueryClientProvider client={new QueryClient()}>
				<GlobalApprovalCard />
				<SessionApprovalDock sessionId={SESSION} cloudOrgId={ORG} />
			</QueryClientProvider>,
		);
		expect(await screen.findByTestId("session-approval-dock")).toHaveTextContent('click "Place order" on this page');
		expect(screen.queryByTestId("global-approval-card")).toBeNull();
	});
});
