import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpMergeRequest, CloudCpSessionParticipant } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import { SharedProjectPanel } from "./SharedProjectPanel";

const mocks = vi.hoisted(() => ({
	participants: [] as CloudCpSessionParticipant[],
	requests: [] as CloudCpMergeRequest[],
	approve: vi.fn(),
	reject: vi.fn(),
	card: vi.fn(),
}));
vi.mock("../hooks/useSessionParticipants", () => ({
	useSessionParticipants: (sessions: { id: string }[]) => new Map(mocks.participants.length ? [[sessions[0].id, mocks.participants]] : []),
}));
vi.mock("../hooks/useProjectMergeRequests", () => ({
	useProjectMergeRequests: () => ({ data: mocks.requests }),
	pendingMergeRequestFor: (requests: CloudCpMergeRequest[] | undefined, url: string) => requests?.find((request) => request.status === "pending" && request.pullRequest.url === url),
	useMergeRequestActions: () => ({
		approve: { mutate: mocks.approve, isPending: false, error: null },
		reject: { mutate: mocks.reject, isPending: false, error: null },
		request: { mutate: vi.fn(), isPending: false, error: null },
	}),
}));
vi.mock("../hooks/useSessionScmSummary", () => ({ useSessionScmSummary: () => ({ data: undefined }) }));
vi.mock("../lib/pr-display", () => ({
	sessionPRDisplaySummaries: (session: WorkspaceSession) => session.prs.map((pr) => ({ ...pr, headSha: "head-1" })),
}));
vi.mock("./SessionInspector", () => ({
	PRSummaryCard: (props: { pr: { number: number }; guest?: boolean }) => {
		mocks.card(props);
		return <div data-testid="pr-card">#{props.pr.number}</div>;
	},
}));

const prUrl = "https://github.com/owner/repo/pull/412";
function session(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "s", workspaceId: "project-1", workspaceName: "project", title: "Session", provider: "codex", kind: "worker",
		status: "working", updatedAt: "2026-10-26T00:00:00Z", prs: [], cloud: { orgId: "org-1" },
		...overrides,
	} as WorkspaceSession;
}
const orchestrator = session({ id: "orch", kind: "orchestrator", title: "Orchestrator" });
const guestWork = session({
	id: "guest-work", title: "Login flow", prs: [{ number: 412, url: prUrl }] as WorkspaceSession["prs"],
	cloud: { orgId: "org-1", requestedBy: { userId: "u-vaanya", name: "Vaanya" } },
});
const request: CloudCpMergeRequest = {
	id: "request-1", projectId: "project-1", sessionId: "guest-work", sessionName: "Login flow",
	pullRequest: { id: "pr-1", number: 412, url: prUrl, title: "Login flow", repository: "owner/repo" },
	requestedBy: { userId: "u-vaanya", name: "Vaanya" }, status: "pending", createdAt: "2026-10-26T00:00:00Z",
};

describe("SharedProjectPanel", () => {
	beforeEach(() => {
		mocks.participants = [{ userId: "u-owner", name: "Vaibhaav", role: "owner", present: true, state: "idle" }];
		mocks.requests = [];
		vi.clearAllMocks();
	});

	it("shows nothing on a project nobody was given", () => {
		mocks.participants = [];
		const { container } = render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(container.textContent).toBe("");
	});

	it("lists who opened each pull request and in which session, and lets the owner approve or reject a request", () => {
		mocks.requests = [request];
		render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(screen.getByRole("button", { expanded: false }).textContent).toContain("1 pull request · 1 to approve");
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		expect(screen.getByText("Opened by Vaanya in Login flow")).toBeTruthy();
		expect(screen.getByTestId("pr-card").textContent).toBe("#412");
		expect(screen.getByText("Vaanya wants to merge")).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: "Approve and merge" }));
		expect(mocks.approve).toHaveBeenCalledWith({ requestId: "request-1", expectedHeadSha: "head-1" });
		fireEvent.click(screen.getByRole("button", { name: "Reject" }));
		expect(mocks.reject).toHaveBeenCalledWith("request-1");
	});

	it("shows a guest where their request stands, with no way to decide it", () => {
		mocks.requests = [request];
		const guestOrchestrator = { ...orchestrator, cloud: { orgId: "org-1", shared: true } };
		render(<SharedProjectPanel orchestrator={guestOrchestrator} sessions={[guestOrchestrator, guestWork]} />);
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		expect(screen.getByText("Waiting for the owner")).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Approve and merge" })).toBeNull();
		expect(mocks.card.mock.lastCall?.[0].guest).toBe(true);
	});
});
