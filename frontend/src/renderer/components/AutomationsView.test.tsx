import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { AUTOMATION_TEMPLATES } from "../lib/automation-templates";
import { AutomationsView } from "./AutomationsView";
import { useUiStore } from "../stores/ui-store";
import { CloudCpError } from "../lib/cloud-cp";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const mocks = vi.hoisted(() => ({
	automations: [] as Array<Record<string, unknown>>,
	create: vi.fn(),
	update: vi.fn(),
	runsError: null as Error | null,
	capture: vi.fn(),
	defaultWorker: "codex",
	defaultsLoaded: true,
	fallbackAmp: false,
	workerTuning: {} as { model?: string; mode?: string; effort?: string },
	baseTuning: {} as { model?: string; mode?: string; effort?: string },
	workspaces: [] as Array<Record<string, unknown>>,
	cloudAvailable: false,
	cloudAutomations: [] as Array<Record<string, unknown>>,
	createCloud: vi.fn(),
	runNow: vi.fn(),
	subscribe: vi.fn(),
	updateCloud: vi.fn(),
	cloudUserId: "u1",
	cloudRuns: [] as Array<Record<string, unknown>>,
	navigate: vi.fn(),
	providerConnections: undefined as Array<{ provider: string; validationState: string }> | undefined,
	createCloudError: null as Error | null,
	createCloudVariables: undefined as Record<string, unknown> | undefined,
	updateCloudError: null as Error | null,
	removeCloud: vi.fn(),
	cloudProjectsLoaded: false,
}));

vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: mocks.capture,
}));

vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));
const codexSnapshot = { id: "codex", label: "Codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready", usageCount: 3, lastUsedAt: null };
const ampSnapshot = { ...codexSnapshot, id: "amp", label: "Amp" };
const cursorSnapshot = { id: "cursor", label: "Cursor", installation: { state: "not_installed", freshness: "fresh" }, authentication: { state: "unknown", freshness: "stale" }, effectiveReadiness: "not_ready", usageCount: 0, lastUsedAt: null };
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [mocks.fallbackAmp ? { ...codexSnapshot, effectiveReadiness: "not_ready" } : codexSnapshot, cursorSnapshot, ampSnapshot] } }) }));
vi.mock("../hooks/useProjectDefaultWorker", () => ({ useProjectWorkerDefaults: () => ({ loaded: mocks.defaultsLoaded, harness: mocks.defaultWorker, agentConfig: mocks.baseTuning, worker: { agent: mocks.defaultWorker, agentConfig: mocks.workerTuning } }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: mocks.workspaces }), useCloudProjectsQuery: () => ({ data: mocks.cloudProjectsLoaded ? [] : undefined }) }));
vi.mock("../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryKey: (agentId: string, projectId: string) => ["agent-models", agentId, projectId],
	agentModelsQueryOptions: (agentId: string, projectId: string) => ({
		queryKey: ["agent-models", agentId, projectId],
		queryFn: async () => agentId === "amp" ? ({ models: [{ id: "smart", label: "Smart", isDefault: true }, { id: "rush", label: "Rush" }], allowCustom: false, customModelEntry: "none", selectionMode: "mode" }) : ({
			models: [{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["low", "high"], defaultEffort: "high" }, { id: "capable", label: "Capable", efforts: ["low", "high"], defaultEffort: "high" }, { id: "plain", label: "Plain", efforts: [] }, { id: "project-model", label: "Project model", efforts: ["minimal", "low"], defaultEffort: "low" }],
			allowCustom: false,
			customModelEntry: "none",
			selectionMode: "catalog",
		}),
		enabled: agentId !== "",
	}),
	refreshAgentModels: vi.fn(),
	revalidateAgentModels: vi.fn(),
}));
vi.mock("../hooks/useSettings", () => ({ useSettings: () => ({ settings: {} }) }));
vi.mock("../hooks/useAutomations", () => ({
	useAutomations: () => ({ data: mocks.automations, isLoading: false, error: null }),
	useCreateAutomation: () => ({ mutateAsync: mocks.create, isPending: false, error: null }),
	useUpdateAutomation: () => ({ mutateAsync: mocks.update, isPending: false, error: null }),
	useDeleteAutomation: () => ({ mutate: vi.fn(), isPending: false, error: null }),
	useAutomationRuns: () => ({ data: [], isLoading: false, error: mocks.runsError }),
}));

vi.mock("../hooks/useProviderConnections", () => ({ useProviderConnections: () => ({ data: mocks.providerConnections }) }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ chatHarnesses: ["claude-code", "codex", "cursor"] }) }));
vi.mock("../hooks/useCloudAutomations", async (importOriginal) => ({
	...await importOriginal<typeof import("../hooks/useCloudAutomations")>(),
	useCloudAutomations: () => ({ data: mocks.cloudAvailable ? mocks.cloudAutomations : undefined, available: mocks.cloudAvailable, orgId: "org-1", userId: mocks.cloudUserId, isLoading: false, error: null }),
	useCreateCloudAutomation: () => ({ mutateAsync: mocks.createCloud, reset: vi.fn(), isPending: false, error: mocks.createCloudError, variables: mocks.createCloudVariables }),
	useUpdateCloudAutomation: () => ({ mutateAsync: mocks.updateCloud, reset: vi.fn(), isPending: false, error: mocks.updateCloudError }),
	useDeleteCloudAutomation: () => ({ mutate: mocks.removeCloud, reset: vi.fn(), isPending: false, error: null }),
	useRunCloudAutomationNow: () => ({ mutateAsync: mocks.runNow, isPending: false }),
	useSubscribeCloudAutomation: () => ({ mutateAsync: mocks.subscribe, isPending: false }),
	useCloudAutomationRuns: () => ({ data: mocks.cloudRuns, isLoading: false, error: null }),
}));

async function pickTime(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, hour: string, minute: string) {
	await user.click(within(dialog).getByRole("combobox", { name: "Hour" }));
	await user.click(await screen.findByRole("option", { name: hour }));
	await user.click(within(dialog).getByRole("combobox", { name: "Minute" }));
	await user.click(await screen.findByRole("option", { name: minute }));
}

function renderView(ui: ReactNode = <AutomationsView />) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(ui, { wrapper: ({ children }) => <QueryClientProvider client={client}><TooltipProvider>{children}</TooltipProvider></QueryClientProvider> });
}

/** Opens a blank create form through the page's New automation menu. */
async function openBlankCreate(user: Pick<typeof userEvent, "click">) {
	// With automations the toolbar's menu holds the action; the empty state has a single plain button.
	const trigger = screen.getByRole("button", { name: "New automation" });
	await user.click(trigger);
	if (trigger.getAttribute("aria-haspopup") === "menu") await user.click(await screen.findByRole("menuitem", { name: "Start from scratch" }));
}

describe("AutomationsView", () => {
	beforeEach(() => { mocks.workspaces = [{ id: "demo", name: "Demo" }]; mocks.defaultWorker = "codex"; mocks.defaultsLoaded = true; mocks.fallbackAmp = false; mocks.workerTuning = {}; mocks.baseTuning = {}; mocks.automations = []; mocks.runsError = null; mocks.cloudAvailable = false; mocks.cloudAutomations = []; mocks.providerConnections = undefined; mocks.cloudUserId = "u1"; mocks.cloudRuns = []; mocks.createCloudError = null; mocks.createCloudVariables = undefined; mocks.updateCloudError = null; mocks.removeCloud.mockReset(); mocks.cloudProjectsLoaded = false; mocks.updateCloud.mockReset(); mocks.createCloud.mockReset(); mocks.runNow.mockReset(); mocks.subscribe.mockReset(); mocks.create.mockReset(); mocks.update.mockReset(); mocks.capture.mockReset(); });

	it("shows a discoverable empty state and create action", () => {
		renderView();
		expect(screen.getByRole("heading", { name: "Automations" })).toBeInTheDocument();
		expect(screen.getByText("No automations yet")).toBeInTheDocument();
		// The empty state owns the only create action; the toolbar waits for a list.
		expect(screen.getAllByRole("button", { name: "New automation" })).toHaveLength(1);
		expect(screen.queryByRole("button", { name: "Search automations" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Filter automations" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Sort" })).not.toBeInTheDocument();
		// Starters lead the page; the empty message sits below them.
		const starters = screen.getByRole("heading", { name: "Start with a template" });
		expect(starters.compareDocumentPosition(screen.getByText("No automations yet")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it("always shows the page title in the topbar", () => {
		renderView();
		const title = screen.getByRole("heading", { name: "Automations" });
		expect(title).not.toHaveClass("sr-only");
		expect(title.closest("header")).toHaveClass("workspace-topbar-container");
		expect(screen.queryByRole("tab")).not.toBeInTheDocument();
	});

	it("offers a blank form and every starter from the New automation menu once automations exist", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "a1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true }];
		renderView();
		await user.click(screen.getByRole("button", { name: "New automation" }));
		expect(await screen.findByRole("menuitem", { name: "Start from scratch" })).toBeInTheDocument();
		for (const name of ["PR review inbox", "Daily issue triage", "Weekly release notes"]) {
			expect(screen.getByRole("menuitem", { name })).toBeInTheDocument();
		}
		await user.click(screen.getByRole("menuitem", { name: "Weekly release notes" }));
		expect(within(screen.getByRole("dialog")).getByRole("textbox", { name: "Name" })).toHaveValue("Weekly release notes");
	});

	it("opens a blank form from the empty state's New automation button", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "New automation" }));
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
		expect(within(screen.getByRole("dialog")).getByRole("textbox", { name: "Name" })).toHaveValue("");
	});

	it("keeps the starters at the top of the page before and after automations exist", () => {
		const { unmount } = renderView();
		expect(screen.getByRole("heading", { name: "Start with a template" })).toBeInTheDocument();
		expect(screen.getByText("Pick a starting point, make it yours, and let your agent take it from there.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "PR review inbox" })).toHaveTextContent("Weekdays at 9:00 AM");
		expect(screen.getByRole("button", { name: "Daily issue triage" })).toHaveTextContent("Daily at 9:15 AM");
		expect(screen.getByRole("button", { name: "Weekly release notes" })).toHaveTextContent("Every Friday at 4:00 PM");
		unmount();
		mocks.automations = [{ id: "a1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true }];
		renderView();
		const row = screen.getByRole("heading", { name: "Morning triage" });
		const starters = screen.getByRole("heading", { name: "Start with a template" });
		expect(screen.getByRole("button", { name: "PR review inbox" })).toBeInTheDocument();
		// Starters stay first; the list and its toolbar follow them.
		expect(starters.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(starters.compareDocumentPosition(screen.getByRole("group", { name: "Status" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.queryByText("No automations yet")).not.toBeInTheDocument();
	});

	it("preselects the project's resolved default worker like New Task", async () => {
		renderView();
		await openBlankCreate(userEvent);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
		expect(await within(dialog).findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5");
		expect(screen.queryByRole("option", { name: "Project default" })).not.toBeInTheDocument();
		expect(screen.queryByRole("combobox", { name: "Session kind" })).not.toBeInTheDocument();
	});

	it("offers no Cloud project or Cloud standalone agent as an automation's project", async () => {
		// The local daemon runs automations and answers "Unknown project" for a Cloud one.
		mocks.workspaces = [
			{ id: "demo", name: "Demo" },
			{ id: "cloud-1", name: "Cloud app", kind: "cloud", cloudOrgId: "org-1", sessions: [] },
			{ id: "agent-1", name: "Fix the flaky test", kind: "cloud", standaloneAgent: true, sessions: [] },
		];
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		expect(await screen.findByRole("option", { name: "Demo" })).toBeInTheDocument();
		expect(screen.queryByRole("option", { name: "Fix the flaky test" })).not.toBeInTheDocument();
		expect(screen.queryByRole("option", { name: "Cloud app" })).not.toBeInTheDocument();
	});

	it("edits an automation from its card action", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", harness: "codex", model: "gpt-5", effort: "high", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Morning triage" }));

		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Morning triage");
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue("Review");
		expect(within(dialog).getByRole("combobox", { name: "Project" })).toBeDisabled();
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
		expect(within(dialog).getByRole("combobox", { name: "Hour" })).toHaveTextContent("09");
		expect(within(dialog).getByRole("combobox", { name: "Minute" })).toHaveTextContent("30");

		const name = within(dialog).getByRole("textbox", { name: "Name" });
		await user.clear(name);
		await user.type(name, "Evening triage");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

		expect(mocks.update).toHaveBeenCalledWith({
			id: "automation-1",
			body: expect.objectContaining({
				displayName: "Evening triage",
				prompt: "Review",
			}),
		});
		expect(within(dialog).queryByRole("combobox", { name: "Start from" })).not.toBeInTheDocument();
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("templateId");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("projectId");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("timezone");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("harness");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("rrule");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("model");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("effort");
	});

	it("saves model and effort overrides for a new automation", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(await within(dialog).findByRole("button", { name: "Model" }));
		await user.click(screen.getByRole("menuitem", { name: "Capable" }));
		expect(within(dialog).getByRole("button", { name: "Model" })).toHaveClass("composer-chip");
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveClass("composer-chip");
		await user.click(within(dialog).getByRole("button", { name: "Effort" }));
		await user.click(await screen.findByRole("menuitemradio", { name: "Low" }));
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ model: "capable", effort: "low" }));
	});

	it("clears saved effort when editing to a model without effort support", async () => {
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "codex", model: "capable", effort: "high", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		const picker = await within(dialog).findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("Capable");
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveTextContent("High");
		await user.click(picker);
		await user.click(screen.getByRole("menuitem", { name: "Plain" }));
		expect(picker).toHaveTextContent("Plain");
		expect(within(dialog).queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ model: "plain", effort: "" }) });
	});

	it("enables saving after switching an invalid effort to a mode-based agent", async () => {
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "codex", model: "capable", effort: "unsupported", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeDisabled());
		await user.click(within(dialog).getByRole("combobox", { name: "Agent" }));
		await user.click(screen.getByRole("option", { name: /Amp/ }));
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Smart"));
		expect(within(dialog).queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
		expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeEnabled();
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ harness: "amp", model: "", effort: "" }) });
	});

	it("pins the catalog harness when adding a model to an unpinned automation", async () => {
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		await user.click(await within(dialog).findByRole("button", { name: "Model" }));
		await user.click(screen.getByRole("menuitem", { name: "Capable" }));
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ harness: "codex", model: "capable" }) });
	});

	it.each(["effort", "mode"] as const)("pins the harness for an %s-only edit", async (field) => {
		mocks.defaultWorker = field === "mode" ? "amp" : "codex";
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "", model: field === "effort" ? "capable" : "", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		if (field === "effort") {
			await user.click(await within(dialog).findByRole("button", { name: "Effort" }));
			await user.click(screen.getByRole("menuitemradio", { name: "Low" }));
		} else {
			await user.click(await within(dialog).findByRole("button", { name: "Model" }));
			await user.click(screen.getByRole("menuitem", { name: "Rush" }));
		}
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ harness: mocks.defaultWorker, [field]: field === "effort" ? "low" : "rush" }) });
		expect(mocks.update.mock.calls[0][0].body).toMatchObject({ model: field === "effort" ? "capable" : "", mode: field === "mode" ? "rush" : "", effort: field === "effort" ? "low" : "" });
	});

	it("clears fallback tuning when the project default harness resolves later", async () => {
		mocks.defaultWorker = "";
		const user = userEvent.setup();
		const view = renderView();
		await openBlankCreate(user);
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(await within(dialog).findByRole("button", { name: "Model" }));
		await user.click(screen.getByRole("menuitem", { name: "Capable" }));
		await user.click(within(dialog).getByRole("button", { name: "Effort" }));
		await user.click(screen.getByRole("menuitemradio", { name: "Low" }));
		mocks.defaultWorker = "amp";
		view.rerender(<AutomationsView />);
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Smart"));
		expect(within(dialog).queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ harness: "amp", model: "", mode: "", effort: "" }));
	});

	it.each(["worker", "base"] as const)("shows inherited %s model and effort and pins them on an effort edit", async (source) => {
		if (source === "worker") mocks.workerTuning = { model: "capable", effort: "low" };
		else mocks.baseTuning = { model: "capable", effort: "low" };
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Capable"));
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveTextContent("Low");
		await user.click(within(dialog).getByRole("button", { name: "Effort" }));
		await user.click(screen.getByRole("menuitemradio", { name: "High" }));
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ harness: "codex", model: "capable", effort: "high" }) });
	});

	it("offers effort levels from the inherited model rather than the catalog default", async () => {
		mocks.workerTuning = { model: "project-model", effort: "minimal" };
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Project model"));
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveTextContent("Minimal");
		await user.click(within(dialog).getByRole("button", { name: "Effort" }));
		expect(screen.queryByRole("menuitemradio", { name: "High" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("menuitemradio", { name: "Low" }));
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: expect.objectContaining({ harness: "codex", model: "project-model", effort: "low" }) });
	});

	it("preserves inherited tuning on an unrelated edit", async () => {
		mocks.workerTuning = { model: "capable", effort: "low" };
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Capable"));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), " renamed");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("model");
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("effort");
	});

	it("preserves hydrated unpinned tuning until project defaults finish loading", async () => {
		mocks.defaultWorker = "";
		mocks.defaultsLoaded = false;
		mocks.fallbackAmp = true;
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Review", prompt: "Review", kind: "worker", harness: "", model: "capable", effort: "low", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		const view = renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeDisabled();
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), " renamed");
		mocks.defaultWorker = "codex";
		mocks.defaultsLoaded = true;
		view.rerender(<AutomationsView />);
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Capable"));
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveTextContent("Low");
		expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeEnabled();
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: { displayName: "Review renamed", prompt: "Review" } });
	});

	it("allows renaming an unpinned automation after missing-project defaults fail", async () => {
		mocks.defaultWorker = "";
		mocks.defaultsLoaded = false;
		mocks.automations = [{ id: "automation-1", projectId: "missing", displayName: "Review", prompt: "Review", kind: "worker", harness: "", model: "capable", effort: "low", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		const user = userEvent.setup();
		const view = renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("combobox", { name: "Project" })).toHaveTextContent("missing");
		expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeDisabled();
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), " renamed");
		// A settled error provides no defaults; the fallback and saved tuning remain.
		mocks.defaultsLoaded = true;
		view.rerender(<AutomationsView />);
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Save changes" })).toBeEnabled());
		expect(within(dialog).getByRole("button", { name: "Model" })).toHaveTextContent("Capable");
		expect(within(dialog).getByRole("button", { name: "Effort" })).toHaveTextContent("Low");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "automation-1", body: { displayName: "Review renamed", prompt: "Review" } });
	});

	it("creates a weekly automation on the chosen weekday", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Friday review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(within(dialog).getByRole("combobox", { name: "Schedule" }));
		await user.click(screen.getByRole("option", { name: "Weekly" }));
		await user.click(within(dialog).getByRole("combobox", { name: "Day" }));
		await user.click(screen.getByRole("option", { name: "Friday" }));
		await pickTime(user, dialog, "09", "30");
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));

		expect(mocks.create).toHaveBeenCalledWith(
			expect.objectContaining({
				projectId: "demo",
				displayName: "Friday review",
				prompt: "Review the board",
				rrule: "FREQ=WEEKLY;BYDAY=FR;BYHOUR=9;BYMINUTE=30;BYSECOND=0",
			}),
		);
	});

	it("reports the create funnel from dialog open through success", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		expect(mocks.capture).toHaveBeenCalledWith("ao.automations.create_opened");

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Friday review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));

		const funnel = { project_id: "demo", schedule_preset: "daily", prompt_length_bucket: "xs" };
		expect(mocks.capture).not.toHaveBeenCalledWith("ao.renderer.automation_create_requested", expect.anything());
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.automation_create_succeeded", funnel);
		expect(mocks.capture).not.toHaveBeenCalledWith("ao.renderer.automation_create_failed", expect.anything());
	});

	it("reports a failed create and keeps the dialog open with its error", async () => {
		const user = userEvent.setup();
		mocks.create.mockRejectedValueOnce(new Error("daemon unreachable"));
		renderView();
		await openBlankCreate(user);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Friday review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));

		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.automation_create_failed", expect.objectContaining({ project_id: "demo" }));
		expect(mocks.capture).not.toHaveBeenCalledWith("ao.renderer.automation_create_succeeded", expect.anything());
		expect(screen.getByRole("dialog", { name: "Create automation" })).toBeInTheDocument();
	});

	it("reports no create funnel while editing an existing automation", async () => {
		mocks.automations = [{ id: "a1", projectId: "demo", displayName: "Nightly", prompt: "Check CI", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: new Date().toISOString(), createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }];
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Nightly" }));

		expect(screen.getByRole("dialog", { name: "Edit automation" })).toBeInTheDocument();
		expect(mocks.capture).not.toHaveBeenCalled();
	});

	it("creates a custom weekly automation on the chosen weekday", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Wednesday review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(within(dialog).getByRole("combobox", { name: "Schedule" }));
		await user.click(screen.getByRole("option", { name: "Custom" }));
		await user.click(within(dialog).getByRole("combobox", { name: "Frequency" }));
		await user.click(screen.getByRole("option", { name: "Weekly" }));
		await user.click(within(dialog).getByRole("combobox", { name: "Day" }));
		await user.click(screen.getByRole("option", { name: "Wednesday" }));
		await pickTime(user, dialog, "16", "45");
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));

		expect(mocks.create).toHaveBeenCalledWith(
			expect.objectContaining({
				rrule: "FREQ=WEEKLY;BYDAY=WE;BYHOUR=16;BYMINUTE=45;BYSECOND=0",
			}),
		);
	});

	it("sends an updated weekly RRULE when the day changes", async () => {
		const user = userEvent.setup();
		const rrule = "FREQ=WEEKLY;BYDAY=MO;BYHOUR=9;BYMINUTE=0;BYSECOND=0";
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Weekly sync", prompt: "Review", kind: "worker", harness: "codex", rrule, timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Weekly sync" }));

		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		const day = within(dialog).getByRole("combobox", { name: "Day" });
		await user.click(day);
		await user.click(screen.getByRole("option", { name: "Friday" }));
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

		expect(mocks.update.mock.calls[0][0].body).toEqual(
			expect.objectContaining({
				rrule: "FREQ=WEEKLY;BYDAY=FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0",
			}),
		);
	});

	it("omits a custom recurrence rule when only non-schedule fields change", async () => {
		const user = userEvent.setup();
		const rrule = "DTSTART:20260826T090000Z\nRRULE:FREQ=DAILY;INTERVAL=2;BYHOUR=9;BYMINUTE=0;BYSECOND=0";
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Every other day", prompt: "Review", kind: "worker", harness: "codex", rrule, timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Every other day" }));

		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("combobox", { name: "Schedule" })).toHaveTextContent("Custom");
		expect(within(dialog).getByRole("combobox", { name: "Frequency" })).toBeInTheDocument();
		const name = within(dialog).getByRole("textbox", { name: "Name" });
		await user.clear(name);
		await user.type(name, "Renamed");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

		expect(mocks.update).toHaveBeenCalledWith({
			id: "automation-1",
			body: expect.objectContaining({
				displayName: "Renamed",
			}),
		});
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("rrule");
	});

	it.each([
		"FREQ=DAILY;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
		"FREQ=DAILY;BYHOUR=9;BYMINUTE=0,30;BYSECOND=0",
		"FREQ=WEEKLY;BYDAY=FR;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0,30;BYSECOND=0",
		"FREQ=MONTHLY;BYMONTHDAY=1,15;BYHOUR=9;BYMINUTE=0;BYSECOND=0",
		"FREQ=MONTHLY;BYMONTHDAY=-1;BYHOUR=9;BYMINUTE=0;BYSECOND=0",
		"FREQ=MONTHLY;BYMONTHDAY=1;BYHOUR=9,17;BYMINUTE=0;BYSECOND=0",
	])("preserves unsupported anchored schedule %s on a prompt-only edit", async (rule) => {
		const user = userEvent.setup();
		const rrule = `DTSTART:20261015T080000Z\nRRULE:${rule}`;
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "API schedule", prompt: "Review", kind: "worker", harness: "codex", rrule, timezone: "UTC", enabled: true }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit API schedule" }));
		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("combobox", { name: "Schedule" })).toHaveTextContent("Custom");
		const prompt = within(dialog).getByRole("textbox", { name: "Prompt" });
		await user.clear(prompt);
		await user.type(prompt, "Review open issues");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledExactlyOnceWith({
			id: "automation-1", body: { displayName: "API schedule", prompt: "Review open issues" },
		});
	});

	it("preserves an inherited harness when only unrelated fields change", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Inherited worker", prompt: "Review", kind: "worker", harness: "", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Inherited worker" }));

		const dialog = screen.getByRole("dialog", { name: "Edit automation" });
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
		const name = within(dialog).getByRole("textbox", { name: "Name" });
		await user.clear(name);
		await user.type(name, "Renamed inherited worker");
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));

		expect(mocks.update).toHaveBeenCalledWith({
			id: "automation-1",
			body: expect.objectContaining({
				displayName: "Renamed inherited worker",
			}),
		});
		expect(mocks.update.mock.calls[0][0].body).not.toHaveProperty("harness");
	});

	it("uses AO popup controls instead of native browser selects", async () => {
		const view = renderView();
		await openBlankCreate(userEvent);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(dialog.querySelector('select:not([aria-hidden="true"])')).toBeNull();
		expect(within(dialog).getByRole("combobox", { name: "Project" })).toBeInTheDocument();
		expect(within(dialog).getByRole("combobox", { name: "Schedule" })).toBeInTheDocument();
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toBeInTheDocument();
		expect(await within(dialog).findByRole("button", { name: "Model" })).toHaveTextContent("GPT-5");
		expect(view.container.querySelector('[data-slot="select-content"]')).toBeNull();
	});

	it("keeps a daily or weekly time's hour and minute together in one Time field", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		const field = within(dialog).getByText("Time").parentElement!;
		expect(within(field).getByRole("combobox", { name: "Hour" })).toBeInTheDocument();
		expect(within(field).getByRole("combobox", { name: "Minute" })).toBeInTheDocument();
		await user.click(within(dialog).getByRole("combobox", { name: "Schedule" }));
		await user.click(screen.getByRole("option", { name: "Weekly" }));
		const weekly = within(dialog).getByText("Time").parentElement!;
		expect(within(weekly).getByRole("combobox", { name: "Hour" })).toBeInTheDocument();
		expect(within(weekly).getByRole("combobox", { name: "Minute" })).toBeInTheDocument();
	});

	it("picks the time from 24-hour hour and minute lists", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(within(dialog).queryByRole("textbox", { name: "Time" })).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("combobox", { name: "Hour" }));
		expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual(Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, "0")));
		await user.click(screen.getByRole("option", { name: "07" }));
		await user.click(within(dialog).getByRole("combobox", { name: "Minute" }));
		expect(screen.getAllByRole("option")).toHaveLength(60);
		await user.click(screen.getByRole("option", { name: "05" }));
		expect(within(dialog).getByRole("combobox", { name: "Hour" })).toHaveTextContent("07");
		expect(within(dialog).getByRole("combobox", { name: "Minute" })).toHaveTextContent("05");
	});

	it("uses AO's shared centered dialog without a purpose selector", async () => {
		renderView();
		await openBlankCreate(userEvent);
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(dialog).toHaveClass("border-(--color-border-overlay)", "bg-popover", "rounded-lg");
		expect(dialog.querySelector(".border-b")).toBeNull();
		expect(dialog.querySelector(".border-t")).toBeNull();
		expect(within(dialog).getByRole("heading", { name: "Create automation" })).toHaveClass(
			"settings-dialog-title",
			"px-4",
			"pt-3",
		);
		expect(dialog).toHaveClass("left-1/2", "top-1/2");
		expect(dialog).not.toHaveClass("right-0", "h-dvh");
		expect(within(dialog).queryByText(/purpose|more options/i)).not.toBeInTheDocument();
	});

	it("shows AO-styled inline feedback when required fields are missing", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		await user.click(screen.getByRole("button", { name: "Create automation" }));

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		const project = within(dialog).getByRole("combobox", { name: "Project" });
		expect(within(dialog).getByRole("alert")).toHaveTextContent("Complete the highlighted fields.");
		expect(project).toHaveAttribute("aria-invalid", "true");
		expect(project).toHaveAccessibleDescription("Select a project.");
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveAccessibleDescription("Enter a name.");
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveAccessibleDescription("Enter a prompt.");
		expect(mocks.create).not.toHaveBeenCalled();
	});

	it("clears a field error as soon as the field is corrected", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		await user.click(screen.getByRole("button", { name: "Create automation" }));

		const project = screen.getByRole("combobox", { name: "Project" });
		expect(project).toHaveAttribute("aria-invalid", "true");
		await user.click(project);
		await user.click(screen.getByRole("option", { name: "Demo" }));

		expect(project).not.toHaveAttribute("aria-invalid");
		expect(screen.queryByText("Select a project.")).not.toBeInTheDocument();
	});

	it("keeps inline feedback until a text field has meaningful content", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		await user.click(screen.getByRole("button", { name: "Create automation" }));

		const name = screen.getByRole("textbox", { name: "Name" });
		expect(name).toHaveAccessibleDescription("Enter a name.");
		await user.type(name, " ");
		expect(name).toHaveAccessibleDescription("Enter a name.");
		await user.type(name, "Morning");
		expect(screen.queryByText("Enter a name.")).not.toBeInTheDocument();
	});

	it("defaults create time to the current local clock", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.setSystemTime(new Date(2026, 8, 22, 14, 7, 0));
		renderView();
		await openBlankCreate(userEvent);
		expect(screen.getByRole("combobox", { name: "Hour" })).toHaveTextContent("14");
		expect(screen.getByRole("combobox", { name: "Minute" })).toHaveTextContent("07");
		vi.useRealTimers();
	});

	it("filters paused automations and keeps schedule and project names readable", async () => {
		const base = { projectId: "demo", prompt: "Review the board", kind: "worker", harness: "codex", rrule: "FREQ=WEEKLY;BYDAY=FR;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC" };
		mocks.automations = [{ ...base, id: "active", displayName: "Friday review", enabled: true }, { ...base, id: "paused", displayName: "Paused review", enabled: false }];
		renderView();
		expect(screen.getAllByText("Demo")).toHaveLength(2);
		expect(screen.getAllByText("Every Friday at 9:30 AM")).toHaveLength(2);
		const status = screen.getByRole("group", { name: "Status" });
		expect(within(status).getByRole("button", { name: "All 2" })).toHaveAttribute("aria-pressed", "true");
		await userEvent.click(within(status).getByRole("button", { name: "Paused 1" }));
		expect(within(status).getByRole("button", { name: "Paused 1" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.queryByRole("heading", { name: "Friday review" })).not.toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Paused review" })).toBeInTheDocument();
		// The pressed pill is the status cue, so no status chip repeats it.
		expect(screen.queryByRole("button", { name: /Remove filter Status/ })).not.toBeInTheDocument();
		await userEvent.click(within(status).getByRole("button", { name: "Enabled 1" }));
		expect(screen.getByRole("heading", { name: "Friday review" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "Paused review" })).not.toBeInTheDocument();
	});

	it("offers a project filter only when automations span more than one project", async () => {
		const user = userEvent.setup();
		const base = { prompt: "Review", kind: "worker", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true };
		mocks.automations = [{ ...base, id: "a", projectId: "demo", displayName: "Demo triage" }];
		const { unmount } = renderView();
		expect(screen.queryByRole("button", { name: "Filter automations" })).not.toBeInTheDocument();
		unmount();
		mocks.automations = [{ ...base, id: "a", projectId: "demo", displayName: "Demo triage" }, { ...base, id: "b", projectId: "other", displayName: "Other triage" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Filter automations" }));
		expect(screen.queryByRole("menuitemradio", { name: /Paused/ })).not.toBeInTheDocument();
		await user.click(await screen.findByRole("menuitemradio", { name: "Demo" }));
		expect(screen.getByRole("heading", { name: "Demo triage" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "Other triage" })).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Remove filter Project: Demo" }));
		expect(screen.getByRole("heading", { name: "Other triage" })).toBeInTheDocument();
	});

	it("sorts by next run with paused automations last, or by name", async () => {
		const base = { projectId: "demo", prompt: "Review", kind: "worker", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC" };
		mocks.automations = [
			{ ...base, id: "a", displayName: "Alpha", enabled: false, nextRunAt: "2026-11-11T08:00:00Z" },
			{ ...base, id: "b", displayName: "Bravo", enabled: true, nextRunAt: "2026-11-12T09:00:00Z" },
			{ ...base, id: "c", displayName: "Charlie", enabled: true, nextRunAt: "2026-11-11T09:00:00Z" },
		];
		renderView();
		const names = () => screen.getAllByRole("article").map((row) => within(row).getByRole("heading", { level: 2 }).textContent);
		expect(names()).toEqual(["Charlie", "Bravo", "Alpha"]);
		await userEvent.click(screen.getByRole("button", { name: "Sort" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "Name" }));
		expect(names()).toEqual(["Alpha", "Bravo", "Charlie"]);
	});

	it.each([
		["FREQ=DAILY;BYHOUR=8;BYMINUTE=30;BYSECOND=0", "Daily at 8:30 AM"],
		["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0", "Weekdays at 9:00 AM"],
		["FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=10;BYMINUTE=0;BYSECOND=0", "Every Mon, Wed, Fri at 10:00 AM"],
		["FREQ=MONTHLY;BYMONTHDAY=1;BYHOUR=10;BYMINUTE=0;BYSECOND=0", "Monthly on day 1 at 10:00 AM"],
		["FREQ=HOURLY", "Repeats hourly"],
	])("shows the complete schedule from a canonical daemon rule %s", (rule, label) => {
		mocks.automations = [{ id: "canonical", projectId: "demo", displayName: "Scheduled review", prompt: "Review", kind: "worker", rrule: `DTSTART;TZID=Asia/Kolkata:20261001T183600\nRRULE:${rule}`, timezone: "Asia/Kolkata", enabled: true }];
		renderView();
		expect(within(screen.getByRole("article")).getByText(label)).toBeInTheDocument();
	});

	it("exposes details with expansion state and surfaces failed toggles", async () => {
		mocks.automations = [{ id: "active", projectId: "demo", displayName: "Morning triage", prompt: "Review the board", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true }];
		mocks.update.mockRejectedValue(new Error("Could not pause"));
		renderView();
		const expand = screen.getByRole("button", { name: /Show run history/ });
		expect(expand).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(expand);
		expect(screen.getByRole("button", { name: /Hide run history/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("Review the board")).toBeInTheDocument();
		expect(screen.getByText("Timezone: UTC")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("switch", { name: "Disable Morning triage" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "active", body: { enabled: false } });
		expect(screen.getByRole("alert")).toHaveTextContent("Could not pause");
	});

	it("surfaces run-history fetch failures instead of an empty list", async () => {
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY", timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
		mocks.runsError = new Error("daemon unavailable");
		renderView();
		await userEvent.click(screen.getByRole("button", { name: /show run history/i }));
		expect(screen.getByRole("alert")).toHaveTextContent("daemon unavailable");
	});
});

describe("automation starters", () => {
	beforeEach(() => { mocks.automations = []; mocks.create.mockReset(); mocks.update.mockReset(); mocks.capture.mockReset(); });
	it.each([
		["PR review inbox", 0], ["Daily issue triage", 1], ["Weekly release notes", 2],
	] as const)("prefills %s and saves only the edited form", async (label, index) => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: label }));
		const dialog = screen.getByRole("dialog");
		expect(mocks.create).not.toHaveBeenCalled();
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue(label);
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue(AUTOMATION_TEMPLATES[index].prompt);
		expect(within(dialog).getByText(/Requires GitHub read access/)).toBeInTheDocument();
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.click(await within(dialog).findByRole("button", { name: "Model" }));
		await user.click(screen.getByRole("menuitem", { name: "Capable" }));
		await user.click(within(dialog).getByRole("button", { name: "Effort" }));
		await user.click(screen.getByRole("menuitemradio", { name: "Low" }));
		fireEvent.change(within(dialog).getByRole("textbox", { name: "Prompt" }), { target: { value: "My revised instructions" } });
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
			templateId: AUTOMATION_TEMPLATES[index].id, useCase: AUTOMATION_TEMPLATES[index].useCase,
			prompt: "My revised instructions", rrule: AUTOMATION_TEMPLATES[index].rrule,
			model: "capable", effort: "low",
		}));
	});

	it("creates a custom automation without asking for a purpose", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);
		const dialog = screen.getByRole("dialog");
		expect(within(dialog).queryByRole("combobox", { name: "Start from" })).not.toBeInTheDocument();
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue("");
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		fireEvent.change(within(dialog).getByRole("textbox", { name: "Name" }), { target: { value: "Custom" } });
		fireEvent.change(within(dialog).getByRole("textbox", { name: "Prompt" }), { target: { value: "My task" } });
		expect(within(dialog).queryByText(/purpose|more options/i)).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ templateId: "custom", useCase: "unknown" }));
	});

	it("edits a starter without changing its stored classification or anchored schedule", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "starter", projectId: "demo", displayName: "Review inbox", prompt: "My edited review prompt", kind: "worker", harness: "codex", templateId: "daily_pr_review_v1", useCase: "pr_review", rrule: "DTSTART;TZID=UTC:20261015T080000\nRRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: "2026-10-16T09:00:00Z", createdAt: "2026-10-15T08:00:00Z", updatedAt: "2026-10-15T08:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Edit Review inbox" }));
		const dialog = screen.getByRole("dialog");
		expect(within(dialog).queryByRole("combobox", { name: "Start from" })).not.toBeInTheDocument();
		expect(within(dialog).getByRole("combobox", { name: "Frequency" })).toHaveTextContent("Weekdays (Mon–Fri)");
		expect(within(dialog).queryByText(/purpose|more options/i)).not.toBeInTheDocument();
		await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
		expect(mocks.update).toHaveBeenCalledExactlyOnceWith({ id: "starter", body: { displayName: "Review inbox", prompt: "My edited review prompt" } });
	});

	it("can cancel a starter, choose another, then reopen a blank custom form", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "PR review inbox" }));
		let dialog = screen.getByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await user.click(screen.getByRole("button", { name: "Daily issue triage" }));
		dialog = screen.getByRole("dialog");
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Daily issue triage");
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue(AUTOMATION_TEMPLATES[1].prompt);
		await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
		await openBlankCreate(user);
		dialog = screen.getByRole("dialog");
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("");
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue("");
		expect(mocks.create).not.toHaveBeenCalled();
	});

	it("searches saved automations by name and project", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "a1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0", timezone: "UTC", enabled: true, createdAt: "2026-10-15T08:00:00Z", updatedAt: "2026-10-15T08:00:00Z" }];
		renderView();
		await user.click(screen.getByRole("button", { name: "Search automations" }));
		const search = screen.getByRole("searchbox", { name: "Search automations" });
		await user.type(search, "missing");
		expect(screen.queryByText("Morning triage")).not.toBeInTheDocument();
		expect(screen.getByText("No automations match")).toBeInTheDocument();
		await user.clear(search);
		await user.type(search, "DEMO");
		expect(screen.getByText("Morning triage")).toBeInTheDocument();
	});

	it("validates prompt byte length before creating", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getByRole("button", { name: "PR review inbox" }));
		const dialog = screen.getByRole("dialog");
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		fireEvent.change(within(dialog).getByRole("textbox", { name: "Prompt" }), { target: { value: "界".repeat(1366) } });
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).not.toHaveBeenCalled();
		expect(within(dialog).getByText("Keep the prompt within 4,096 UTF-8 bytes.")).toBeInTheDocument();
	});
	describe("AO Cloud", () => {
		const cloudAutomation = {
			id: "c1", projectId: "cloud-1", displayName: "Nightly debt", prompt: "List the top 10 debt items.", harness: "codex", mode: "trusted",
			rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: "2026-11-15T07:00:00Z", maxRuntimeMinutes: 60,
			deleteSessionOnComplete: false, branch: "ao/automation-c1", runAs: { userId: "u2", displayName: "Priya" }, consecutiveFailures: 0, subscribed: false, canEdit: false,
			createdAt: "2026-11-14T00:00:00Z", updatedAt: "2026-11-14T00:00:00Z",
			latestRun: { id: "r1", automationId: "c1", scheduledFor: "2026-11-14T07:00:00Z", trigger: "schedule", status: "skipped", attemptCount: 1, errorCode: "SANDBOX_QUOTA_EXCEEDED", errorMessage: "full", createdAt: "2026-11-14T07:00:00Z" },
		};
		beforeEach(() => {
			mocks.workspaces = [{ id: "demo", name: "Demo" }, { id: "cloud-1", name: "Cloud app", kind: "cloud", cloudOrgId: "org-1" }];
			// "automation starters" does not reset the cloud mocks, so each test starts from none.
			mocks.cloudAvailable = false; mocks.cloudAutomations = []; mocks.providerConnections = undefined; mocks.cloudUserId = "u1"; mocks.cloudRuns = []; mocks.createCloudError = null; mocks.createCloudVariables = undefined; mocks.updateCloudError = null; mocks.removeCloud.mockReset(); mocks.cloudProjectsLoaded = false;
			mocks.createCloud.mockReset(); mocks.updateCloud.mockReset(); mocks.runNow.mockReset(); mocks.subscribe.mockReset();
		});

		it("offers no Runs on choice without the automations feature", async () => {
			const user = userEvent.setup();
			renderView();
			await openBlankCreate(user);
			expect(within(screen.getByRole("dialog")).queryByRole("combobox", { name: "Runs on" })).not.toBeInTheDocument();
		});

		it("names the local choice for any desktop platform, not only a Mac", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			renderView();
			await openBlankCreate(user);
			const runsOn = within(screen.getByRole("dialog")).getByRole("combobox", { name: "Runs on" });
			expect(runsOn).toHaveTextContent("This computer");
			await user.click(runsOn);
			expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual(["This computer", "AO Cloud"]);
		});

		it("creates a cloud automation in a cloud project with its rule and timezone", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.createCloud.mockResolvedValue({});
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			await user.click(dialog.getByRole("combobox", { name: "Project" }));
			// Only the organization's cloud projects run in AO Cloud.
			expect(screen.queryByRole("option", { name: "Demo" })).not.toBeInTheDocument();
			await user.click(await screen.findByRole("option", { name: "Cloud app" }));
			await user.type(dialog.getByRole("textbox", { name: "Name" }), "Nightly debt");
			await user.type(dialog.getByRole("textbox", { name: "Prompt" }), "List the top 10 debt items.");
			await pickTime(user, screen.getByRole("dialog"), "07", "00");
			await user.click(dialog.getByRole("button", { name: "Create automation" }));
			await waitFor(() => expect(mocks.createCloud).toHaveBeenCalled());
			expect(mocks.create).not.toHaveBeenCalled();
			expect(mocks.createCloud.mock.calls[0][0]).toMatchObject({
				projectId: "cloud-1", displayName: "Nightly debt", prompt: "List the top 10 debt items.", harness: "claude-code",
				rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
				// Off by default: the session stays, paused, for the next run.
				deleteSessionOnComplete: false,
			});
		});

		it("runs a standalone project (no repository) in AO Cloud, but not a standalone agent", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.createCloud.mockResolvedValue({});
			// A standalone project has no repositories; a standalone agent's project is the Scratchpad's.
			mocks.workspaces = [
				{ id: "demo", name: "Demo" },
				{ id: "cloud-blank", name: "Blank project", kind: "cloud", cloudOrgId: "org-1", workspaceRepos: [] },
				{ id: "cloud-agent", name: "My agent", kind: "cloud", cloudOrgId: "org-1", standaloneAgent: true },
			];
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			await user.click(dialog.getByRole("combobox", { name: "Project" }));
			expect(screen.queryByRole("option", { name: "My agent" })).not.toBeInTheDocument();
			await user.click(await screen.findByRole("option", { name: "Blank project" }));
			await user.type(dialog.getByRole("textbox", { name: "Name" }), "Scratch notes");
			await user.type(dialog.getByRole("textbox", { name: "Prompt" }), "Summarize yesterday.");
			await pickTime(user, screen.getByRole("dialog"), "07", "00");
			await user.click(dialog.getByRole("button", { name: "Create automation" }));
			await waitFor(() => expect(mocks.createCloud).toHaveBeenCalled());
			expect(mocks.createCloud.mock.calls[0][0]).toMatchObject({ projectId: "cloud-blank" });
			// Nothing about a repository or a GitHub token: it has none.
			expect(screen.queryByText(/GitHub/)).toBeNull();
		});

		it("shows an automation whose project was deleted as turned off and deletable, and says why a change or Run now is refused", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudProjectsLoaded = true;
			const deletedReason = "Turned off: its project was deleted. Delete the automation, or move its prompt to another project.";
			const refusal = () => new CloudCpError("This automation's project was deleted.", { status: 409, code: "AUTOMATION_PROJECT_DELETED" });
			mocks.cloudAutomations = [{
				...cloudAutomation, projectId: "cloud-gone", enabled: false, nextRunAt: undefined, disabledReason: deletedReason,
				canEdit: true, runAs: { userId: "u1", displayName: "Me" },
				latestRun: { ...cloudAutomation.latestRun, status: "completed", errorCode: undefined, errorMessage: undefined },
			}];
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			// The project is gone from the list: it reads as not found, not as an id.
			expect(within(row).getByText("Project not found")).toBeInTheDocument();
			expect(within(row).queryByText("cloud-gone")).toBeNull();
			expect(within(row).getByText(deletedReason)).toBeInTheDocument();
			expect(within(row).getByRole("switch", { name: "Enable Nightly debt" })).not.toBeChecked();

			// Run now and turning it on are refused, with what to do.
			const message = "This automation's project was deleted, so it can't run or change. Delete the automation.";
			mocks.runNow.mockRejectedValue(refusal());
			await user.click(within(row).getByRole("button", { name: "Run Nightly debt now" }));
			expect(await screen.findByText(message)).toHaveAttribute("role", "alert");
			mocks.updateCloud.mockRejectedValue(refusal());
			await user.click(within(row).getByRole("switch", { name: "Enable Nightly debt" }));
			await waitFor(() => expect(mocks.updateCloud).toHaveBeenCalledWith({ id: "c1", body: { enabled: true } }));
			expect(await screen.findByText(message)).toHaveAttribute("role", "alert");

			// An edit's save shows it on the form.
			mocks.updateCloudError = refusal();
			cleanup();
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			const dialog = within(screen.getByRole("dialog", { name: "Edit automation" }));
			expect(dialog.getByRole("combobox", { name: "Project" })).toHaveTextContent("Project not found");
			expect(dialog.getByRole("alert")).toHaveTextContent(message);
			await user.click(dialog.getByRole("button", { name: "Cancel" }));

			// Delete still works.
			await user.click(screen.getByRole("button", { name: "Delete Nightly debt" }));
			await user.click(await screen.findByRole("button", { name: "Delete automation" }));
			expect(mocks.removeCloud).toHaveBeenCalledWith("c1", expect.anything());
		});

		it("offers deleting the session after each run, explained, and saves only that change", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.createCloud.mockResolvedValue({});
			renderView();
			await openBlankCreate(user);
			let dialog = within(screen.getByRole("dialog"));
			// This computer's automations have no such setting.
			expect(dialog.queryByRole("switch", { name: "Delete each run's session when it finishes" })).toBeNull();
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			const toggle = dialog.getByRole("switch", { name: "Delete each run's session when it finishes" });
			expect(toggle).not.toBeChecked();
			// What it does is visible help the switch is described by, not a tooltip paragraph (DESIGN.md).
			expect(toggle).toHaveAccessibleDescription(/Off: the session pauses when idle, and the next run continues in it\. On: each run starts a new session/);
			expect(dialog.queryByRole("button", { name: "About deleting the session" })).toBeNull();
			await user.click(toggle);
			await user.click(dialog.getByRole("combobox", { name: "Project" }));
			await user.click(await screen.findByRole("option", { name: "Cloud app" }));
			await user.type(dialog.getByRole("textbox", { name: "Name" }), "Nightly debt");
			await user.type(dialog.getByRole("textbox", { name: "Prompt" }), "List the top 10 debt items.");
			await pickTime(user, screen.getByRole("dialog"), "07", "00");
			await user.click(dialog.getByRole("button", { name: "Create automation" }));
			await waitFor(() => expect(mocks.createCloud).toHaveBeenCalled());
			expect(mocks.createCloud.mock.calls[0][0]).toMatchObject({ deleteSessionOnComplete: true });
			await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

			// An edit that flips only the toggle saves only the toggle.
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true, runAs: { userId: "u1", displayName: "Me" } }];
			mocks.updateCloud.mockResolvedValue({});
			cleanup();
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			dialog = within(screen.getByRole("dialog", { name: "Edit automation" }));
			await user.click(dialog.getByRole("switch", { name: "Delete each run's session when it finishes" }));
			await user.click(dialog.getByRole("button", { name: "Save changes" }));
			await waitFor(() => expect(mocks.updateCloud).toHaveBeenCalledTimes(1));
			expect(mocks.updateCloud).toHaveBeenCalledWith({ id: "c1", body: { deleteSessionOnComplete: true } });

			// One that deletes its sessions opens with the switch on, and turning it off saves only that.
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true, deleteSessionOnComplete: true, runAs: { userId: "u1", displayName: "Me" } }];
			mocks.updateCloud.mockClear();
			cleanup();
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			dialog = within(screen.getByRole("dialog", { name: "Edit automation" }));
			const on = dialog.getByRole("switch", { name: "Delete each run's session when it finishes" });
			expect(on).toBeChecked();
			await user.click(on);
			await user.click(dialog.getByRole("button", { name: "Save changes" }));
			await waitFor(() => expect(mocks.updateCloud).toHaveBeenCalledTimes(1));
			expect(mocks.updateCloud).toHaveBeenCalledWith({ id: "c1", body: { deleteSessionOnComplete: false } });
		});

		it("names the agent to log in to when saving is refused for its missing login", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			// The save's preflight refused the credential of the harness it sent.
			mocks.createCloudError = new CloudCpError("Connect and validate your Claude Code credential first: the automation runs with it.", { status: 422, code: "agent_provider_required" });
			mocks.createCloudVariables = { harness: "claude-code" };
			renderView();
			await openBlankCreate(user);
			expect(within(screen.getByRole("dialog")).getByText("Log in to Claude Code in AO Cloud first: the automation runs with that login.")).toHaveAttribute("role", "alert");
		});

		it("says a full plan means a zero limit when saving, and full right now on Run now", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			// A save is refused for a plan limit only when the limit is zero.
			mocks.createCloudError = new CloudCpError("Your plan has 0 worker slots.", { status: 409, code: "WORKER_SLOTS_FULL" });
			mocks.cloudAutomations = [{ ...cloudAutomation, latestRun: { ...cloudAutomation.latestRun, status: "completed", errorCode: undefined, errorMessage: undefined } }];
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			expect(dialog.getByRole("alert")).toHaveTextContent("Your plan allows none of these cloud sessions (its limit is 0), so this automation could never run. Choose a plan that includes them in Settings > Plans, then save again.");
			await user.click(dialog.getByRole("button", { name: "Cancel" }));

			// Run now is refused for being full right now: free a session or wait.
			mocks.runNow.mockRejectedValue(new CloudCpError("Your plan's worker slots are full.", { status: 409, code: "WORKER_SLOTS_FULL" }));
			await user.click(screen.getByRole("button", { name: "Run Nightly debt now" }));
			expect(await screen.findByText("Your plan has no room for another session right now, so the run couldn't start. Free a session or wait for your limit to reset, then try again.")).toHaveAttribute("role", "alert");
			expect(screen.queryByText(/its limit is 0/)).toBeNull();
		});

		it("offers only the agents logged in to AO Cloud", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.providerConnections = [{ provider: "codex", validationState: "valid" }, { provider: "cursor", validationState: "invalid" }, { provider: "github", validationState: "valid" }];
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			expect(dialog.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
			await user.click(dialog.getByRole("combobox", { name: "Agent" }));
			expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual(["Codex"]);
		});

		it("asks for an agent login when none is connected to AO Cloud", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.providerConnections = [];
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			expect(dialog.getByRole("combobox", { name: "Agent" })).toHaveTextContent("Log in to an agent first");
			expect(dialog.getByText("An automation runs with your agent login in AO Cloud. Log in to an agent first.")).toBeInTheDocument();
		});

		it("does not send a cloud automation without an agent, and opens the cloud agent logins", async () => {
			const user = userEvent.setup();
			const openGlobalSettings = vi.fn();
			useUiStore.setState({ openGlobalSettings });
			mocks.cloudAvailable = true;
			mocks.providerConnections = [];
			renderView();
			await openBlankCreate(user);
			const dialog = within(screen.getByRole("dialog"));
			await user.click(dialog.getByRole("combobox", { name: "Runs on" }));
			await user.click(await screen.findByRole("option", { name: "AO Cloud" }));
			expect(dialog.getByRole("combobox", { name: "Agent" })).toBeDisabled();
			expect(dialog.getByRole("button", { name: "Create automation" })).toBeDisabled();
			await user.click(dialog.getByRole("button", { name: "Go to Agents settings" }));
			expect(openGlobalSettings).toHaveBeenCalledWith("harness", expect.objectContaining({ harnessView: "cloud" }));
			expect(mocks.createCloud).not.toHaveBeenCalled();
		});

		it("lists cloud automations with who they run as, and runs one now", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [cloudAutomation];
			mocks.runNow.mockResolvedValue({});
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			expect(within(row).getByText("AO Cloud")).toBeInTheDocument();
			expect(within(row).getByText("Runs as Priya")).toBeInTheDocument();
			expect(within(row).getByRole("alert")).toHaveTextContent("Skipped: your workspace is at its session limit.");
			// Only its owner, creator or an admin may change it.
			expect(within(row).queryByRole("button", { name: "Edit Nightly debt" })).not.toBeInTheDocument();
			await user.click(within(row).getByRole("button", { name: "Run Nightly debt now" }));
			expect(mocks.runNow).toHaveBeenCalledWith("c1");
			await user.click(within(row).getByRole("button", { name: "Get notified about Nightly debt" }));
			expect(mocks.subscribe).toHaveBeenCalledWith({ id: "c1", subscribed: true });
		});

		it("lines up a read-only row's actions with an editable row's", () => {
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [cloudAutomation, { ...cloudAutomation, id: "c2", displayName: "Docs drift", canEdit: true }];
			renderView();
			const actions = (name: RegExp) => within(screen.getByRole("heading", { name }).closest("article")!).getByRole("switch").closest("div.flex")!;
			// Run now, notify, Edit and Delete (or their places), then the switch.
			expect(actions(/Nightly debt/).children).toHaveLength(5);
			expect(actions(/Docs drift/).children).toHaveLength(5);
		});

		it("keeps the notify toggle's name and reports its state with aria-pressed", () => {
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, subscribed: true }];
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			expect(within(row).getByRole("button", { name: "Get notified about Nightly debt" })).toHaveAttribute("aria-pressed", "true");
		});

		it("shows a schedule in the automation's own timezone when it differs from this computer's", () => {
			mocks.cloudAvailable = true;
			const here = Intl.DateTimeFormat().resolvedOptions().timeZone;
			const elsewhere = here === "Pacific/Chatham" ? "Asia/Kolkata" : "Pacific/Chatham";
			mocks.cloudAutomations = [{ ...cloudAutomation, timezone: elsewhere }, { ...cloudAutomation, id: "c2", displayName: "Local zone", timezone: here }];
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			expect(within(row).getByText(`Daily at 7:00 AM (${elsewhere})`)).toBeInTheDocument();
			const local = screen.getByRole("heading", { name: /Local zone/ }).closest("article")!;
			expect(within(local).getByText("Daily at 7:00 AM")).toBeInTheDocument();
		});

		it("explains, in the person's language, why a run was skipped or did not finish", () => {
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [
				{ ...cloudAutomation, latestRun: { ...cloudAutomation.latestRun, errorCode: "WORKER_SLOTS_FULL", errorMessage: "Your plan is full." } },
				{ ...cloudAutomation, id: "c2", displayName: "Docs drift", latestRun: { ...cloudAutomation.latestRun, automationId: "c2", errorCode: "AUTOMATION_DISABLED", errorMessage: "off" } },
				{ ...cloudAutomation, id: "c3", displayName: "Security scan", latestRun: { ...cloudAutomation.latestRun, automationId: "c3", status: "failed", errorCode: "agent_provider_required", errorMessage: "Connect your Codex credential" } },
			];
			renderView();
			const row = (name: RegExp) => screen.getByRole("heading", { name }).closest("article")!;
			expect(within(row(/Nightly debt/)).getByRole("alert")).toHaveTextContent("Skipped: your plan's limit was reached.");
			expect(within(row(/Docs drift/)).getByRole("alert")).toHaveTextContent("Skipped: the automation was turned off before this run started.");
			expect(within(row(/Security scan/)).getByRole("alert")).toHaveTextContent("Failed: the person it runs as has no valid agent login in AO Cloud.");
		});

		it("shows why it was turned off, and the agent's name, in its details", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, enabled: false, nextRunAt: undefined, consecutiveFailures: 5, disabledReason: "Turned off after 5 failed runs in a row." }];
			renderView();
			await user.click(screen.getByRole("button", { name: "Show run history for Nightly debt" }));
			const details = document.getElementById("automation-history-cloud-c1")!;
			expect(within(details).getByText("Turned off after 5 failed runs in a row.")).toBeInTheDocument();
			expect(within(details).getByText("Codex")).toBeInTheDocument();
		});

		it("shows a settled run's session as ended and viewable, never as running", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.navigate.mockReset();
			const run = (id: string, status: string, sessionId: string) => ({ id, automationId: "c1", scheduledFor: "2026-11-15T07:00:00Z", trigger: "schedule", status, attemptCount: 1, sessionId, createdAt: "2026-11-15T07:00:00Z" });
			mocks.cloudRuns = [run("r3", "running", "s-live"), run("r2", "running", "s-deleted"), run("r1", "completed", "s-done")];
			// The session list says which sessions ended: one deleted mid-run reads terminated before the run settles.
			mocks.workspaces = [{ id: "demo", name: "Demo" }, { id: "cloud-1", name: "Cloud app", kind: "cloud", cloudOrgId: "org-1", sessions: [
				{ id: "s-live", isTerminated: false }, { id: "s-deleted", isTerminated: true }, { id: "s-done", isTerminated: true },
			] }];
			mocks.cloudAutomations = [{ ...cloudAutomation, latestRun: mocks.cloudRuns[0] }];
			renderView();
			await user.click(screen.getByRole("button", { name: "Show run history for Nightly debt" }));
			const history = document.getElementById("automation-history-cloud-c1")!;
			const entry = (sessionId: string) => within(history).getAllByTestId("automation-run").find((element) => element.dataset.session === sessionId)!;
			expect(within(entry("s-live")).getByRole("button", { name: "Open session" })).toBeInTheDocument();
			expect(within(entry("s-live")).queryByText("Session ended")).toBeNull();
			for (const ended of ["s-deleted", "s-done"]) {
				expect(within(entry(ended)).getByText("Session ended")).toBeInTheDocument();
				expect(within(entry(ended)).queryByRole("button", { name: "Open session" })).toBeNull();
			}
			await user.click(within(entry("s-done")).getByRole("button", { name: "View session" }));
			expect(mocks.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "cloud-1", sessionId: "s-done" } });
		});

		it("shows a kept session as open after its run settles, and an unlisted one as ended only when the automation deletes its sessions", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			const run = (id: string, status: string, sessionId: string) => ({ id, automationId: "c1", scheduledFor: "2026-11-15T07:00:00Z", trigger: "schedule", status, attemptCount: 1, sessionId, createdAt: "2026-11-15T07:00:00Z" });
			// deleteSessionOnComplete off (the default): settled runs share one session, which stays, paused, for the next run.
			mocks.cloudRuns = [run("r3", "completed", "s-kept"), run("r2", "completed", "s-kept"), run("r1", "failed", "s-unlisted")];
			mocks.workspaces = [{ id: "demo", name: "Demo" }, { id: "cloud-1", name: "Cloud app", kind: "cloud", cloudOrgId: "org-1", sessions: [{ id: "s-kept", isTerminated: false }] }];
			mocks.cloudAutomations = [{ ...cloudAutomation, latestRun: mocks.cloudRuns[0] }];
			renderView();
			await user.click(screen.getByRole("button", { name: "Show run history for Nightly debt" }));
			let history = document.getElementById("automation-history-cloud-c1")!;
			let entries = within(history).getAllByTestId("automation-run");
			for (const entry of entries) {
				expect(within(entry).queryByText("Session ended")).toBeNull();
				expect(within(entry).getByRole("button", { name: "Open session" })).toBeInTheDocument();
			}

			// deleteSessionOnComplete on: a settled run's session the list does not carry has ended.
			mocks.cloudAutomations = [{ ...cloudAutomation, deleteSessionOnComplete: true, latestRun: mocks.cloudRuns[0] }];
			cleanup();
			renderView();
			await user.click(screen.getByRole("button", { name: "Show run history for Nightly debt" }));
			history = document.getElementById("automation-history-cloud-c1")!;
			entries = within(history).getAllByTestId("automation-run");
			const unlisted = entries.find((element) => element.dataset.session === "s-unlisted")!;
			expect(within(unlisted).getByText("Session ended")).toBeInTheDocument();
			// The list's own word wins: the kept session is still live.
			const kept = entries.filter((element) => element.dataset.session === "s-kept");
			for (const entry of kept) expect(within(entry).getByRole("button", { name: "Open session" })).toBeInTheDocument();
		});

		it("saves only what an edit changed, and nothing when it changed nothing", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true, runAs: { userId: "u1", displayName: "Me" } }];
			mocks.updateCloud.mockResolvedValue({});
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			let dialog = screen.getByRole("dialog", { name: "Edit automation" });
			await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit automation" })).not.toBeInTheDocument());
			expect(mocks.updateCloud).not.toHaveBeenCalled();

			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			dialog = screen.getByRole("dialog", { name: "Edit automation" });
			const name = within(dialog).getByRole("textbox", { name: "Name" });
			await user.clear(name);
			await user.type(name, "Weekly debt");
			await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
			await waitFor(() => expect(mocks.updateCloud).toHaveBeenCalledTimes(1));
			expect(mocks.updateCloud).toHaveBeenCalledWith({ id: "c1", body: { displayName: "Weekly debt" } });
		});

		it("does not rebind an unchanged automation from a control plane that leaves out deleteSessionOnComplete", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			const { deleteSessionOnComplete: _omitted, ...withoutField } = cloudAutomation;
			mocks.cloudAutomations = [{ ...withoutField, canEdit: true, runAs: { userId: "u1", displayName: "Me" } }];
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			const dialog = screen.getByRole("dialog", { name: "Edit automation" });
			expect(within(dialog).getByRole("switch", { name: "Delete each run's session when it finishes" })).not.toBeChecked();
			await user.click(within(dialog).getByRole("button", { name: "Save changes" }));
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Edit automation" })).not.toBeInTheDocument());
			expect(mocks.updateCloud).not.toHaveBeenCalled();
		});

		it("says turning a teammate's automation on makes it run as you, and why it could not", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true, enabled: false, nextRunAt: undefined }];
			mocks.updateCloud.mockRejectedValue(new CloudCpError("Connect and validate your Codex credential first.", { status: 422, code: "agent_provider_required" }));
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			const toggle = within(row).getByRole("switch", { name: "Enable Nightly debt" });
			await user.hover(toggle);
			expect(await screen.findByRole("tooltip")).toHaveTextContent("Turning it on makes it run as you, with your Codex login in AO Cloud.");
			await user.click(toggle);
			expect(mocks.updateCloud).toHaveBeenCalledWith({ id: "c1", body: { enabled: true } });
			expect(await screen.findByText("Turning it on makes it run as you. Log in to Codex in AO Cloud first.")).toBeInTheDocument();
		});

		it("tells an editor who is not its run-as person that saving makes it run as them", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true }];
			renderView();
			await user.click(screen.getByRole("button", { name: "Edit Nightly debt" }));
			const dialog = screen.getByRole("dialog", { name: "Edit automation" });
			expect(within(dialog).getByText("Saving makes it run as you, with your Codex login in AO Cloud.")).toBeInTheDocument();
		});

		it("tells the person it runs as nothing about it running as them", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, canEdit: true, enabled: false, nextRunAt: undefined, runAs: { userId: "u1", displayName: "Me" } }];
			mocks.updateCloud.mockRejectedValue(new CloudCpError("Connect and validate your Codex credential first.", { status: 422, code: "agent_provider_required" }));
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			const toggle = within(row).getByRole("switch", { name: "Enable Nightly debt" });
			await user.hover(toggle);
			await new Promise((resolve) => setTimeout(resolve, 500));
			expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
			await user.click(toggle);
			// The refusal names the agent whose login is missing.
			expect(await screen.findByText("Log in to Codex in AO Cloud first: the automation runs with that login.")).toBeInTheDocument();
			await user.click(within(row).getByRole("button", { name: "Edit Nightly debt" }));
			const dialog = screen.getByRole("dialog", { name: "Edit automation" });
			expect(within(dialog).queryByText(/Saving makes it run as you/)).not.toBeInTheDocument();
		});

		it("says why Run now is off while a run is in flight, on hover and to the keyboard", async () => {
			const user = userEvent.setup();
			mocks.cloudAvailable = true;
			mocks.cloudAutomations = [{ ...cloudAutomation, latestRun: { ...cloudAutomation.latestRun, status: "running", errorCode: undefined, errorMessage: undefined } }];
			// A disabled button takes no pointer events (the Button's disabled:pointer-events-none).
			const style = document.head.appendChild(document.createElement("style"));
			style.textContent = "button:disabled { pointer-events: none; }";
			try {
				renderView();
				const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
				const runNow = within(row).getByRole("button", { name: "Run Nightly debt now" });
				expect(runNow).toBeDisabled();
				const trigger = runNow.closest<HTMLElement>("[data-slot=tooltip-trigger]")!;
				await user.hover(trigger);
				expect(await screen.findByRole("tooltip")).toHaveTextContent("Already running");
				await user.unhover(trigger);
				await waitFor(() => expect(screen.queryByRole("tooltip")).not.toBeInTheDocument());
				// Tab from the row's expander reaches the explanation, not the next button.
				within(row).getByRole("button", { name: "Show run history for Nightly debt" }).focus();
				await user.tab();
				expect(await screen.findByRole("tooltip")).toHaveTextContent("Already running");
			} finally {
				style.remove();
			}
		});

		it("keeps showing a run in flight that a newer skipped occurrence hides, with Run now off", () => {
			mocks.cloudAvailable = true;
			const skipped = { ...cloudAutomation.latestRun, id: "r2", scheduledFor: "2026-11-16T14:19:00Z", errorCode: "PREVIOUS_RUN_ACTIVE", errorMessage: "The previous run was still going, so this one was skipped." };
			const manual = { id: "r1", automationId: "c1", scheduledFor: "2026-11-16T14:11:00Z", trigger: "manual", status: "running", attemptCount: 1, sessionId: "s-live", createdAt: "2026-11-16T14:11:00Z" };
			mocks.cloudAutomations = [{ ...cloudAutomation, latestRun: skipped, hiddenInFlightRun: manual }];
			renderView();
			const row = screen.getByRole("heading", { name: /Nightly debt/ }).closest("article")!;
			expect(within(row).getByText("Last run Running")).toBeInTheDocument();
			expect(within(row).queryByText("Last run Skipped")).not.toBeInTheDocument();
			// The skip still says why that occurrence did not run.
			expect(within(row).getByRole("alert")).toHaveTextContent("Skipped: the previous run was still going.");
			expect(within(row).getByRole("button", { name: "Run Nightly debt now" })).toBeDisabled();
		});
	});
});
