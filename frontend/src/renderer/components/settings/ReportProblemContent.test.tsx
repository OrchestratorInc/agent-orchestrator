import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import { newChatDraftScope, readChatSessionDraft } from "../../lib/chat-drafts";
import { REPORT_PROBLEM_PROMPT } from "../../lib/report-problem";
import { captureRendererEvent } from "../../lib/telemetry";
import { useUiStore } from "../../stores/ui-store";
import { ReportProblemContent } from "./ReportProblemContent";

const h = vi.hoisted(() => ({ daemonState: "ready", navigate: vi.fn() }));
vi.mock("../../lib/telemetry", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/telemetry")>(),
	captureRendererEvent: vi.fn(),
}));
vi.mock("../../lib/shell-context", () => ({ useShell: () => ({ daemonStatus: { state: h.daemonState } }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => h.navigate }));
function renderReport(active = true) {
	render(<QueryClientProvider client={new QueryClient()}><ReportProblemContent active={active} /></QueryClientProvider>);
}
beforeEach(() => {
	h.daemonState = "ready";
	window.localStorage.clear();
	vi.mocked(captureRendererEvent).mockClear();
	useUiStore.setState({ newTaskRequest: null, settingsModal: { scope: "global", section: "help" } });
});
afterEach(() => vi.restoreAllMocks());
describe("Report a problem", () => {
	it("reports each activation once and does not report while inactive", () => {
		const { rerender } = render(<ReportProblemContent active={false} />);
		expect(captureRendererEvent).not.toHaveBeenCalled();
		rerender(<ReportProblemContent active />);
		expect(captureRendererEvent).toHaveBeenCalledExactlyOnceWith("ao.support.opened");
		rerender(<ReportProblemContent active />);
		expect(captureRendererEvent).toHaveBeenCalledTimes(1);
		rerender(<ReportProblemContent active={false} />);
		rerender(<ReportProblemContent active />);
		expect(captureRendererEvent).toHaveBeenCalledTimes(2);
		expect(captureRendererEvent).toHaveBeenLastCalledWith("ao.support.opened");
	});
	it("opens a standalone agent with the prompt filled in", async () => {
		const launch = vi.spyOn(aoBridge.triage, "launch");
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		await waitFor(() => expect(useUiStore.getState().newTaskRequest).not.toBeNull());
		expect(launch).not.toHaveBeenCalled();
		expect(useUiStore.getState().newTaskRequest).toMatchObject({ projectId: "__standalone__" });
		expect(readChatSessionDraft(newChatDraftScope()).composer.text).toBe(REPORT_PROBLEM_PROMPT);
		expect(useUiStore.getState().settingsModal).toBeNull();
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "agent", outcome: "succeeded" });
	});
	it("uses Auto terminal fallback when the daemon is down", async () => {
		h.daemonState = "stopped";
		const launch = vi.spyOn(aoBridge.triage, "launch").mockResolvedValue({ ok: false, error: "report.terminalFailed" });
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not open a terminal. Try again.");
		expect(launch).toHaveBeenCalledWith();
		expect(useUiStore.getState().newTaskRequest).toBeNull();
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "terminal", outcome: "failed" });
	});
	it("reports a successful terminal fallback when the daemon is down", async () => {
		h.daemonState = "stopped";
		vi.spyOn(aoBridge.triage, "launch").mockResolvedValue({ ok: true });
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		await waitFor(() => expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "terminal", outcome: "succeeded" }));
	});
	it("keeps settings open when the draft cannot be saved", async () => {
		vi.spyOn(window.localStorage, "setItem").mockImplementation(() => { throw new Error("quota"); });
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not start the report. Try again.");
		expect(useUiStore.getState().newTaskRequest).toBeNull();
		expect(useUiStore.getState().settingsModal).not.toBeNull();
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "agent", outcome: "failed" });
	});
	it("disables duplicate submission and handles IPC rejection when the daemon is down", async () => {
		h.daemonState = "stopped";
		let reject!: (error: Error) => void;
		vi.spyOn(aoBridge.triage, "launch").mockReturnValue(new Promise((_, fail) => { reject = fail; }));
		renderReport(); fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(screen.getByRole("button", { name: "Opening..." })).toBeDisabled();
		reject(new Error("IPC unavailable"));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not start the report. Try again.");
		expect(screen.getByRole("button", { name: "Report a problem" })).toBeEnabled();
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "terminal", outcome: "failed" });
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
		await waitFor(() => expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: "github", outcome: "succeeded" }));
		for (const [name, href] of [["Discord", "https://discord.gg/WjKNa7EbB8"], ["Email", "mailto:prasad@untrivial.ai?cc=prateek@untrivial.ai"]]) {
			expect(screen.getByRole("link", { name })).toHaveAttribute("href", href);
			fireEvent.click(screen.getByRole("link", { name }));
			expect(open).toHaveBeenLastCalledWith(href);
			await waitFor(() => expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination: name.toLowerCase(), outcome: "succeeded" }));
		}
	});
	it.each(["github", "discord", "email"])("opens the %s link through the desktop bridge and reports failures", async (destination) => {
		const open = vi.spyOn(aoBridge.app, "openExternal").mockRejectedValue(new Error("blocked"));
		renderReport();
		await waitFor(() => expect(screen.getByRole("link", { name: "GitHub" }).getAttribute("href")).toContain("body="));
		const link = screen.getByRole("link", { name: new RegExp(destination, "i") });
		fireEvent.click(link);
		expect(open).toHaveBeenCalledWith(link.getAttribute("href"));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not open the link. Try again.");
		expect(captureRendererEvent).toHaveBeenCalledWith("ao.renderer.support_submitted", { destination, outcome: "failed" });
	});
});
