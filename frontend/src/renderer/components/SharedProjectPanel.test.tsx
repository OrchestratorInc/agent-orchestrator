import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpMergeRequest, CloudCpSessionParticipant } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import { SharedProjectPanel } from "./SharedProjectPanel";

const mocks = vi.hoisted(() => ({
	participants: [] as CloudCpSessionParticipant[],
	requests: [] as CloudCpMergeRequest[],
	projectPRs: [] as { number: number; url: string; headSha: string }[],
	workerPRs: [] as { number: number; url: string; headSha: string }[],
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
// The orchestrator lists every pull request in its project; a worker's
// summary adds the ones it claimed to its own.
vi.mock("../hooks/useSessionScmSummary", () => ({
	useSessionScmSummary: (sessionId: string) => ({
		data: sessionId === "orch" ? { prs: mocks.projectPRs, linkedPrs: [] } : mocks.workerPRs.length ? { prs: mocks.workerPRs, linkedPrs: [] } : undefined,
	}),
}));
vi.mock("../lib/pr-display", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/pr-display")>()),
	sessionPRDisplaySummaries: (session: WorkspaceSession, summaries: { number: number; url: string }[] = []) => [
		...session.prs.map((pr) => ({ ...pr, headSha: "head-1" })),
		...summaries.filter((summary) => !session.prs.some((pr) => pr.number === summary.number)),
	],
}));
vi.mock("./SessionInspector", () => ({
	PRSummaryCard: (props: { pr: { number: number }; guest?: boolean; sessionId: string }) => {
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
		mocks.projectPRs = [];
		mocks.workerPRs = [];
		vi.clearAllMocks();
	});

	it("shows nothing on a project nobody was given", () => {
		mocks.participants = [];
		const { container } = render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(container.textContent).toBe("");
	});

	it("is a header button naming the shared project that opens its list as a popover, never over the page", () => {
		mocks.requests = [request];
		render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		const button = screen.getByRole("button", { name: "Shared project: 1 pull request · 1 to approve" });
		expect(button).toHaveAttribute("data-priority", "secondary");
		expect(button.querySelector("[data-compact-label]")?.textContent).toBe("1 pull request · 1 to approve");
		// Closed, nothing of the list is on the page to cover the conversation.
		expect(document.querySelector("[data-shared-project-list]")).toBeNull();
		fireEvent.click(button);
		const list = document.querySelector("[data-shared-project-list]");
		expect(list).not.toBeNull();
		expect(button.contains(list)).toBe(false);
		expect(list?.textContent).toContain("Shared project");
	});

	it("marks a request waiting on the owner even when the label is compacted, and not for a guest", () => {
		mocks.requests = [request];
		const view = render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(view.container.querySelector("[data-shared-project-pending]")).not.toBeNull();
		view.unmount();
		const guestOrchestrator = { ...orchestrator, cloud: { orgId: "org-1", shared: true } };
		const guestView = render(<SharedProjectPanel orchestrator={guestOrchestrator} sessions={[guestOrchestrator, guestWork]} />);
		expect(guestView.container.querySelector("[data-shared-project-pending]")).toBeNull();
		mocks.requests = [];
		guestView.unmount();
		const quiet = render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(quiet.container.querySelector("[data-shared-project-pending]")).toBeNull();
	});

	it.each([
		{ who: "the owner", shared: false },
		{ who: "a guest", shared: true },
	])("shows $who nothing until the project has a pull request, then the control", ({ shared }) => {
		const viewer = shared ? { ...orchestrator, cloud: { orgId: "org-1", shared: true } } : orchestrator;
		const quietWork = { ...guestWork, prs: [] as WorkspaceSession["prs"] };
		const { container, rerender } = render(<SharedProjectPanel orchestrator={viewer} sessions={[viewer, quietWork]} />);
		expect(container.textContent).toBe("");
		expect(screen.queryByRole("button")).toBeNull();
		mocks.projectPRs = [{ number: 500, url: "https://github.com/owner/repo/pull/500", headSha: "head-5" }];
		rerender(<SharedProjectPanel orchestrator={viewer} sessions={[viewer, quietWork]} />);
		expect(screen.getByRole("button", { name: "Shared project: 1 pull request" })).toHaveAttribute("aria-expanded", "false");
	});

	it("closes its list when the last pull request goes, so it does not reopen on its own", () => {
		mocks.projectPRs = [{ number: 500, url: "https://github.com/owner/repo/pull/500", headSha: "head-5" }];
		const quietWork = { ...guestWork, prs: [] as WorkspaceSession["prs"] };
		const { rerender } = render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, quietWork]} />);
		fireEvent.click(screen.getByRole("button", { name: "Shared project: 1 pull request" }));
		expect(document.querySelector("[data-shared-project-list]")).not.toBeNull();
		mocks.projectPRs = [];
		rerender(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, quietWork]} />);
		expect(screen.queryByRole("button")).toBeNull();
		mocks.projectPRs = [{ number: 501, url: "https://github.com/owner/repo/pull/501", headSha: "head-6" }];
		rerender(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, quietWork]} />);
		expect(screen.getByRole("button", { name: "Shared project: 1 pull request" })).toHaveAttribute("aria-expanded", "false");
		expect(document.querySelector("[data-shared-project-list]")).toBeNull();
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

	it("shows a pull request a session claimed once, under the project, and matches one seen under another address", () => {
		const claimedURL = "https://github.com/owner/repo/pull/500";
		mocks.workerPRs = [{ number: 412, url: prUrl, headSha: "head-1" }, { number: 500, url: claimedURL, headSha: "head-5" }];
		mocks.projectPRs = [{ number: 412, url: `${prUrl}?notification_referrer_id=1`, headSha: "head-1" }, { number: 500, url: claimedURL, headSha: "head-5" }];
		render(<SharedProjectPanel orchestrator={orchestrator} sessions={[orchestrator, guestWork]} />);
		expect(screen.getByRole("button", { expanded: false }).textContent).toContain("2 pull requests");
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		expect(screen.getAllByTestId("pr-card").map((card) => card.textContent)).toEqual(["#412", "#500"]);
		expect(mocks.card.mock.calls.map(([props]) => [props.pr.number, props.sessionId])).toEqual([[412, "guest-work"], [500, "orch"]]);
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

	it.each([
		{ who: "the owner", shared: false },
		{ who: "a guest", shared: true },
	])("shows $who every pull request in the project, even one opened in a session they were not given", ({ shared }) => {
		const viewer = shared ? { ...orchestrator, cloud: { orgId: "org-1", shared: true } } : orchestrator;
		const elsewhereURL = "https://github.com/owner/repo/pull/500";
		mocks.projectPRs = [{ number: 412, url: prUrl, headSha: "head-1" }, { number: 500, url: elsewhereURL, headSha: "head-5" }];
		render(<SharedProjectPanel orchestrator={viewer} sessions={[viewer, guestWork]} />);
		expect(screen.getByRole("button", { expanded: false }).textContent).toContain("2 pull requests");
		fireEvent.click(screen.getByRole("button", { expanded: false }));
		expect(screen.getAllByTestId("pr-card").map((card) => card.textContent)).toEqual(["#412", "#500"]);
		expect(screen.getByText("Opened by Vaanya in Login flow")).toBeTruthy();
		expect(screen.getByText("Opened in this project")).toBeTruthy();
		const cards = mocks.card.mock.calls.map(([props]) => props);
		expect(cards.map((props) => [props.pr.number, props.sessionId, props.guest])).toEqual([
			[412, "guest-work", shared],
			[500, "orch", shared],
		]);
	});
});
