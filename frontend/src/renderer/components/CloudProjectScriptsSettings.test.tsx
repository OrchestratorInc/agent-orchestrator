import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
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
	expect(screen.queryByText(/deleted without it/)).toBeNull();
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

it("does not say Cleanup is skipped for a project on Freestyle or Docker, which run it", async () => {
	const { coder: _coder, ...withoutCoder } = stored;
	for (const provider of ["freestyle", "docker"]) {
		mocks.get.mockResolvedValue({ project: project({ ...withoutCoder, sandboxProvider: provider }) });
		const { unmount } = renderScripts();
		await userEvent.click(await screen.findByRole("tab", { name: "Cleanup" }));
		expect(screen.getByLabelText("Step 1")).toBeInTheDocument();
		expect(screen.queryByText(/deleted without it/)).toBeNull();
		unmount();
	}
});

it("says Cleanup does not run for a project on a provider without it", async () => {
	// A project pinned to a provider that has no Cleanup run, with no Coder
	// config, is deleted without it whatever the org default is.
	mocks.defaultProvider = "coder";
	const { coder: _coder, ...withoutCoder } = stored;
	mocks.get.mockResolvedValue({ project: project({ ...withoutCoder, sandboxProvider: "createos" }) });
	renderScripts();
	await userEvent.click(await screen.findByRole("tab", { name: "Cleanup" }));
	expect(screen.getByText(/Cleanup runs before AO deletes a Coder, Freestyle or Docker workspace/)).toBeInTheDocument();
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
	// The app is unselectable by default; the names are meant to be copied.
	expect(screen.getByText("$AO_SESSION_ID").closest("dl")).toHaveClass("select-text");
	expect(screen.getByRole("button", { name: "Copy $AO_SESSION_ID" })).toBeInTheDocument();
	// A single-repository project has no folder of repositories to name.
	expect(screen.queryByText("$AO_REPOS_ROOT")).toBeNull();
	// Save stays pinned in view however many steps the page lists.
	expect(screen.getByRole("button", { name: "Save changes" }).closest("[data-settings-save-footer]")).toHaveClass("sticky");
});

it("copies a variable and names the folder of every repository for a multi-repository project", async () => {
	const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
	mocks.get.mockResolvedValue({ project: project({ ...stored, repos: [{ url: "https://github.com/acme/11x" }] }) });
	renderScripts();
	await screen.findByLabelText("Step 1");
	await userEvent.click(screen.getByText("Variables you can use"));
	expect(screen.getByText("$AO_REPOS_ROOT")).toBeInTheDocument();
	expect(screen.getByText(/the folder that holds every repository's folder/)).toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Copy $AO_REPOS_ROOT" }));
	expect(writeText).toHaveBeenCalledWith("$AO_REPOS_ROOT");
	writeText.mockRestore();
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

it("runs a step in one of the project's additional repositories and stores it as a plain command run from its folder", async () => {
	const multi = {
		...stored,
		repos: [{ url: "https://github.com/acme/11x" }, { url: "https://github.com/acme/voice-calling-ai" }],
		postCreate: ["npm ci", "(\ncd -- '../11x' || exit 1\nmake dev-up\n)"],
	};
	mocks.get.mockResolvedValue({ project: project(multi) });
	renderScripts();
	expect(await screen.findByLabelText("Step 2")).toHaveValue("make dev-up");
	expect(screen.getByRole("button", { name: "Repository for step 1" })).toHaveTextContent("Main repository");
	expect(screen.getByRole("button", { name: "Repository for step 2" })).toHaveTextContent("acme/11x");
	await userEvent.click(screen.getByRole("button", { name: "Repository for step 1" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "acme/voice-calling-ai" }));
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2].config.postCreate).toEqual([
		"(\ncd -- '../voice-calling-ai' || exit 1\nnpm ci\n)",
		"(\ncd -- '../11x' || exit 1\nmake dev-up\n)",
	]);
});

it("keeps a step in a folder that is no longer a project repository, and reads the earlier object form", async () => {
	const legacy = {
		...stored,
		coder: { ...stored.coder, extraRepos: [{ url: "https://github.com/acme/11x.git" }] },
		postCreate: [{ command: "make dev-up", repository: "https://github.com/acme/11x" }, "(\ncd -- '../gone' || exit 1\nmake old\n)"],
	};
	mocks.get.mockResolvedValue({ project: project(legacy) });
	renderScripts();
	expect(await screen.findByLabelText("Step 1")).toHaveValue("make dev-up");
	// A non-canonical legacy URL still matches its repository.
	expect(screen.getByRole("button", { name: "Repository for step 1" })).toHaveTextContent("acme/11x");
	expect(screen.getByRole("button", { name: "Repository for step 2" })).toHaveTextContent("gone (not a project repository)");
	fireEvent.change(screen.getByLabelText("Step 1"), { target: { value: "make dev-up --fast" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2].config.postCreate).toEqual([
		"(\ncd -- '../11x' || exit 1\nmake dev-up --fast\n)",
		"(\ncd -- '../gone' || exit 1\nmake old\n)",
	]);
});

it("offers no repository choice to a single-repository project", async () => {
	renderScripts();
	await screen.findByLabelText("Step 1");
	expect(screen.queryByRole("button", { name: "Repository for step 1" })).toBeNull();
});

it("reads, edits and saves each step's own time limit in the stored form", async () => {
	mocks.get.mockResolvedValue({ project: project({ ...stored, postCreate: ["npm ci", "# ao-timeout: 45m\nmake seed"] }) });
	renderScripts();
	expect(await screen.findByLabelText("Step 2")).toHaveValue("make seed");
	const first = screen.getByLabelText("Time limit for step 1, in minutes");
	const second = screen.getByLabelText("Time limit for step 2, in minutes");
	// Empty is Setup's 30-minute default, shown as its placeholder.
	expect(first).toHaveValue("");
	expect(first).toHaveAttribute("placeholder", "30");
	expect(second).toHaveValue("45");
	// Only digits are kept; the arrow keys step by one minute.
	await userEvent.type(first, "9x0");
	expect(first).toHaveValue("90");
	fireEvent.keyDown(second, { key: "ArrowUp" });
	expect(second).toHaveValue("46");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2].config.postCreate).toEqual(["# ao-timeout: 90m\nnpm ci", "# ao-timeout: 46m\nmake seed"]);
});

it("shows a step's time limit hint, with what empty means, on keyboard focus", async () => {
	renderScripts();
	const field = await screen.findByLabelText("Time limit for step 1, in minutes");
	expect(field).toHaveAccessibleDescription("Time limit in minutes (1 to 120). Leave empty for 30 minutes.");
	expect(field).not.toHaveAttribute("title");
	act(() => field.focus());
	const tooltip = await screen.findByRole("tooltip");
	expect(tooltip).toHaveTextContent("Time limit in minutes (1 to 120). Leave empty for 30 minutes.");
	// Beside the field, so it covers neither the Scripts description above nor the step's command below.
	expect(tooltip.closest("[data-side]")).toHaveAttribute("data-side", "left");
});

it("says each kind's default: 30 minutes for a Setup step, 10 for a Cloud Cleanup step", async () => {
	renderScripts();
	expect(await screen.findByLabelText("Time limit for step 1, in minutes")).toHaveAttribute("placeholder", "30");
	await userEvent.click(screen.getByRole("tab", { name: "Cleanup" }));
	const cleanup = screen.getByLabelText("Time limit for step 1, in minutes");
	expect(cleanup).toHaveAttribute("placeholder", "10");
	expect(cleanup).toHaveAccessibleDescription("Time limit in minutes (1 to 120). Leave empty for 10 minutes.");
});

it("does not mark the time limit of an empty step, which saving drops", async () => {
	renderScripts();
	fireEvent.change(await screen.findByLabelText("Step 1"), { target: { value: "" } });
	const field = screen.getByLabelText("Time limit for step 1, in minutes");
	fireEvent.change(field, { target: { value: "0" } });
	// Saving keeps no empty step, so its limit cannot block it, and is not marked either.
	expect(field).not.toHaveAttribute("aria-invalid");
	expect(screen.queryByRole("alert")).toBeNull();
	expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
	// Once it has a command, the same limit is marked and keeps the form from saving.
	fireEvent.change(screen.getByLabelText("Step 1"), { target: { value: "npm ci" } });
	expect(field).toHaveAttribute("aria-invalid", "true");
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
});

it("marks an out-of-range time limit at once and keeps the form from saving", async () => {
	renderScripts();
	const field = await screen.findByLabelText("Time limit for step 1, in minutes");
	fireEvent.change(field, { target: { value: "121" } });
	expect(field).toHaveAttribute("aria-invalid", "true");
	expect(screen.getByRole("alert")).toHaveTextContent("Setup step 1: the time limit must be a whole number of minutes from 1 to 120.");
	expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
	// The message sits beside Save, clamped to its height, so the footer does not grow.
	expect(screen.getByRole("alert").parentElement).toContainElement(screen.getByRole("button", { name: "Save changes" }));
	expect(screen.getByRole("alert")).toHaveClass("line-clamp-2", "leading-4");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	expect(mocks.patch).not.toHaveBeenCalled();
	// Clearing it goes back to the default, which saves the plain command.
	fireEvent.change(field, { target: { value: "" } });
	expect(field).not.toHaveAttribute("aria-invalid");
	expect(screen.queryByRole("alert")).toBeNull();
	// A typed default's value is kept as the step's own limit.
	fireEvent.change(field, { target: { value: "30" } });
	await userEvent.click(screen.getByRole("tab", { name: "Cleanup" }));
	fireEvent.change(screen.getByLabelText("Time limit for step 1, in minutes"), { target: { value: "0" } });
	expect(screen.getByRole("alert")).toHaveTextContent("Cleanup step 1");
	fireEvent.change(screen.getByLabelText("Time limit for step 1, in minutes"), { target: { value: "120" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({ config: {
		postCreate: ["# ao-timeout: 30m\nnpm ci"],
		preRemove: ["# ao-timeout: 120m\ndocker compose -p \"ao-$AO_SESSION_ID\" down"],
	} });
});

it("keeps a repository step's time limit outside its folder wrapper", async () => {
	mocks.get.mockResolvedValue({ project: project({
		...stored, repos: [{ url: "https://github.com/acme/web" }],
		postCreate: ["# ao-timeout: 30m\n(\ncd -- '../web' || exit 1\nnpm ci\n)"],
	}) });
	renderScripts();
	expect(await screen.findByLabelText("Step 1")).toHaveValue("npm ci");
	expect(screen.getByLabelText("Time limit for step 1, in minutes")).toHaveValue("30");
	fireEvent.change(screen.getByLabelText("Step 1"), { target: { value: "npm ci --prefer-offline" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2].config.postCreate).toEqual(["# ao-timeout: 30m\n(\ncd -- '../web' || exit 1\nnpm ci --prefer-offline\n)"]);
});
