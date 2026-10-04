import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { ResumeAgentControl } from "./ResumeAgentControl";

vi.mock("../hooks/useCanResumeAgent", () => ({ useCanResumeAgent: () => true }));

const session = { id: "sess-1", workspaceId: "proj-1", status: "exited", prs: [] } as WorkspaceSession;

function renderControl(hostId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<ResumeAgentControl session={session} hostId={hostId} />
		</QueryClientProvider>,
	);
}

describe("ResumeAgentControl workspace probes", () => {
	it("does not consult local workspace availability for a remote session", async () => {
		const getState = vi.spyOn(window.ao!.editorHandoff, "getState");
		renderControl("box-a");
		expect(await screen.findByRole("button", { name: "Resume agent" })).toBeEnabled();
		expect(getState).not.toHaveBeenCalled();
	});

	it("hides resume when the local workspace is definitively missing", async () => {
		const getState = vi.spyOn(window.ao!.editorHandoff, "getState").mockResolvedValue({
			targets: [],
			workspaceAvailable: false,
			unavailableCode: "SESSION_WORKSPACE_NOT_FOUND",
		});
		renderControl();
		await waitFor(() => expect(getState).toHaveBeenCalledWith(session.id));
		await waitFor(() => expect(screen.queryByRole("button", { name: "Resume agent" })).not.toBeInTheDocument());
	});
});
