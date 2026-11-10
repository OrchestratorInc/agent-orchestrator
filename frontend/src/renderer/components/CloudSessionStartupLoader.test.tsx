import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { CloudCpSessionReadyCheck } from "../lib/cloud-cp";
import { forgetControlPlaneClock, groupScriptRuns } from "../lib/cloud-script-runs";
import { CloudSessionStartupLoader } from "./CloudSessionStartupLoader";
import { TooltipProvider } from "./ui/tooltip";

vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => <button type="button" aria-label="Notifications" /> }));

const workspaceUrl = "https://coder.acme.example.com/@dev/dev-3f2a9c1e7b4d";

function mount(props: Parameters<typeof CloudSessionStartupLoader>[0]) {
	return render(<TooltipProvider><CloudSessionStartupLoader {...props} /></TooltipProvider>);
}

describe("CloudSessionStartupLoader", () => {
	afterEach(() => vi.restoreAllMocks());

	it("shows the running Setup log under the steps until the loader completes", () => {
		const setupRun = groupScriptRuns([{
			sessionId: "s", sequence: 1, type: "workspace.script.step", createdAt: "2026-10-18T10:00:00Z",
			payload: { phase: "setup", runId: "r", step: 1, total: 2, command: "npm ci", repository: "", status: "running", output: "fetching" },
		}])[0];
		const { rerender } = mount({ activeIndex: 2, setupRun });
		const panel = screen.getByTestId("script-run-panel");
		expect(panel).toHaveTextContent("Running step 1 of 2");
		expect(panel).toHaveTextContent("fetching");
		expect(screen.getByRole("status", { name: "Session setup activity" })).toBeInTheDocument();
		rerender(<TooltipProvider><CloudSessionStartupLoader activeIndex={3} completed setupRun={setupRun} /></TooltipProvider>);
		expect(screen.queryByTestId("script-run-panel")).not.toBeInTheDocument();
	});

	it("links the bring-your-own Coder workspace beside the workspace step", async () => {
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		mount({ activeIndex: 0, coderWorkspaceUrl: workspaceUrl });
		const workspaceStep = screen.getByText("Creating the workspace").closest("li");
		expect(workspaceStep).not.toBeNull();
		const link = within(workspaceStep!).getByRole("button", { name: "Open the workspace in Coder" });
		await userEvent.click(link);
		expect(openExternal).toHaveBeenCalledWith(workspaceUrl);
		expect(screen.getAllByRole("button", { name: "Open the workspace in Coder" })).toHaveLength(1);
	});

	it("keeps the link after the workspace step completes", () => {
		mount({ activeIndex: 2, coderWorkspaceUrl: workspaceUrl });
		const workspaceStep = screen.getByText("Creating the workspace").closest("li");
		expect(within(workspaceStep!).getByRole("button", { name: "Open the workspace in Coder" })).toBeInTheDocument();
	});

	it("shows no link without a workspace URL or for a non-web URL", () => {
		const view = mount({ activeIndex: 1 });
		expect(screen.queryByTestId("coder-workspace-link")).toBeNull();
		view.unmount();
		mount({ activeIndex: 1, coderWorkspaceUrl: "javascript:alert(1)" });
		expect(screen.queryByTestId("coder-workspace-link")).toBeNull();
	});

	it("shows the person's queued first message above the steps, labelled as waiting for the workspace", () => {
		const view = mount({ activeIndex: 0, queuedMessage: "Fix the flaky login test\nand add a regression test" });
		const message = screen.getByRole("figure", { name: "Your message, sent when the workspace is ready" });
		expect(message).toHaveTextContent("Fix the flaky login test and add a regression test");
		// It reads as the chat's queued message: the agent has not seen it yet.
		expect(within(message).getByText(/Fix the flaky login test/)).toHaveAttribute("data-queued");
		// The steps still show, after it.
		const steps = screen.getByRole("status", { name: "Session setup activity" });
		expect(message.compareDocumentPosition(steps) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		view.rerender(<TooltipProvider><CloudSessionStartupLoader activeIndex={3} completed queuedMessage="Fix it" /></TooltipProvider>);
		expect(screen.getByRole("figure", { name: "Your message, sent when the workspace is ready" })).toBeInTheDocument();
	});

	it("shows no message without one", () => {
		mount({ activeIndex: 1 });
		expect(screen.queryByRole("figure")).toBeNull();
		expect(screen.queryByText("Your message, sent when the workspace is ready")).toBeNull();
	});

	describe("while AO waits for the workspace's own ready check", () => {
		// Coder reported the workspace ready 20:12 ago; the check runs until 1:00:00.
		const now = Date.parse("2026-11-09T19:38:42Z");
		const check = (overrides: Partial<CloudCpSessionReadyCheck> = {}): CloudCpSessionReadyCheck => ({
			since: "2026-11-09T19:18:30Z", timeoutSeconds: 3600, attempts: 31, exitCode: 1,
			checkedAt: "2026-11-09T19:38:40Z", ...overrides,
		});

		beforeEach(() => {
			forgetControlPlaneClock();
			vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
			vi.setSystemTime(now);
		});
		afterEach(() => vi.useRealTimers());

		it("says so in place of creating the workspace, with the time since Coder reported it ready and the checks so far, when the check prints nothing", () => {
			// A bare `grep -q '^Ready' ~/.bootstrap-status` prints nothing.
			mount({ activeIndex: 0, readyCheck: check(), startupNote: "Waiting for your workspace's own setup to finish: its ready check hasn't passed yet.", coderWorkspaceUrl: workspaceUrl });
			const step = screen.getByTestId("multi-step-loader-step");
			expect(step).toHaveTextContent("Waiting for your workspace's own setup");
			expect(screen.queryByText("Creating the workspace")).toBeNull();
			// The workspace link stays beside the step.
			expect(within(step.closest("li")!).getByRole("button", { name: "Open the workspace in Coder" })).toBeInTheDocument();
			const wait = screen.getByRole("region", { name: "Waiting for your workspace's own setup" });
			expect(within(wait).getByTestId("cloud-session-ready-check-progress"))
				.toHaveTextContent("20:12 of 1:00:00 since Coder reported the workspace ready, 31 checks so far");
			expect(screen.queryByTestId("cloud-session-ready-check-output")).toBeNull();
			expect(screen.queryByText(/Latest output/)).toBeNull();
			// The server's note is for older apps; the wait replaces it here.
			expect(screen.queryByTestId("cloud-session-startup-note")).toBeNull();
			// The elapsed time is live.
			act(() => { vi.advanceTimersByTime(1_000); });
			expect(screen.getByTestId("cloud-session-ready-check-progress")).toHaveTextContent("20:13 of 1:00:00");
		});

		it("shows the last lines the check printed and its exit status", () => {
			mount({ activeIndex: 0, readyCheck: check({ attempts: 1, exitCode: 3, output: "Bootstrapping step 2/5\nBootstrapping step 3/5\n" }) });
			const output = screen.getByRole("region", { name: "Waiting for your workspace's own setup" }).querySelector("pre");
			expect(output).toBe(screen.getByLabelText("Latest output of the ready check (exit 3)"));
			expect(output).toHaveTextContent("Bootstrapping step 2/5 Bootstrapping step 3/5");
			expect(screen.getByTestId("cloud-session-ready-check-progress")).toHaveTextContent("20:12 of 1:00:00 since Coder reported the workspace ready, 1 check so far");
		});

		it("leaves the limit out when the control plane sends none", () => {
			mount({ activeIndex: 0, readyCheck: check({ timeoutSeconds: undefined }) });
			expect(screen.getByTestId("cloud-session-ready-check-progress")).toHaveTextContent("20:12 since Coder reported the workspace ready, 31 checks so far");
		});

		it("moves on once the check passed or the screen completes", () => {
			const view = mount({ activeIndex: 0, readyCheck: check() });
			view.rerender(<TooltipProvider><CloudSessionStartupLoader activeIndex={1} /></TooltipProvider>);
			expect(screen.queryByTestId("cloud-session-ready-check")).toBeNull();
			expect(screen.getByText("Creating the workspace")).toBeInTheDocument();
			expect(screen.getByTestId("multi-step-loader-step")).toHaveTextContent("Connecting to the worker");
			view.rerender(<TooltipProvider><CloudSessionStartupLoader activeIndex={3} completed readyCheck={check()} /></TooltipProvider>);
			expect(screen.queryByTestId("cloud-session-ready-check")).toBeNull();
		});
	});
});
