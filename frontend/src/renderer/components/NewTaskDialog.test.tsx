import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkspaceSummary } from "../types/workspace";
import { NewTaskDialog } from "./NewTaskDialog";

const workspaces: WorkspaceSummary[] = [
	{ id: "proj-1", name: "storefront", kind: "cloud", cloudOrgId: "org-1", path: "", sessions: [] },
	{ id: "agent-1", name: "Fix the flaky test", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true, path: "", sessions: [] },
];

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: workspaces }),
	useRemoteWorkspaces: () => ({ data: [] }),
}));
vi.mock("./TaskComposer", () => ({ TaskComposer: () => <div data-testid="composer" /> }));

describe("NewTaskDialog", () => {
	// A Cloud standalone agent runs one session of its own: the Coder New Task
	// dialog offers Cloud projects, never an agent's project.
	it("offers Cloud projects but no Cloud standalone agent", async () => {
		const user = userEvent.setup();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<NewTaskDialog open projectId="proj-1" onCreated={() => undefined} onOpenChange={() => undefined} />
			</QueryClientProvider>,
		);
		await user.click(screen.getByRole("button", { name: "Project" }));
		expect(await screen.findByRole("menuitem", { name: /storefront/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Fix the flaky test/ })).not.toBeInTheDocument();
	});
});
