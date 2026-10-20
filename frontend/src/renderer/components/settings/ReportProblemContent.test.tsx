import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import { useUiStore } from "../../stores/ui-store";
import { GlobalNewTaskDialog } from "../GlobalNewTaskDialog";
import { ReportProblemContent } from "./ReportProblemContent";

const h = vi.hoisted(() => ({ daemonState: "ready", navigate: vi.fn() }));
vi.mock("../../lib/shell-context", () => ({ useShell: () => ({ daemonStatus: { state: h.daemonState } }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));
vi.mock("../NewTaskDialog", () => ({
	NewTaskDialog: ({ open, projectId, initialPrompt, displayName, onContentChange }: { open: boolean; projectId?: string; initialPrompt?: string; displayName?: string; onContentChange?: (dirty: boolean) => void }) => open
		? <div role="dialog" data-project={projectId} data-name={displayName}><textarea aria-label="Prompt" defaultValue={initialPrompt} onChange={(event) => onContentChange?.(!!event.target.value.trim())} /></div> : null,
}));
function renderReport(active = true) {
	render(<QueryClientProvider client={new QueryClient()}><ReportProblemContent active={active} /><GlobalNewTaskDialog /></QueryClientProvider>);
}
beforeEach(() => {
	h.daemonState = "ready";
	useUiStore.setState({ newTaskRequest: null, newTaskDialogHasContent: false, settingsModal: { scope: "global", section: "help" } });
});
afterEach(() => vi.restoreAllMocks());
describe("Report a problem", () => {
	it("prepares diagnostics and opens the existing standalone dialog with the prompt and name", async () => {
		const prepare = vi.spyOn(aoBridge.triage, "prepare").mockResolvedValue({ ok: true, prompt: "Read /home/me/.ao/triage/context.md", dir: "/home/me/.ao/triage" });
		const launch = vi.spyOn(aoBridge.triage, "launch");
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		const dialog = await screen.findByRole("dialog");
		expect(prepare).toHaveBeenCalledWith(); expect(launch).not.toHaveBeenCalled();
		expect(dialog).toHaveAttribute("data-project", "__standalone__");
		expect(dialog).toHaveAttribute("data-name", "Report a problem");
		expect(screen.getByLabelText("Prompt")).toHaveValue("Read /home/me/.ao/triage/context.md");
		expect(useUiStore.getState().settingsModal).toBeNull();
	});
	it.each(["", "keep this draft"])("handles an existing composer containing %s", async (draft) => {
 const prepare = vi.spyOn(aoBridge.triage, "prepare").mockResolvedValue({ ok: true, prompt: "triage prompt", dir: "/triage" });
 renderReport();
 act(() => useUiStore.getState().requestNewTask("project"));
 await screen.findByRole("dialog");
 fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: draft } });
 fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
 if (draft) {
  expect(await screen.findByRole("alert")).toHaveTextContent("Close the open New agent dialog first.");
  expect(screen.getByLabelText("Prompt")).toHaveValue(draft);
  expect(useUiStore.getState().settingsModal).not.toBeNull();
  expect(prepare).not.toHaveBeenCalled();
 } else {
  await waitFor(() => expect(screen.getByLabelText("Prompt")).toHaveValue("triage prompt"));
  expect(useUiStore.getState().settingsModal).toBeNull();
 }
 });
	it("uses Auto terminal fallback when the daemon is down", async () => {
		h.daemonState = "stopped";
		const launch = vi.spyOn(aoBridge.triage, "launch").mockResolvedValue({ ok: false, error: "report.terminalFailed" });
		const prepare = vi.spyOn(aoBridge.triage, "prepare");
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not open a terminal. Try again.");
		expect(launch).toHaveBeenCalledWith(); expect(prepare).not.toHaveBeenCalled();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});
	it("shows preparation failures and keeps settings open", async () => {
		vi.spyOn(aoBridge.triage, "prepare").mockResolvedValue({ ok: false, error: "report.prepareFailed" });
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not prepare the report. Try again.");
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(useUiStore.getState().settingsModal).not.toBeNull();
	});
	it("disables duplicate submission and handles IPC rejection", async () => {
		let reject!: (error: Error) => void;
		vi.spyOn(aoBridge.triage, "prepare").mockReturnValue(new Promise((_, fail) => { reject = fail; }));
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(screen.getByRole("button", { name: "Opening..." })).toBeDisabled();
		reject(new Error("IPC unavailable"));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not start the report. Try again.");
		expect(screen.getByRole("button", { name: "Report a problem" })).toBeEnabled();
	});
	it("shows one action and enabled secondary links", () => {
		renderReport();
		expect(screen.getAllByRole("button")).toHaveLength(1);
		for (const name of ["GitHub", "Discord", "Email"]) expect(screen.getByRole("link", { name })).toHaveAttribute("href");
		expect(screen.getByText("Starts an agent that gathers logs, asks what went wrong and can file the issue for you.")).toBeInTheDocument();
	});
	it("prefills GitHub diagnostics and keeps Discord and email as plain links", async () => {
		vi.spyOn(aoBridge.app, "getVersion").mockResolvedValue("1.2.3");
		vi.spyOn(aoBridge.daemon, "getStatus").mockResolvedValue({ state: "ready", message: "Failed /Users/alice/repo token=private" });
		const open = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		renderReport();
		await waitFor(() => expect(screen.getByRole("link", { name: "GitHub" }).getAttribute("href")).toContain("body="));
		fireEvent.click(screen.getByRole("link", { name: "GitHub" }));
		const url = new URL(open.mock.calls[0][0]);
		expect(url.searchParams.get("body")).toContain("App version: 1.2.3");
		expect(url.searchParams.get("body")).toContain("[redacted-local-path]");
		expect(url.searchParams.get("body")).not.toContain("private");
		for (const [name, href] of [["Discord", "https://discord.gg/WjKNa7EbB8"], ["Email", "mailto:prasad@untrivial.ai?cc=prateek@untrivial.ai"]]) {
			expect(screen.getByRole("link", { name })).toHaveAttribute("href", href);
			fireEvent.click(screen.getByRole("link", { name }));
			expect(open).toHaveBeenLastCalledWith(href);
		}
	});
	it("opens secondary links through the desktop bridge and renders failures", async () => {
		const open = vi.spyOn(aoBridge.app, "openExternal").mockRejectedValue(new Error("blocked"));
		renderReport();
		await waitFor(() => expect(screen.getByRole("link", { name: "GitHub" }).getAttribute("href")).toContain("body="));
		fireEvent.click(screen.getByRole("link", { name: "GitHub" }));
		expect(open).toHaveBeenCalledWith(expect.stringContaining("https://github.com/OrchestratorInc/agent-orchestrator/issues/new?"));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not open the link. Try again.");
	});
});
