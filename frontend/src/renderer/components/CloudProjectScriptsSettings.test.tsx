import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { CloudProjectScriptsSettings } from "./ProjectScriptsSettings";

const mocks = vi.hoisted(() => ({
	get: vi.fn(),
	patch: vi.fn(),
	defaultProvider: "coder",
	org: { id: "org-1", role: "admin" } as { id: string; role?: string },
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { getProject: mocks.get, updateProjectSettings: mocks.patch }, ready: true, baseUrl: "https://cloud.test" }),
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ defaultProvider: mocks.defaultProvider }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org }) }));

const project = (config: Record<string, unknown>) => ({
	id: "p", orgId: "org-1", displayName: "alice", repositoryUrl: "https://github.com/acme/alice", defaultBranch: "main",
	config, createdAt: "2026-10-13T00:00:00Z", updatedAt: "2026-10-13T00:00:00Z",
});
const stored = {
	coder: { templateId: "t-1", workspaceNamePrefix: "alice" },
	worker: { agent: "claude-code" },
	postCreate: ["npm ci"],
	preRemove: ["docker compose -p \"ao-$AO_SESSION_ID\" down"],
};

beforeEach(() => {
	mocks.defaultProvider = "coder";
	mocks.org = { id: "org-1", role: "admin" };
	mocks.get.mockReset().mockResolvedValue({ project: project(stored) });
	mocks.patch.mockReset().mockImplementation((_org: string, _id: string, body: { config: Record<string, unknown> }) =>
		Promise.resolve({ project: project({ ...stored, ...body.config }) }));
});

function renderScripts(onSaveState?: (state: { phase: string; dirty?: boolean }) => void) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(<QueryClientProvider client={client}><CloudProjectScriptsSettings projectId="p" cloudOrgId="org-1" onSaveState={onSaveState} /></QueryClientProvider>);
}

it("edits a Cloud project's Setup and Cleanup and saves only the two lists", async () => {
	renderScripts();
	expect(await screen.findByLabelText("Step 1")).toHaveValue("npm ci");
	expect(screen.getByText(/before the agent begins working/)).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Add step" }));
	fireEvent.change(screen.getByLabelText("Step 2"), { target: { value: "docker compose -p \"ao-$AO_SESSION_ID\" up -d" } });
	await userEvent.click(screen.getByRole("tab", { name: "Cleanup" }));
	expect(screen.getByLabelText("Step 1")).toHaveValue("docker compose -p \"ao-$AO_SESSION_ID\" down");
	// The local daemon's retry has no Cloud equivalent, and Coder runs Cleanup.
	expect(screen.queryByRole("button", { name: "Retry cleanup" })).toBeNull();
	expect(screen.queryByText(/Cleanup only runs for projects that use Coder/)).toBeNull();
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	// Settings merge objects but replace arrays: coder, worker and the rest stay as stored.
	expect(mocks.patch).toHaveBeenCalledWith("org-1", "p", {
		config: {
			postCreate: ["npm ci", "docker compose -p \"ao-$AO_SESSION_ID\" up -d"],
			preRemove: ["docker compose -p \"ao-$AO_SESSION_ID\" down"],
		},
	});
	await waitFor(() => expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled());
});

it("clears a list by saving it empty", async () => {
	renderScripts();
	await userEvent.click(await screen.findByRole("tab", { name: "Cleanup" }));
	fireEvent.change(screen.getByLabelText("Step 1"), { target: { value: "" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: { postCreate: ["npm ci"], preRemove: [] } });
});

it("says Cleanup does not run for a project outside Coder", async () => {
	mocks.defaultProvider = "freestyle";
	renderScripts();
	await userEvent.click(await screen.findByRole("tab", { name: "Cleanup" }));
	expect(screen.getByText(/Cleanup only runs for projects that use Coder/)).toBeInTheDocument();
});

it("reports unsaved edits as dirty without the autosave Saving state, and lists the variables plainly", async () => {
	const states: { phase: string; dirty?: boolean }[] = [];
	renderScripts((state) => states.push(state));
	fireEvent.change(await screen.findByLabelText("Step 1"), { target: { value: "npm ci --prefer-offline" } });
	await waitFor(() => expect(states.at(-1)).toMatchObject({ phase: "idle", dirty: true }));
	expect(states.some((state) => state.phase === "pending")).toBe(false);
	expect(screen.queryByText("Unsaved changes")).toBeNull();
	await userEvent.click(screen.getByText("Variables you can use"));
	expect(screen.getByText("$AO_SESSION_ID")).toBeInTheDocument();
	expect(screen.getByText(/a unique ID for the session/)).toBeInTheDocument();
	expect(screen.queryByText(/AO_SOURCE_TREE_PATH/)).toBeNull();
});

// Setup and Cleanup run in every member's later sessions, so a member sees
// them but only an owner or admin can change them.
it("shows a member the scripts read-only", async () => {
	mocks.org = { id: "org-1", role: "member" };
	renderScripts();
	expect(await screen.findByLabelText("Step 1")).toHaveValue("npm ci");
	expect(screen.getByLabelText("Step 1")).toBeDisabled();
	expect(screen.getByRole("button", { name: "Add step" })).toBeDisabled();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
	expect(screen.getByText("Only organization owners and admins can change these scripts.")).toBeInTheDocument();
});
