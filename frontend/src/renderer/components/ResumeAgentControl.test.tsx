import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import { ResumeAgentControl } from "./ResumeAgentControl";

const { restartSessionAgent } = vi.hoisted(() => ({ restartSessionAgent: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: true, baseUrl: "", client: { restartSessionAgent } }) }));
// The local daemon's resume is not this test's: only the Cloud restart runs.
vi.mock("../hooks/useCanResumeAgent", () => ({ useCanResumeAgent: () => false }));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

const cloudSession: WorkspaceSession = {
	id: "cloud-1", workspaceId: "proj-1", workspaceName: "app", title: "Fix login",
	provider: "claude-code", kind: "worker", status: "idle", mode: "tui",
	createdAt: "2026-11-25T00:00:00Z", updatedAt: "2026-11-25T00:00:00Z",
	activity: { state: "exited", lastActivityAt: "2026-11-25T00:00:00Z" },
	cloud: { orgId: "org-1" }, prs: [],
};

function renderControl() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<ResumeAgentControl session={cloudSession} />
		</QueryClientProvider>,
	);
}

describe("ResumeAgentControl on a Cloud session", () => {
	beforeEach(() => restartSessionAgent.mockReset());

	it("says the session is moving right now when Cloud will not restart it mid-export", async () => {
		restartSessionAgent.mockRejectedValueOnce(new CloudCpError("move in progress (CP text)", { status: 409, code: "MOVE_IN_PROGRESS" }));
		renderControl();
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));
		expect(await screen.findByText(
			"This session is moving to another computer right now. Restart its agent once the move has finished or failed.",
		)).toBeTruthy();
		expect(screen.queryByText("move in progress (CP text)")).toBeNull();
		expect(restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1");
	});

	it("restarts it once the move has finished or failed", async () => {
		restartSessionAgent.mockResolvedValueOnce({ requested: true });
		renderControl();
		await userEvent.click(screen.getByRole("button", { name: "Resume agent" }));
		await waitFor(() => expect(restartSessionAgent).toHaveBeenCalledWith("org-1", "cloud-1"));
		expect(screen.queryByRole("status")).toBeNull();
	});
});
