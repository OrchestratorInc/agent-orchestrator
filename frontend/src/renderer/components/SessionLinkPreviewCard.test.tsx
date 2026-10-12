import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import type { SessionLinkSource } from "../lib/use-session-link-navigation";
import type { PullRequestFacts, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { SessionLinkPreviewCard } from "./SessionLinkPreviewCard";

const mocks = vi.hoisted(() => ({ restore: vi.fn(), source: vi.fn() }));

vi.mock("../hooks/useRestoreSession", () => ({
	useRestoreSession: () => mocks.restore,
}));

vi.mock("../lib/use-session-link-navigation", () => ({
	useSessionLinkSource: (...args: unknown[]) => mocks.source(...args),
}));

vi.mock("./ui/hover-card", () => ({
	HoverCardContent: ({ children, className }: { children: React.ReactNode; className?: string }) => (
		<div className={className}>{children}</div>
	),
}));

function pr(
	number: number,
	overrides: Partial<PullRequestFacts> = {},
): PullRequestFacts {
	return {
		url: `https://github.com/aoagents/agent-orchestrator/pull/${number}`,
		number,
		state: "open",
		ci: "passing",
		review: "none",
		mergeability: "unknown",
		reviewComments: false,
		updatedAt: "2026-10-08T10:00:00Z",
		...overrides,
	};
}

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "worker-367",
		workspaceId: "project-a",
		workspaceName: "Agent Orchestrator",
		title: "Implement worker link previews",
		provider: "codex",
		kind: "worker",
		status: "working",
		displayStatus: "Implementing",
		updatedAt: "2026-10-08T10:00:00Z",
		prs: [],
		...overrides,
	};
}

function workspace(worker: WorkspaceSession): WorkspaceSummary {
	return { id: worker.workspaceId, name: worker.workspaceName, path: "/repo", sessions: [worker] };
}

function source(overrides: Partial<SessionLinkSource> = {}): SessionLinkSource {
	return { ready: true, isLoading: false, isError: false, workspaces: [], ...overrides };
}

describe("SessionLinkPreviewCard", () => {
	beforeEach(() => {
		mocks.restore.mockReset();
		mocks.restore.mockResolvedValue({ status: "success" });
		mocks.source.mockReset();
		mocks.source.mockReturnValue(source());
	});

	it("resolves the card from the chat source host", () => {
		const remote = session({ title: "Remote worker" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(remote)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" sourceHostId="box-a" />);

		expect(mocks.source).toHaveBeenCalledWith("box-a", undefined);
		expect(screen.getByText("Remote worker")).toBeInTheDocument();
	});

	it("keeps stale session data visible when the source refresh fails", () => {
		const stale = session({ title: "Cached worker" });
		mocks.source.mockReturnValue(source({ isError: true, ready: false, workspaces: [workspace(stale)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);

		expect(screen.getByText("Cached worker")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("renders distinct loading, failed, and unavailable states", () => {
		mocks.source.mockReturnValue(source({ isLoading: true, ready: false }));
		const view = render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("status", { name: "Checking…" })).toBeInTheDocument();

		mocks.source.mockReturnValue(source({ isError: true, ready: false }));
		view.rerender(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("alert")).toHaveTextContent("Could not load sessions");

		mocks.source.mockReturnValue(source());
		view.rerender(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		expect(screen.getByRole("status")).toHaveTextContent("Unable to verify");
		expect(screen.queryByText(/sidebar/i)).not.toBeInTheDocument();
	});

	it("shows open for pending readiness and blocked only for real blockers", () => {
		const worker = session({
			prs: [
				pr(1, { ci: "pending" }),
				pr(2, { review: "review_required" }),
				pr(3, { mergeability: "mergeable" }),
				pr(4, { state: "merged", mergeability: "mergeable" }),
			],
		});
		mocks.source.mockReturnValue(source({ workspaces: [workspace(worker)] }));

		const { container } = render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);

		expect(screen.getByText("4 PRs")).toBeInTheDocument();
		expect(screen.getByText("PR #1").parentElement).toHaveTextContent(/open/i);
		expect(screen.getByText("PR #2").parentElement).toHaveTextContent("Blocked");
		expect(screen.getByText("PR #3").parentElement).toHaveTextContent("Ready to merge");
		expect(screen.getByText("PR #4").parentElement).toHaveTextContent(/merged/i);
		expect(container.innerHTML).not.toContain("text-[");
	});

	it("replaces terminated session details with a remote aware resume action", async () => {
		const terminated = session({ isTerminated: true, status: "terminated", prs: [pr(1)] });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(terminated)] }));

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" sourceHostId="box-a" />);

		expect(screen.getByRole("status", { name: "Agent terminated" })).toBeInTheDocument();
		expect(screen.queryByText("Implement worker link previews")).not.toBeInTheDocument();
		expect(screen.queryByText("1 PR")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));
		expect(mocks.restore).toHaveBeenCalledWith("worker-367", "box-a");
	});

	it("shows restore failures inside the terminated card", async () => {
		const terminated = session({ isTerminated: true, status: "terminated" });
		mocks.source.mockReturnValue(source({ workspaces: [workspace(terminated)] }));
		mocks.restore.mockResolvedValue({ status: "not_resumable", message: "No saved session is available." });

		render(<SessionLinkPreviewCard href="ao://sessions/project-a/worker-367" />);
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("No saved session is available.");
	});

	it("localizes the PR count instead of adding an English suffix", () => {
		expect(appI18n.t("pr.count", { count: 1, lng: "es" })).toBe("1 PR");
		expect(appI18n.t("pr.count", { count: 2, lng: "es" })).toBe("2 PR");
	});
});
