import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { AUTOMATION_TEMPLATES } from "../lib/automation-templates";
import { AutomationsView } from "./AutomationsView";

const mocks = vi.hoisted(() => ({
	automations: [] as Array<Record<string, unknown>>,
	create: vi.fn(),
	update: vi.fn(),
	runsError: null as Error | null,
	hideShellTopbar: false,
	capture: vi.fn(),
}));

vi.mock("../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/telemetry")>(),
	captureRendererEvent: mocks.capture,
}));

vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
	hidesShellTopbar: () => mocks.hideShellTopbar,
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
const codexSnapshot = { id: "codex", label: "Codex", installation: { state: "installed", freshness: "fresh" }, authentication: { state: "authorized", freshness: "fresh" }, effectiveReadiness: "ready", usageCount: 3, lastUsedAt: null };
const cursorSnapshot = { id: "cursor", label: "Cursor", installation: { state: "not_installed", freshness: "fresh" }, authentication: { state: "unknown", freshness: "stale" }, effectiveReadiness: "not_ready", usageCount: 0, lastUsedAt: null };
vi.mock("../hooks/useAgentReadinessQuery", () => ({ useAgentReadinessQuery: () => ({ data: { agents: [codexSnapshot, cursorSnapshot] } }) }));
vi.mock("../hooks/useProjectDefaultWorker", () => ({ useProjectDefaultWorker: () => "codex" }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: [{ id: "demo", name: "Demo" }] }) }));
vi.mock("../hooks/useAgentModelsQuery", () => ({
	agentModelsQueryKey: (agentId: string, projectId: string) => ["agent-models", agentId, projectId],
	agentModelsQueryOptions: (agentId: string, projectId: string) => ({
		queryKey: ["agent-models", agentId, projectId],
		queryFn: async () => ({
			models: [{ id: "gpt-5", label: "GPT-5", isDefault: true }],
			allowCustom: false,
			customModelEntry: "none",
			selectionMode: "catalog",
		}),
		enabled: agentId !== "",
	}),
	refreshAgentModels: vi.fn(),
	revalidateAgentModels: vi.fn(),
}));
vi.mock("../hooks/useAutomations", () => ({
	useAutomations: () => ({ data: mocks.automations, isLoading: false, error: null }),
	useCreateAutomation: () => ({ mutateAsync: mocks.create, isPending: false, error: null }),
	useUpdateAutomation: () => ({ mutateAsync: mocks.update, isPending: false, error: null }),
	useDeleteAutomation: () => ({ mutate: vi.fn(), isPending: false, error: null }),
	useAutomationRuns: () => ({ data: [], isLoading: false, error: mocks.runsError }),
}));

function renderView(ui: ReactNode = <AutomationsView />) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe("AutomationsView", () => {
	beforeEach(() => { mocks.automations = []; mocks.runsError = null; mocks.hideShellTopbar = false; mocks.create.mockReset(); mocks.update.mockReset(); mocks.capture.mockReset(); });

	it("shows a discoverable empty state and create action", () => {
		renderView();
		expect(screen.getByRole("heading", { name: "Automations" })).toBeInTheDocument();
		expect(screen.getByText("No automations yet")).toBeInTheDocument();
		expect(screen.getAllByRole("button", { name: /new automation/i })).not.toHaveLength(0);
	});

	it.each([true, false])("keeps AO's shell title behavior with discoverable templates (%s)", (hidden) => {
		mocks.hideShellTopbar = hidden;
		renderView();
		const title = screen.getByRole("heading", { name: "Automations" });
		if (hidden) expect(title).not.toHaveClass("sr-only");
		else expect(title).toHaveClass("sr-only");
		expect(screen.getByRole("heading", { name: "Start with a template" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "PR review inbox" })).toBeInTheDocument();
	});

	it("preselects the project's resolved default worker like New Task", async () => {
		renderView();
		await userEvent.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toHaveTextContent("Codex");
		expect(within(dialog).queryByRole("button", { name: "Model" })).not.toBeInTheDocument();
		expect(screen.queryByRole("option", { name: "Project default" })).not.toBeInTheDocument();
		expect(screen.queryByRole("combobox", { name: "Session kind" })).not.toBeInTheDocument();
	});

	it("edits an automation from its card action", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "automation-1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC", enabled: true, nextRunAt: "2026-08-27T09:00:00Z", createdAt: "2026-08-26T09:00:00Z", updatedAt: "2026-08-26T09:00:00Z" }];
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
	});

	it("creates a weekly automation on the chosen weekday", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.automation_create_opened");

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		await user.click(within(dialog).getByRole("combobox", { name: "Project" }));
		await user.click(screen.getByRole("option", { name: "Demo" }));
		await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Friday review");
		await user.type(within(dialog).getByRole("textbox", { name: "Prompt" }), "Review the board");
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));

		const funnel = { project_id: "demo", schedule_preset: "daily", prompt_length_bucket: "xs" };
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.automation_create_requested", funnel);
		expect(mocks.capture).toHaveBeenCalledWith("ao.renderer.automation_create_succeeded", funnel);
		expect(mocks.capture).not.toHaveBeenCalledWith("ao.renderer.automation_create_failed", expect.anything());
	});

	it("reports a failed create and keeps the dialog open with its error", async () => {
		const user = userEvent.setup();
		mocks.create.mockRejectedValueOnce(new Error("daemon unreachable"));
		renderView();
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

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
		await userEvent.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(dialog.querySelector('select:not([aria-hidden="true"])')).toBeNull();
		expect(within(dialog).getByRole("combobox", { name: "Project" })).toBeInTheDocument();
		expect(within(dialog).getByRole("combobox", { name: "Schedule" })).toBeInTheDocument();
		expect(within(dialog).getByRole("combobox", { name: "Agent" })).toBeInTheDocument();
		expect(within(dialog).queryByRole("button", { name: "Model" })).not.toBeInTheDocument();
		expect(view.container.querySelector('[data-slot="select-content"]')).toBeNull();
	});

	it("uses a 24-hour text time field that only accepts clock digits", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);

		const time = screen.getByLabelText("Time");
		await user.clear(time);
		await user.click(screen.getByRole("button", { name: "Create automation" }));

		expect(time).toBeInvalid();
		expect(time).toHaveAccessibleDescription("Choose a valid time.");
		expect(mocks.create).not.toHaveBeenCalled();
	});

	it("uses AO's shared centered dialog without a purpose selector", async () => {
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "New automation" }));
		const dialog = screen.getByRole("dialog", { name: "Create automation" });
		expect(dialog).toHaveClass("left-1/2", "top-1/2", "bg-popover", "rounded-lg");
		expect(dialog).not.toHaveClass("right-0", "h-dvh");
		expect(within(dialog).queryByText(/purpose|more options/i)).not.toBeInTheDocument();
	});

	it("shows AO-styled inline feedback when required fields are missing", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
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
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
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
		await userEvent.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
		expect(screen.getByLabelText("Time")).toHaveValue("14:07");
		vi.useRealTimers();
	});

	it("filters paused automations and keeps schedule and project names readable", async () => {
		const base = { projectId: "demo", prompt: "Review the board", kind: "worker", harness: "codex", rrule: "FREQ=WEEKLY;BYDAY=FR;BYHOUR=9;BYMINUTE=30;BYSECOND=0", timezone: "UTC" };
		mocks.automations = [{ ...base, id: "active", displayName: "Friday review", enabled: true }, { ...base, id: "paused", displayName: "Paused review", enabled: false }];
		renderView();
		expect(screen.getAllByText("Demo")).toHaveLength(2);
		expect(screen.getAllByText(/Friday ·/)).toHaveLength(2);
		await userEvent.click(screen.getByRole("button", { name: /Paused 1/ }));
		expect(screen.queryByRole("heading", { name: "Friday review" })).not.toBeInTheDocument();
		expect(screen.getByRole("heading", { name: "Paused review" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Paused 1/ })).toHaveAttribute("aria-pressed", "true");
	});

	it.each([
		["FREQ=DAILY;BYHOUR=8;BYMINUTE=30;BYSECOND=0", /daily · 8:30 AM/i],
		["FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0;BYSECOND=0", /Mon–Fri · 9:00 AM/],
		["FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=10;BYMINUTE=0;BYSECOND=0", /Mon, Wed, Fri · 10:00 AM/],
		["FREQ=MONTHLY;BYMONTHDAY=1;BYHOUR=10;BYMINUTE=0;BYSECOND=0", /Monthly · day 1 · 10:00 AM/],
	])("shows the complete schedule from a canonical daemon rule %s", (rule, label) => {
		mocks.automations = [{ id: "canonical", projectId: "demo", displayName: "Scheduled review", prompt: "Review", kind: "worker", rrule: `DTSTART;TZID=Asia/Kolkata:20261001T183600\nRRULE:${rule}`, timezone: "Asia/Kolkata", enabled: true }];
		renderView();
		expect(screen.getByText(label)).toBeInTheDocument();
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
		fireEvent.change(within(dialog).getByRole("textbox", { name: "Prompt" }), { target: { value: "My revised instructions" } });
		await user.click(within(dialog).getByRole("button", { name: "Create automation" }));
		expect(mocks.create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
			templateId: AUTOMATION_TEMPLATES[index].id, useCase: AUTOMATION_TEMPLATES[index].useCase,
			prompt: "My revised instructions", rrule: AUTOMATION_TEMPLATES[index].rrule,
		}));
	});

	it("creates a custom automation without asking for a purpose", async () => {
		const user = userEvent.setup();
		renderView();
		await user.click(screen.getAllByRole("button", { name: /new automation/i })[0]);
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
		await user.click(screen.getByRole("button", { name: "New automation" }));
		dialog = screen.getByRole("dialog");
		expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("");
		expect(within(dialog).getByRole("textbox", { name: "Prompt" })).toHaveValue("");
		expect(mocks.create).not.toHaveBeenCalled();
	});

	it("filters saved automations by name and project while keeping starters discoverable", async () => {
		const user = userEvent.setup();
		mocks.automations = [{ id: "a1", projectId: "demo", displayName: "Morning triage", prompt: "Review", kind: "worker", harness: "codex", rrule: "FREQ=DAILY;BYHOUR=9;BYMINUTE=0", timezone: "UTC", enabled: true, createdAt: "2026-10-15T08:00:00Z", updatedAt: "2026-10-15T08:00:00Z" }];
		renderView();
		const search = screen.getByRole("searchbox", { name: "Search automations…" });
		await user.type(search, "missing");
		expect(screen.queryByText("Morning triage")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "PR review inbox" })).toBeInTheDocument();
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
