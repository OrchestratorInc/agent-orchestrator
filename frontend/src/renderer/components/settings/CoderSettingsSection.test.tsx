import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { CloudCpError } from "../../lib/cloud-cp";
import type { UseCoderTemplatesResult } from "../../hooks/useCoderTemplates";
import { CoderSettingsSection } from "./CoderSettingsSection";

// Mutable mock state so each test drives the loaded config and the template list
// independently. The section under test is gated on a saved connection
// (tokenSet) and rendered from the useCoderTemplates result.
const mocks = vi.hoisted(() => ({
	config: { data: undefined as unknown },
	templates: { value: { templates: [], isLoading: false, isError: false } as UseCoderTemplatesResult },
	putOrgCoderConfig: vi.fn(),
	deleteOrgCoderConfig: vi.fn(),
	org: { value: { id: "org-1" } as { id: string; role?: string } },
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "authenticated" }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { putOrgCoderConfig: mocks.putOrgCoderConfig, deleteOrgCoderConfig: mocks.deleteOrgCoderConfig },
		ready: true,
		baseUrl: "https://cp.test",
	}),
}));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: mocks.org.value }) }));
vi.mock("../../hooks/useOrgCoderConfig", () => ({
	orgCoderConfigQueryKey: ["cloud-org-coder-config"],
	useOrgCoderConfig: () => mocks.config,
}));
vi.mock("../../hooks/useCoderTemplates", () => ({ useCoderTemplates: () => mocks.templates.value }));

const INVALID_WORKING_DIRECTORY = "Enter a path that starts with /, ~/ or $HOME/ and names a folder below it, without . or .. segments. {repo} is the only placeholder.";

const savedConfig = { baseUrl: "https://coder.test", owner: "me", defaultTemplateId: "t-1", tokenSet: true };
// A control plane that knows every Workspace setup field reports protectSecretFiles.
const currentConfig = { ...savedConfig, protectSecretFiles: true };

function renderSection(queryClient = new QueryClient()) {
	render(
		<QueryClientProvider client={queryClient}>
			<CoderSettingsSection />
		</QueryClientProvider>,
	);
}

describe("CoderSettingsSection templates list", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.config.data = undefined;
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
	});

	it("hides the templates list until a connection is saved", () => {
		mocks.config.data = { baseUrl: "https://coder.test", owner: "me", defaultTemplateId: "t-1", tokenSet: false };
		renderSection();
		expect(screen.queryByText("Templates on your Coder")).not.toBeInTheDocument();
	});

	it("shows a loading state while listing templates", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: true, isError: false };
		renderSection();
		expect(screen.getByText("Templates on your Coder")).toBeInTheDocument();
		expect(screen.getByText("Loading templates…")).toBeInTheDocument();
	});

	it("shows an error when the Coder instance or token is bad", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: false, isError: true };
		renderSection();
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't load templates");
	});

	it("shows an empty state when the Coder has no templates", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
		renderSection();
		expect(screen.getByText("No templates found on this Coder deployment.")).toBeInTheDocument();
	});

	it("lists each template with its name and one-line spec", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = {
			templates: [
				{ id: "t-1", name: "ao-devkit-medium", displayName: "AO Dev-kit (Medium)", description: "4 vCPU / 16 GB", icon: "", parameters: [] },
				{ id: "t-2", name: "ao-devkit-large", displayName: "", description: "", icon: "", parameters: [] },
			],
			isLoading: false,
			isError: false,
		};
		renderSection();
		// Scope to the templates section: its rows sit in the section's grouped
		// card (dividers come from the card), so there is no separate list element.
		const list = within(screen.getByRole("heading", { name: "Templates on your Coder" }).closest("section")!);
		// Prefers displayName, with the short description as subtext.
		expect(list.getByText("AO Dev-kit (Medium)")).toBeInTheDocument();
		expect(list.getByText("4 vCPU / 16 GB")).toBeInTheDocument();
		// Falls back to the slug name when no displayName is set.
		expect(list.getByText("ao-devkit-large")).toBeInTheDocument();
	});
});

describe("CoderSettingsSection dev environment", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
		mocks.putOrgCoderConfig.mockReset().mockResolvedValue({ coderConfig: null });
		mocks.org.value = { id: "org-1" };
	});

	it("shows the workspace setup for a saved connection", () => {
		mocks.config.data = {
			...currentConfig, workerUser: "workspace", reuseExistingCheckout: true, workingDirectory: "~/work/{repo}",
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status", readyProbeTimeoutSeconds: 600,
		};
		renderSection();
		expect(screen.getByText("Workspace setup")).toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "Run the agent as the workspace user" })).toBeChecked();
		expect(screen.getByRole("switch", { name: "Use the template's existing checkout" })).toBeChecked();
		expect(screen.getByRole("switch", { name: "Keep the agent out of secret files (.env)" })).toBeChecked();
		expect(screen.getByRole("textbox", { name: "Working directory" })).toHaveValue("~/work/{repo}");
		expect(screen.getByRole("textbox", { name: "Workspace ready check" })).toHaveValue("grep -q '^Ready' ~/.bootstrap-status");
		expect(screen.getByRole("textbox", { name: "Ready check timeout (seconds)" })).toHaveValue("600");
		// Per-session commands are project settings, not connection settings.
		expect(screen.queryByRole("textbox", { name: "Startup script" })).toBeNull();
		expect(screen.getByText(/open the project's settings and choose Scripts/)).toBeInTheDocument();
		// Nothing changed and no new token: nothing to save.
		expect(screen.getByRole("button", { name: "Save and fetch templates" })).toBeDisabled();
	});

	it("hides the workspace setup until a connection is saved", () => {
		mocks.config.data = { ...currentConfig, tokenSet: false };
		renderSection();
		expect(screen.queryByText("Workspace setup")).toBeNull();
		expect(screen.queryByRole("switch", { name: "Run the agent as the workspace user" })).toBeNull();
	});

	it("enables checkout reuse and the working directory with the workspace user", async () => {
		mocks.config.data = currentConfig;
		const user = userEvent.setup();
		renderSection();
		const reuse = screen.getByRole("switch", { name: "Use the template's existing checkout" });
		expect(reuse).toBeDisabled();
		expect(reuse).not.toBeChecked();
		// A working directory without the workspace user is refused, not silently dropped.
		await user.type(screen.getByRole("textbox", { name: "Working directory" }), "~/work/{{repo}");
		expect(screen.getByText('Turn on "Run the agent as the workspace user" to set a working directory.')).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save and fetch templates" })).toBeDisabled();

		await user.click(screen.getByRole("switch", { name: "Run the agent as the workspace user" }));
		expect(reuse).toBeEnabled();
		expect(reuse).toBeChecked();
		expect(screen.queryByText('Turn on "Run the agent as the workspace user" to set a working directory.')).toBeNull();
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: undefined,
			templateId: "t-1",
			workerUser: "workspace",
			reuseExistingCheckout: true,
			workingDirectory: "~/work/{repo}",
			protectSecretFiles: true,
			readyProbe: "",
			readyProbeTimeoutSeconds: 0,
		});
	});

	it("validates the working directory and the ready check timeout", async () => {
		mocks.config.data = { ...currentConfig, workerUser: "workspace" };
		const user = userEvent.setup();
		renderSection();
		const save = screen.getByRole("button", { name: "Save and fetch templates" });

		const workingDirectory = screen.getByRole("textbox", { name: "Working directory" });
		await user.type(workingDirectory, "work/alice");
		expect(screen.getByText(INVALID_WORKING_DIRECTORY)).toBeInTheDocument();
		expect(save).toBeDisabled();
		await user.clear(workingDirectory);
		await user.type(workingDirectory, "~/work/../etc");
		expect(screen.getByText(INVALID_WORKING_DIRECTORY)).toBeInTheDocument();
		await user.clear(workingDirectory);
		await user.type(workingDirectory, "~/work/{{name}");
		expect(screen.getByText(INVALID_WORKING_DIRECTORY)).toBeInTheDocument();
		await user.clear(workingDirectory);
		await user.type(workingDirectory, "$HOME/work/alice");
		expect(screen.queryByText(INVALID_WORKING_DIRECTORY)).toBeNull();

		await user.type(screen.getByRole("textbox", { name: "Workspace ready check" }), "test -f ~/.ready");
		await user.type(screen.getByRole("textbox", { name: "Ready check timeout (seconds)" }), "5");
		expect(save).toBeDisabled();
		await user.clear(screen.getByRole("textbox", { name: "Ready check timeout (seconds)" }));
		await user.type(screen.getByRole("textbox", { name: "Ready check timeout (seconds)" }), "900");
		expect(save).toBeEnabled();
	});

	it("saves new workspace setup fields and keeps the stored ones", async () => {
		mocks.config.data = {
			...currentConfig, templateId: "t-1", startupTimeoutSeconds: 1800,
			workerUser: "workspace", reuseExistingCheckout: false, workingDirectory: "~/work/{repo}",
			readyProbe: "test -f ~/.ready",
		};
		const user = userEvent.setup();
		renderSection();
		await user.type(screen.getByRole("textbox", { name: "Ready check timeout (seconds)" }), "60");
		await user.click(screen.getByRole("switch", { name: "Keep the agent out of secret files (.env)" }));
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: undefined,
			templateId: "t-1",
			startupTimeoutSeconds: 1800,
			workerUser: "workspace",
			reuseExistingCheckout: false,
			workingDirectory: "~/work/{repo}",
			protectSecretFiles: false,
			readyProbe: "test -f ~/.ready",
			readyProbeTimeoutSeconds: 60,
		});
	});

	it("keeps the controls read-only for an org member", () => {
		mocks.org.value = { id: "org-1", role: "member" };
		mocks.config.data = currentConfig;
		renderSection();
		expect(screen.getByText("Only organization owners and admins can change these settings.")).toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "Run the agent as the workspace user" })).toBeDisabled();
		expect(screen.getByRole("textbox", { name: "Working directory" })).toBeDisabled();
	});

	it("keeps the stored dev-environment settings when a new token is saved", async () => {
		mocks.config.data = {
			...currentConfig, templateId: "t-1", parameters: { region: "eu" }, startupTimeoutSeconds: 1800,
			workerUser: "workspace", reuseExistingCheckout: false,
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status", readyProbeTimeoutSeconds: 600,
		};
		const user = userEvent.setup();
		renderSection();
		await user.type(screen.getByLabelText("API token *"), "rotated");
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: "rotated",
			templateId: "t-1",
			parameters: { region: "eu" },
			startupTimeoutSeconds: 1800,
			// Carried through unchanged: a token rotation must not reset the workspace setup.
			workerUser: "workspace",
			reuseExistingCheckout: false,
			workingDirectory: "",
			protectSecretFiles: true,
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status",
			readyProbeTimeoutSeconds: 600,
		});
	});

	it("clears a stored ready check and turns the workspace user off on a current control plane", async () => {
		// A control plane that reports protectSecretFiles knows every field and keeps
		// the stored value of any field left out, so the save sends them all.
		mocks.config.data = {
			...currentConfig, templateId: "t-1",
			workerUser: "workspace", reuseExistingCheckout: false, workingDirectory: "~/work/{repo}",
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status", readyProbeTimeoutSeconds: 600,
		};
		const user = userEvent.setup();
		renderSection();
		await user.clear(screen.getByRole("textbox", { name: "Workspace ready check" }));
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenLastCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: undefined,
			templateId: "t-1",
			workerUser: "workspace",
			reuseExistingCheckout: false,
			workingDirectory: "~/work/{repo}",
			protectSecretFiles: true,
			readyProbe: "",
			readyProbeTimeoutSeconds: 0,
		});

		await user.clear(screen.getByRole("textbox", { name: "Working directory" }));
		await user.click(screen.getByRole("switch", { name: "Run the agent as the workspace user" }));
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(2));
		expect(mocks.putOrgCoderConfig).toHaveBeenLastCalledWith("org-1", expect.objectContaining({
			workerUser: "ao-worker",
			reuseExistingCheckout: false,
			workingDirectory: "",
			readyProbe: "",
		}));
	});

	it("hides the newer fields and still clears the older ones on an older control plane", async () => {
		// An older control plane does not report protectSecretFiles and rejects
		// workingDirectory and protectSecretFiles; it knows the other fields.
		mocks.config.data = {
			...savedConfig, workerUser: "workspace", reuseExistingCheckout: false,
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status", readyProbeTimeoutSeconds: 600,
		};
		const user = userEvent.setup();
		renderSection();
		expect(screen.queryByRole("textbox", { name: "Working directory" })).toBeNull();
		expect(screen.queryByRole("switch", { name: "Keep the agent out of secret files (.env)" })).toBeNull();
		await user.clear(screen.getByRole("textbox", { name: "Workspace ready check" }));
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: undefined,
			templateId: "t-1",
			workerUser: "workspace",
			reuseExistingCheckout: false,
			readyProbe: "",
			readyProbeTimeoutSeconds: 0,
		});
	});

	it("sends no dev-environment fields while they are at their defaults", async () => {
		mocks.config.data = undefined;
		const user = userEvent.setup();
		renderSection();
		await user.type(screen.getByLabelText("Coder URL or IP *"), "https://coder.test");
		await user.type(screen.getByLabelText("API token *"), "secret");
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", { baseUrl: "https://coder.test", token: "secret" });
	});
});

describe("CoderSettingsSection removing the connection", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
		mocks.putOrgCoderConfig.mockReset().mockResolvedValue({ coderConfig: null });
		mocks.deleteOrgCoderConfig.mockReset().mockResolvedValue(undefined);
		mocks.org.value = { id: "org-1", role: "admin" };
	});

	it("removes a stored connection after confirming, then refreshes the config and the offering", async () => {
		mocks.config.data = currentConfig;
		const queryClient = new QueryClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");
		const user = userEvent.setup();
		renderSection(queryClient);
		await user.click(screen.getByRole("button", { name: "Remove connection" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText(/New Coder sessions will run on AO's managed Coder/)).toBeInTheDocument();
		expect(mocks.deleteOrgCoderConfig).not.toHaveBeenCalled();
		await user.click(within(dialog).getByRole("button", { name: "Remove" }));
		await waitFor(() => expect(mocks.deleteOrgCoderConfig).toHaveBeenCalledWith("org-1"));
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
		const keys = invalidate.mock.calls.map(([filters]) => filters?.queryKey);
		expect(keys).toEqual(expect.arrayContaining([["cloud-org-coder-config"], ["cloud-coder-templates"], ["cloud-me"]]));
	});

	it("keeps the dialog open with the reason when the control plane refuses", async () => {
		mocks.config.data = currentConfig;
		mocks.deleteOrgCoderConfig.mockRejectedValue(new Error("Only organization admins can do that."));
		const user = userEvent.setup();
		renderSection();
		await user.click(screen.getByRole("button", { name: "Remove connection" }));
		const dialog = await screen.findByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Remove" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("Only organization admins can do that.");
	});

	it("explains a connection that sessions still run on is kept", async () => {
		mocks.config.data = currentConfig;
		mocks.deleteOrgCoderConfig.mockRejectedValue(new CloudCpError(
			"Sessions still run on this Coder connection. Remove their projects or sessions first.",
			{ status: 409, code: "connection_in_use" },
		));
		const user = userEvent.setup();
		renderSection();
		await user.click(screen.getByRole("button", { name: "Remove connection" }));
		const dialog = await screen.findByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Remove" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent(
			"Sessions still run on this Coder connection. Remove their projects or sessions first.",
		);
		// The connection stays on the page.
		expect(screen.getByLabelText("Coder URL or IP *")).toHaveValue("https://coder.test");
	});

	it("offers no removal without a stored connection", () => {
		mocks.config.data = undefined;
		renderSection();
		expect(screen.queryByRole("button", { name: "Remove connection" })).toBeNull();
	});

	it("offers no removal to a member, who cannot change the connection", () => {
		mocks.config.data = currentConfig;
		mocks.org.value = { id: "org-1", role: "member" };
		renderSection();
		expect(screen.queryByRole("button", { name: "Remove connection" })).toBeNull();
	});

	it("keeps an org that may save on the editable page, with Remove beside Save", () => {
		mocks.config.data = { ...currentConfig, editable: true };
		renderSection();
		expect(screen.queryByText(/can no longer connect its own Coder/)).toBeNull();
		expect(screen.getByLabelText("Coder URL or IP *")).toBeEnabled();
		expect(screen.getByRole("button", { name: "Save and fetch templates" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove connection" })).toBeInTheDocument();
	});

	it("shows an org that lost the coder capability its connection read-only, with only Remove", () => {
		mocks.config.data = { ...currentConfig, editable: false, workerUser: "workspace" };
		renderSection();
		expect(screen.getByText("Your organization can no longer connect its own Coder. You can remove this connection.")).toBeInTheDocument();
		expect(screen.getByLabelText("Coder URL or IP *")).toBeDisabled();
		expect(screen.getByLabelText("API token *")).toBeDisabled();
		expect(screen.getByRole("switch", { name: "Run the agent as the workspace user" })).toBeDisabled();
		expect(screen.queryByRole("button", { name: "Save and fetch templates" })).toBeNull();
		expect(screen.queryByRole("button", { name: "Create a token in Coder" })).toBeNull();
		expect(screen.queryByText("Need a token?")).toBeNull();
		expect(screen.getByRole("button", { name: "Remove connection" })).toBeInTheDocument();
	});
});
