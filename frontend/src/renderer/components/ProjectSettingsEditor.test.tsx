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
	const view = render(<QueryClientProvider client={client}><TooltipProvider>
		<ProjectSettingsEditor initialValues={values} section={section} saveUnchanged={saveUnchanged}
			capabilities={{ workflow: true, sessionPrefix: !cloud, intake: !cloud, reviewer: true, requiredAgents: !cloud, requiredBranch: cloud, nameLimit: cloud ? 120 : 100, runtimeDefaults: cloud }}
			details={[]} modelScope={() => ""} renderAgent={() => null} save={save} onSaveState={onSaveState} />
	</TooltipProvider></QueryClientProvider>);
	return { save, onSaveState, unmount: view.unmount };
}

async function edit(label: string, value: string) {
	await userEvent.click(screen.getByRole("button", { name: `Edit ${label}` }));
	fireEvent.change(screen.getByRole("textbox", { name: label }), { target: { value } });
	await userEvent.keyboard("{Enter}");
}

describe.each([false, true])("shared settings page, Cloud=%s", (cloud) => {
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
		await edit("Project name", "Valid name");
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
