import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { AUTOMATION_TEMPLATES } from "../lib/automation-templates";
import { AutomationsView } from "./AutomationsView";
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
}));

vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: mocks.capture,
}));

vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
const codexSnapshot = { id: "codex", label: "Codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready", usageCount: 3, lastUsedAt: null };
const ampSnapshot = { ...codexSnapshot, id: "amp", label: "Amp" };
const cursorSnapshot = { id: "cursor", label: "Cursor", installation: { state: "not_installed", freshness: "fresh" }, authentication: { state: "unknown", freshness: "stale" }, effectiveReadiness: "not_ready", usageCount: 0, lastUsedAt: null };
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [mocks.fallbackAmp ? { ...codexSnapshot, effectiveReadiness: "not_ready" } : codexSnapshot, cursorSnapshot, ampSnapshot] } }) }));
vi.mock("../hooks/useProjectDefaultWorker", () => ({ useProjectWorkerDefaults: () => ({ loaded: mocks.defaultsLoaded, harness: mocks.defaultWorker, agentConfig: mocks.baseTuning, worker: { agent: mocks.defaultWorker, agentConfig: mocks.workerTuning } }) }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: [{ id: "demo", name: "Demo" }] }) }));
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
	beforeEach(() => { mocks.defaultWorker = "codex"; mocks.defaultsLoaded = true; mocks.fallbackAmp = false; mocks.workerTuning = {}; mocks.baseTuning = {}; mocks.automations = []; mocks.runsError = null; mocks.create.mockReset(); mocks.update.mockReset(); mocks.capture.mockReset(); });

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
		expect(within(dialog).getByLabelText("Time")).toHaveValue("09:30");

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
		const time = within(dialog).getByLabelText("Time");
		await user.clear(time);
		await user.type(time, "0930");
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
		const hour = within(dialog).getByLabelText("Hour");
		const minute = within(dialog).getByLabelText("Minute");
		await user.clear(hour);
		await user.type(hour, "16");
		await user.clear(minute);
		await user.type(minute, "45");
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

	it("uses a 24-hour text time field that only accepts clock digits", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		const time = within(dialog).getByLabelText("Time");
		expect(time).toHaveAttribute("type", "text");
		expect(time).toHaveAttribute("inputMode", "numeric");
		await user.clear(time);
		await user.type(time, "0705");
		expect(time).toHaveValue("07:05");
	});

	it("rejects out-of-range time digits while typing", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const time = within(screen.getByRole("dialog", { name: "Create automation" })).getByLabelText("Time");
		await user.clear(time);
		await user.type(time, "9999");
		expect(time).toHaveValue("09");
		await user.clear(time);
		await user.type(time, "2560");
		expect(time).toHaveValue("20");
		await user.clear(time);
		await user.type(time, "2360");
		expect(time).toHaveValue("23:0");
		await user.clear(time);
		await user.type(time, "2359");
		expect(time).toHaveValue("23:59");
	});

	it("rejects an empty time before create", async () => {
		const user = userEvent.setup();
		renderView();
		await openBlankCreate(user);

		const time = screen.getByLabelText("Time");
		await user.clear(time);
		await user.click(screen.getByRole("button", { name: "Create automation" }));

		expect(time).toBeInvalid();
		expect(time).toHaveAccessibleDescription("Choose a valid time.");
		expect(mocks.create).not.toHaveBeenCalled();
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
		expect(screen.getByLabelText("Time")).toHaveValue("14:07");
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
});
