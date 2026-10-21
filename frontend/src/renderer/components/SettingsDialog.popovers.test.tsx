import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { SettingsDialog } from "./SettingsPageTestHarness";

// The control plane and the org's offering are external boundaries; the
// settings dialog, the Repositories page, the repositories editor, and its
// picker are the real components.
const mocks = vi.hoisted(() => ({
	get: vi.fn(),
	patch: vi.fn(),
	listRepos: vi.fn(),
}));
vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getProject: mocks.get, updateProjectSettings: mocks.patch, listGitHubRepositories: mocks.listRepos },
		ready: true,
		baseUrl: "https://cloud.test",
	}),
}));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true, localEnabled: true, client: "" }) }));
vi.mock("../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ defaultProvider: "coder", hasCoder: true, customCoder: false, selfServeBilling: false }),
}));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1", role: "admin" } }) }));
vi.mock("../hooks/useOrgCoderConfig", () => ({ useOrgCoderConfig: () => ({ data: null, isLoading: false }) }));

const project = (config: Record<string, unknown>) => ({
	id: "p", orgId: "org-1", displayName: "alice", repositoryUrl: "https://github.com/acme/alice", defaultBranch: "main",
	config, createdAt: "2026-10-16T00:00:00Z", updatedAt: "2026-10-16T00:00:00Z",
});
const repository = (id: string, name: string) => ({
	githubRepositoryId: id, name, fullName: `acme/${name}`, htmlUrl: `https://github.com/acme/${name}`,
	defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "",
});

beforeEach(() => {
	useUiStore.setState({ settingsModal: null });
	mocks.get.mockReset().mockResolvedValue({ project: project({ repos: [{ url: "https://github.com/acme/11x" }] }) });
	mocks.patch.mockReset().mockImplementation((_org: string, _id: string, body: { config: Record<string, unknown> }) =>
		Promise.resolve({ project: project({ ...body.config }) }));
	mocks.listRepos.mockReset().mockResolvedValue({
		items: [repository("1", "alice"), repository("2", "11x"), repository("3", "voice-calling-ai")],
		page: { hasMore: false },
	});
});

function renderSettings() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><SettingsDialog /></QueryClientProvider>);
}

const settingsDialog = () => screen.getByRole("dialog", { name: "Repositories" });

it("keeps project settings open while a repository is picked, and saves the pick", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	expect(await screen.findByText("acme/alice")).toBeInTheDocument();

	await userEvent.click(screen.getByRole("button", { name: "Add a repository" }));
	// The picker's list is portalled outside the dialog; choosing from it is not an outside click.
	await userEvent.click(await screen.findByRole("option", { name: "acme/voice-calling-ai" }));

	expect(useUiStore.getState().settingsModal).not.toBeNull();
	expect(within(settingsDialog()).getByRole("combobox", { name: "Additional repository 2" })).toHaveTextContent("acme/voice-calling-ai");
	expect(screen.queryByRole("listbox")).toBeNull();

	await userEvent.click(within(settingsDialog()).getByRole("button", { name: "Save changes" }));
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledOnce());
	expect(mocks.patch.mock.calls[0][2]).toEqual({
		config: { repos: [{ url: "https://github.com/acme/11x" }, { url: "https://github.com/acme/voice-calling-ai" }] },
	});
	expect(useUiStore.getState().settingsModal).not.toBeNull();
});

it("closes an open picker on Escape before the dialog, then the dialog", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await userEvent.click(await screen.findByRole("combobox", { name: "Additional repository 1" }));
	expect(await screen.findByRole("listbox")).toBeInTheDocument();

	await userEvent.keyboard("{Escape}");
	await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
	expect(useUiStore.getState().settingsModal).not.toBeNull();

	await userEvent.keyboard("{Escape}");
	await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
});

it("closes an open picker on a backdrop click before the dialog, then the dialog", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await userEvent.click(await screen.findByRole("combobox", { name: "Additional repository 1" }));
	expect(await screen.findByRole("listbox")).toBeInTheDocument();

	await userEvent.click(screen.getByTestId("settings-dialog-overlay"));
	await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
	expect(useUiStore.getState().settingsModal).not.toBeNull();

	await userEvent.click(screen.getByTestId("settings-dialog-overlay"));
	await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
});

it("closes on a backdrop click while a picker is still playing its exit animation", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await screen.findByText("acme/alice");
	// Radix keeps a closing picker mounted, with data-state="closed", until its exit animation ends.
	const closing = document.createElement("div");
	closing.setAttribute("data-radix-popper-content-wrapper", "");
	closing.innerHTML = '<div data-state="closed" role="dialog"><div role="listbox"></div></div>';
	document.body.append(closing);
	try {
		await userEvent.click(screen.getByTestId("settings-dialog-overlay"));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
	} finally {
		closing.remove();
	}
});

it("still closes on a click on the backdrop", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await screen.findByText("acme/alice");

	await userEvent.click(screen.getByTestId("settings-dialog-overlay"));
	await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
});

it("closes on a backdrop click right after a picker was used", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await userEvent.click(await screen.findByRole("combobox", { name: "Additional repository 1" }));
	await userEvent.click(await screen.findByRole("option", { name: "acme/11x" }));
	expect(useUiStore.getState().settingsModal).not.toBeNull();

	await userEvent.click(screen.getByTestId("settings-dialog-overlay"));
	await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
});

it("keeps project settings open while a settings menu option is chosen", async () => {
	useUiStore.getState().openProjectSettings("p", { section: "repositories", cloudOrgId: "org-1" });
	renderSettings();
	await userEvent.click(await screen.findByRole("button", { name: "Agent starts in" }));
	await userEvent.click(await screen.findByRole("menuitem", { name: "Folder with all repositories" }));

	expect(useUiStore.getState().settingsModal).not.toBeNull();
	expect(within(settingsDialog()).getByText(/Falls back to the main repository/)).toBeInTheDocument();
});
