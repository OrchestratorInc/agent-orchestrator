import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProjectSettingsEditor, type ProjectSettingsDraft } from "./ProjectSettingsEditor";
import { TooltipProvider } from "./ui/tooltip";

const initialValues: ProjectSettingsDraft = {
	displayName: "Project", defaultBranch: "main", sessionPrefix: "ao",
	workerAgent: "codex", orchestratorAgent: "claude-code", reviewerHarness: "",
	workerModel: "", orchestratorModel: "", reviewerModel: "",
	workerMode: "", orchestratorMode: "", reviewerMode: "",
	workerEffort: "", orchestratorEffort: "", reviewerEffort: "",
	workerPermissions: "auto", orchestratorPermissions: "auto", reviewerPermissions: "",
	autoReview: true, intakeEnabled: false, intakeRepo: "", intakeAssignee: "",
};

function setup(cloud: boolean, save = vi.fn().mockResolvedValue({}), values = initialValues, section: "general" | "agents" = "general", saveUnchanged = false) {
	const onSaveState = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const editor = (selectedSection: "general" | "agents") => <QueryClientProvider client={client}><TooltipProvider>
		<ProjectSettingsEditor initialValues={values} section={selectedSection} saveUnchanged={saveUnchanged}
			capabilities={{ workflow: true, sessionPrefix: !cloud, intake: !cloud, reviewer: true, requiredAgents: !cloud, requiredBranch: cloud, nameLimit: cloud ? 120 : 100, runtimeDefaults: cloud, attribution: true }}
			details={[]} modelScope={() => ""} renderAgent={() => null} save={save} onSaveState={onSaveState} />
	</TooltipProvider></QueryClientProvider>;
	const view = render(editor(section));
	return { save, onSaveState, unmount: view.unmount, setSection: (next: "general" | "agents") => view.rerender(editor(next)) };
}

async function edit(label: string, value: string) {
	await userEvent.click(screen.getByRole("button", { name: `Edit ${label}` }));
	fireEvent.change(screen.getByRole("textbox", { name: label }), { target: { value } });
	await userEvent.keyboard("{Enter}");
}

describe.each([false, true])("shared settings page, Cloud=%s", (cloud) => {
	it("keeps changed controls quiet during a successful autosave", async () => {
		let finish!: (result: object) => void;
		const save = vi.fn().mockReturnValue(new Promise<object>((resolve) => { finish = resolve; }));
		const { onSaveState } = setup(cloud, save);
		await edit("Project name", "New name");
		await userEvent.click(screen.getByRole("switch", { name: "Auto review PRs" }));
		const nameRow = screen.getByTestId("setting-displayName");
		const toggleRow = screen.getByTestId("setting-autoReview");
		expect(within(nameRow).queryByText(/Unsaved changes/)).not.toBeInTheDocument();
		expect(within(toggleRow).queryByText(/Unsaved changes/)).not.toBeInTheDocument();
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(within(nameRow).queryByText(/Unsaved changes/)).not.toBeInTheDocument();
		expect(within(toggleRow).queryByText(/Unsaved changes/)).not.toBeInTheDocument();
		await act(async () => finish({}));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false })));
		expect(screen.queryByText(/Unsaved changes/)).not.toBeInTheDocument();
	});

	it("does not mark a valid name invalid when a server failure uses validation wording", async () => {
		const save = vi.fn().mockRejectedValue(new Error("Project name is required."));
		const { onSaveState } = setup(cloud, save);
		await edit("Project name", "New name");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Project name is required." })));
		expect(within(screen.getByTestId("setting-displayName")).getByText(/Unsaved changes.*Project name is required/)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Edit Project name" }));
		expect(screen.getByRole("textbox", { name: "Project name" })).not.toHaveAttribute("aria-invalid");
	});

	it("does not mark a valid branch invalid when a server failure uses validation wording", async () => {
		if (!cloud) return;
		const message = "Enter a project name of 1-120 characters and a default branch of 1-255 characters.";
		const save = vi.fn().mockRejectedValue(new Error(message));
		const { onSaveState } = setup(cloud, save);
		await edit("Default branch", "develop");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: message })));
		await userEvent.click(screen.getByRole("button", { name: "Edit Default branch" }));
		expect(screen.getByRole("textbox", { name: "Default branch" })).not.toHaveAttribute("aria-invalid");
	});

	it("marks a branch invalid when local validation rejects it", async () => {
		if (!cloud) return;
		const { save, onSaveState } = setup(cloud);
		await edit("Default branch", "");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Enter a project name of 1-120 characters and a default branch of 1-255 characters." })));
		expect(save).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Edit Default branch" }));
		const branch = screen.getByRole("textbox", { name: "Default branch" });
		expect(branch).toHaveAttribute("aria-invalid", "true");
		expect(branch).toHaveAttribute("aria-describedby", "setting-defaultBranch-feedback");
	});

	it("marks changed controls unsaved after a refused write, then clears feedback on retry and reopen", async () => {
		const save = vi.fn().mockRejectedValueOnce(new Error("Save rejected")).mockResolvedValue({});
		const { onSaveState, unmount } = setup(cloud, save);
		await edit("Project name", "Draft name");
		await userEvent.click(screen.getByRole("switch", { name: "Auto review PRs" }));
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Save rejected" })));
		expect(within(screen.getByTestId("setting-displayName")).getByText(/Unsaved changes.*Save rejected/)).toBeInTheDocument();
		expect(within(screen.getByTestId("setting-autoReview")).getByText(/Unsaved changes.*Save rejected/)).toHaveAttribute("aria-live", "polite");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
		expect(screen.queryByText(/Unsaved changes.*Save rejected/)).not.toBeInTheDocument();
		unmount();
		setup(cloud, save, { ...initialValues, displayName: "Draft name", autoReview: false });
		expect(screen.getByText("Draft name")).toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "Auto review PRs" })).not.toBeChecked();
		expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument();
	});

	it("marks a refused attribution toggle unsaved", async () => {
		const save = vi.fn().mockRejectedValue(new Error("Save rejected"));
		const { onSaveState } = setup(cloud, save);
		await userEvent.click(screen.getByRole("switch", { name: "Credit Orchestrator on commits" }));
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Save rejected" })));
		expect(save).toHaveBeenCalledWith(expect.objectContaining({ attributionCommitTrailer: false }));
		expect(within(screen.getByTestId("setting-attribution")).getByText(/Unsaved changes.*Save rejected/)).toHaveAttribute("aria-live", "polite");
		expect(within(screen.getByTestId("setting-autoReview")).queryByText(/Unsaved changes/)).not.toBeInTheDocument();
	});

	it("reopens with saved values after a refused change", async () => {
		const save = vi.fn().mockRejectedValue(new Error("Save rejected"));
		const { onSaveState, unmount } = setup(cloud, save);
		await userEvent.click(screen.getByRole("switch", { name: "Auto review PRs" }));
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed" })));
		expect(within(screen.getByTestId("setting-autoReview")).getByText(/Unsaved changes.*Save rejected/)).toBeInTheDocument();
		unmount();
		setup(cloud);
		expect(screen.getByRole("switch", { name: "Auto review PRs" })).toBeChecked();
		expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument();
	});

	it("lets an unrelated setting save when an existing local project has no orchestrator agent", async () => {
		if (cloud) return;
		const save = vi.fn().mockResolvedValue({});
		const { onSaveState } = setup(cloud, save, { ...initialValues, orchestratorAgent: "" });
		await userEvent.click(screen.getByRole("switch", { name: "Auto review PRs" }));
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false })));
	});

	it("still requires missing agents on an explicit Agents-page save", async () => {
		if (cloud) return;
		const save = vi.fn().mockResolvedValue({});
		const { onSaveState } = setup(cloud, save, { ...initialValues, orchestratorAgent: "" }, "agents", true);
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Worker and orchestrator agents are required." })));
		expect(save).not.toHaveBeenCalled();
	});

	it("retries a validation-refused draft when changing sections makes it valid", async () => {
		if (cloud) return;
		const save = vi.fn().mockResolvedValue({});
		const { onSaveState, setSection } = setup(cloud, save, { ...initialValues, orchestratorAgent: "" }, "agents", true);
		await userEvent.click(screen.getByRole("button", { name: "Worker approval" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Accept edits" }));
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Worker and orchestrator agents are required." })));
		expect(save).not.toHaveBeenCalled();
		setSection("general");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", error: undefined })));
	});

	it("retries a refused draft after another edit returns to the same value", async () => {
		const save = vi.fn().mockRejectedValueOnce(new Error("Write failed")).mockResolvedValue({});
		const { onSaveState } = setup(cloud, save);
		await edit("Project name", "Draft B");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Write failed" })));
		await edit("Project name", "Draft C");
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ error: undefined })));
		await edit("Project name", "Draft B");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
	});

	it("does not automatically retry a server-refused draft on another section", async () => {
		const save = vi.fn().mockRejectedValue(new Error("Write failed"));
		const { onSaveState, setSection } = setup(cloud, save);
		await edit("Project name", "Draft name");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Write failed" })));
		setSection("agents");
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 750)); });
		expect(save).toHaveBeenCalledTimes(1);
		expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Write failed" }));
	});

	it("uses the same inline fields and autosave, with supported workflow fields", async () => {
		const { save } = setup(cloud);
		expect(screen.getByRole("button", { name: "Edit Project name" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Edit Default branch" })).toBeInTheDocument();
		expect(Boolean(screen.queryByRole("button", { name: "Edit Session prefix" }))).toBe(!cloud);
		await edit("Project name", "Renamed project");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ displayName: "Renamed project", defaultBranch: "main" }));
		await edit("Default branch", "develop");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ displayName: "Renamed project", defaultBranch: "develop" }));
	});

	it("validates a blank name and recovers after editing without a write", async () => {
		const { save, onSaveState } = setup(cloud);
		await edit("Project name", "   ");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", error: "Project name is required." })));
		expect(within(screen.getByTestId("setting-displayName")).getByText(/Unsaved changes.*Project name is required/)).toBeInTheDocument();
		expect(save).not.toHaveBeenCalled();
		const editName = screen.getByRole("button", { name: "Edit Project name" });
		expect(editName).toHaveAttribute("aria-describedby", "setting-displayName-feedback");
		await userEvent.click(editName);
		const name = screen.getByRole("textbox", { name: "Project name" });
		expect(name).toHaveAttribute("aria-invalid", "true");
		expect(name).toHaveAttribute("aria-describedby", "setting-displayName-feedback");
		fireEvent.change(name, { target: { value: "Valid name" } });
		await userEvent.keyboard("{Enter}");
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
	});

	it("waits for writes, retains failed drafts and retries without duplicate autosaves", async () => {
		let reject!: (error: Error) => void;
		const save = vi.fn().mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
		const { onSaveState } = setup(cloud, save);
		await edit("Project name", "Pending name");
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
		expect(screen.getByRole("button", { name: "Edit Project name" })).toBeDisabled();
		fireEvent.submit(document.getElementById("project-settings-form")!);
		expect(save).toHaveBeenCalledTimes(1);
		await act(async () => reject(new Error("Write failed")));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "failed", dirty: true, error: "Write failed" })));
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 750)); });
		expect(save).toHaveBeenCalledTimes(1);
		expect(screen.getByText("Pending name")).toBeInTheDocument();
		save.mockResolvedValue({});
		fireEvent.submit(document.getElementById("project-settings-form")!);
		await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "saved", dirty: false, error: undefined })));
	});
});

describe("issue intake on another computer", () => {
	const renderWith = (intakeEnabled: boolean) => render(<QueryClientProvider client={new QueryClient()}><TooltipProvider>
		<ProjectSettingsEditor initialValues={{ ...initialValues, intakeEnabled }} section="general"
			capabilities={{ workflow: true, sessionPrefix: true, intake: true, reviewer: true, requiredAgents: true, requiredBranch: false, nameLimit: 100, runtimeDefaults: false }}
			details={[]} modelScope={() => ""} renderAgent={() => null} save={vi.fn().mockResolvedValue({})}
			intakeWarning="Issue intake is also on for this repository on: MacBook B." />
	</TooltipProvider></QueryClientProvider>);

	it("is pointed out while intake is on here", () => {
		renderWith(true);
		expect(screen.getByRole("status")).toHaveTextContent("Issue intake is also on for this repository on: MacBook B.");
	});

	it("is not mentioned while intake is off here", () => {
		renderWith(false);
		expect(screen.queryByText(/also on for this repository/)).not.toBeInTheDocument();
	});
});
