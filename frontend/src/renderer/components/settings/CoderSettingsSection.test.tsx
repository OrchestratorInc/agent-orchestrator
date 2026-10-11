import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { UseCoderTemplatesResult } from "../../hooks/useCoderTemplates";
import { CoderSettingsSection } from "./CoderSettingsSection";

// Mutable mock state so each test drives the loaded config and the template list
// independently. The section under test is gated on a saved connection
// (tokenSet) and rendered from the useCoderTemplates result.
const mocks = vi.hoisted(() => ({
	config: { data: undefined as unknown },
	templates: { value: { templates: [], isLoading: false, isError: false } as UseCoderTemplatesResult },
	putOrgCoderConfig: vi.fn(),
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "authenticated" }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { putOrgCoderConfig: mocks.putOrgCoderConfig }, ready: true, baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));
vi.mock("../../hooks/useOrgCoderConfig", () => ({
	orgCoderConfigQueryKey: ["cloud-org-coder-config"],
	useOrgCoderConfig: () => mocks.config,
}));
vi.mock("../../hooks/useCoderTemplates", () => ({ useCoderTemplates: () => mocks.templates.value }));

const savedConfig = { baseUrl: "https://coder.test", owner: "me", defaultTemplateId: "t-1", tokenSet: true };

function renderSection() {
	render(
		<QueryClientProvider client={new QueryClient()}>
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
		// Scope to the read-only list: the selected template's name also appears in
		// the default-template dropdown trigger above it.
		const list = within(screen.getByRole("list", { name: "Templates on your Coder" }));
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
	});

	it("reads the stored dev-environment settings back", () => {
		mocks.config.data = {
			...savedConfig, workerUser: "workspace", reuseExistingCheckout: false,
			readyProbe: "grep -q '^Ready' ~/.bootstrap-status", readyProbeTimeoutSeconds: 600,
		};
		renderSection();
		expect(screen.getByRole("switch", { name: "Run the agent as the workspace user" })).toBeChecked();
		const reuse = screen.getByRole("switch", { name: "Use the template's existing checkout" });
		expect(reuse).toBeEnabled();
		expect(reuse).not.toBeChecked();
		expect(screen.getByRole("textbox", { name: "Workspace ready check" })).toHaveValue("grep -q '^Ready' ~/.bootstrap-status");
		expect(screen.getByRole("spinbutton", { name: "Ready check timeout (seconds)" })).toHaveValue(600);
		// Nothing changed and no new token: nothing to save.
		expect(screen.getByRole("button", { name: "Save and fetch templates" })).toBeDisabled();
	});

	it("locks the checkout switch until the agent runs as the workspace user", async () => {
		mocks.config.data = savedConfig;
		const user = userEvent.setup();
		renderSection();
		const reuse = screen.getByRole("switch", { name: "Use the template's existing checkout" });
		expect(reuse).toBeDisabled();
		expect(reuse).not.toBeChecked();
		await user.click(screen.getByRole("switch", { name: "Run the agent as the workspace user" }));
		expect(reuse).toBeEnabled();
		// Reusing the checkout defaults on with the workspace user.
		expect(reuse).toBeChecked();
	});

	it("saves dev-environment edits on a stored connection without a new token", async () => {
		mocks.config.data = { ...savedConfig, templateId: "t-1", parameters: { region: "eu" }, startupTimeoutSeconds: 1800 };
		const user = userEvent.setup();
		renderSection();
		await user.click(screen.getByRole("switch", { name: "Run the agent as the workspace user" }));
		await user.click(screen.getByRole("switch", { name: "Use the template's existing checkout" }));
		await user.type(screen.getByRole("textbox", { name: "Workspace ready check" }), "test -f ~/.ready");
		await user.type(screen.getByRole("spinbutton", { name: "Ready check timeout (seconds)" }), "300");
		await user.click(screen.getByRole("button", { name: "Save and fetch templates" }));
		await waitFor(() => expect(mocks.putOrgCoderConfig).toHaveBeenCalledTimes(1));
		expect(mocks.putOrgCoderConfig).toHaveBeenCalledWith("org-1", {
			baseUrl: "https://coder.test",
			token: undefined,
			// Stored settings the form does not edit ride along so the save keeps them.
			templateId: "t-1",
			parameters: { region: "eu" },
			startupTimeoutSeconds: 1800,
			workerUser: "workspace",
			reuseExistingCheckout: false,
			readyProbe: "test -f ~/.ready",
			readyProbeTimeoutSeconds: 300,
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

	it("blocks save on an out-of-range ready check timeout", async () => {
		mocks.config.data = savedConfig;
		const user = userEvent.setup();
		renderSection();
		await user.type(screen.getByRole("textbox", { name: "Workspace ready check" }), "true");
		await user.type(screen.getByRole("spinbutton", { name: "Ready check timeout (seconds)" }), "5");
		expect(screen.getByRole("alert")).toHaveTextContent("Enter a whole number of seconds from 30 to 3600.");
		expect(screen.getByRole("button", { name: "Save and fetch templates" })).toBeDisabled();
	});
});
