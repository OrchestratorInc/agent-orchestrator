import { act, fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpClientEvent } from "../lib/cloud-cp";
import { forgetControlPlaneClock, groupScriptRuns, SCRIPT_STEP_EVENT } from "../lib/cloud-script-runs";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { claimQuietStepAnnouncement, ScriptRunPanel } from "./ScriptRunPanel";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	const result = rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
	return { ...result, rerender: (next: ReactElement) => result.rerender(<TooltipProvider>{next}</TooltipProvider>) };
}

let sequence = 0;
function stepEvent(payload: Record<string, unknown>): CloudCpClientEvent {
	sequence++;
	return {
		sessionId: "s1", sequence, type: SCRIPT_STEP_EVENT, createdAt: "2026-10-18T10:00:00Z",
		payload: { phase: "setup", runId: "run-1", total: 3, repository: "", ...payload },
	};
}

function runOf(...payloads: Record<string, unknown>[]) {
	return groupScriptRuns(payloads.map(stepEvent))[0];
}

const steps = () => screen.getAllByTestId("script-step");
/** A step row's disclosure button (its body also holds a copy button). */
const toggleOf = (row: HTMLElement) => row.querySelector<HTMLButtonElement>("button[aria-expanded]");

beforeEach(() => {
	sequence = 0;
	forgetControlPlaneClock();
	vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
	vi.setSystemTime(new Date("2026-10-18T10:00:12Z"));
});
afterEach(() => vi.useRealTimers());

describe("ScriptRunPanel", () => {
	it("shows a running run with the live step open, its output and a live timer", () => {
		const run = runOf(
			{ step: 1, status: "succeeded", command: "npm ci", exitCode: 0, startedAt: "2026-10-18T10:00:00Z", durationMs: 4000, output: "added 120 packages" },
			{ step: 2, status: "running", command: "docker compose up -d", repository: "api", startedAt: "2026-10-18T10:00:04Z", output: "Pulling db\nPulling cache" },
		);
		const { container } = render(<ScriptRunPanel run={run} />);
		expect(screen.getByText("Setup")).toBeInTheDocument();
		expect(screen.getByText("Running step 2 of 3")).toBeInTheDocument();
		expect(screen.getByLabelText("Elapsed 0:12")).toBeInTheDocument();
		const [first, second, third] = steps();
		expect(first).toHaveAttribute("data-status", "succeeded");
		expect(toggleOf(first)).toHaveAttribute("aria-expanded", "false");
		const running = toggleOf(second)!;
		expect(running).toHaveAttribute("aria-expanded", "true");
		expect(document.getElementById(running.getAttribute("aria-controls")!)).not.toBeNull();
		expect(within(second).getByText("api")).toBeInTheDocument();
		expect(within(second).getByText("0:08")).toBeInTheDocument();
		expect(within(second).getByTestId("script-step-output")).toHaveTextContent("Pulling db Pulling cache");
		expect(third).toHaveAttribute("data-status", "pending");
		expect(toggleOf(third)).toBeNull();
		act(() => { vi.advanceTimersByTime(3000); });
		expect(within(second).getByText("0:11")).toBeInTheDocument();
		expect(container.querySelector("[data-testid=script-run-panel]")).toMatchSnapshot();
	});

	it("follows new output only while the reader is at the bottom", () => {
		const base = { step: 1, status: "running", command: "make", startedAt: "2026-10-18T10:00:00Z" };
		const { rerender } = render(<ScriptRunPanel run={runOf({ ...base, output: "a" })} />);
		const pre = screen.getByTestId("script-step-output");
		Object.defineProperty(pre, "scrollHeight", { configurable: true, get: () => 1000 });
		Object.defineProperty(pre, "clientHeight", { configurable: true, get: () => 240 });
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf({ ...base, output: "a\nb" })} />);
		expect(pre.scrollTop).toBe(1000);
		pre.scrollTop = 100;
		fireEvent.scroll(pre);
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf({ ...base, output: "a\nb\nc" })} />);
		expect(pre.scrollTop).toBe(100);
	});

	it("collapses a succeeded run in the timeline and keeps its steps closed when opened", () => {
		vi.setSystemTime(new Date("2026-10-18T11:00:00Z"));
		const run = groupScriptRuns([
			stepEvent({ total: 2, step: 1, status: "succeeded", command: "npm ci", exitCode: 0, startedAt: "2026-10-18T10:00:00Z", durationMs: 4000 }),
			stepEvent({ total: 2, step: 2, status: "succeeded", command: "npm run build", exitCode: 0, startedAt: "2026-10-18T10:00:04Z", durationMs: 8000 }),
		])[0];
		const { container } = render(<ScriptRunPanel collapsible run={run} />);
		const header = screen.getByRole("button", { name: /Setup/ });
		expect(header).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByText("Succeeded")).toBeInTheDocument();
		expect(screen.getByLabelText("Elapsed 0:12")).toBeInTheDocument();
		expect(screen.queryAllByTestId("script-step")).toHaveLength(0);
		fireEvent.click(header);
		expect(header).toHaveAttribute("aria-expanded", "true");
		expect(steps()).toHaveLength(2);
		for (const row of steps()) expect(toggleOf(row)).toHaveAttribute("aria-expanded", "false");
		expect(container.querySelector("[data-testid=script-run-panel]")).toMatchSnapshot();
	});

	it("opens a failed step with its hint, who ran it and where, and dims the skipped steps", () => {
		const run = runOf(
			{ step: 1, status: "succeeded", command: "npm ci", exitCode: 0, startedAt: "2026-10-18T10:00:00Z", durationMs: 1000 },
			{
				step: 2, status: "failed", command: "docker compose up -d", exitCode: 1, startedAt: "2026-10-18T10:00:01Z", durationMs: 2000,
				output: "permission denied while trying to connect to the Docker daemon socket", outputTruncated: true,
				logPath: "/home/coder/.ao/setup-logs/step-2.log", user: "ao-worker", groups: ["ao-worker"], dir: "/home/coder/app",
				hint: "This step ran as ao-worker, which cannot use Docker.",
			},
			{ step: 3, status: "skipped", command: "npm test" },
		);
		const { container } = render(<ScriptRunPanel collapsible run={run} />);
		expect(screen.getByText("Failed at step 2")).toBeInTheDocument();
		const [, failed, skipped] = steps();
		expect(toggleOf(failed)).toHaveAttribute("aria-expanded", "true");
		expect(within(failed).getByText("This step ran as ao-worker, which cannot use Docker.")).toBeInTheDocument();
		expect(within(failed).getByText("Exited with code 1")).toBeInTheDocument();
		expect(within(failed).getByText("Ran as ao-worker (groups: ao-worker)")).toBeInTheDocument();
		expect(within(failed).getByText("Ran in /home/coder/app")).toBeInTheDocument();
		expect(within(failed).getByText("Showing the end of the output. Full output: /home/coder/.ao/setup-logs/step-2.log")).toBeInTheDocument();
		expect(within(failed).getByRole("button", { name: "Copy output" })).toBeInTheDocument();
		expect(skipped).toHaveAttribute("data-status", "skipped");
		expect(toggleOf(skipped)).toBeNull();
		expect(container.querySelector("[data-testid=script-run-panel]")).toMatchSnapshot();
	});

	it("respects a manual toggle as the run advances", () => {
		const first = { step: 1, command: "npm ci", startedAt: "2026-10-18T10:00:00Z" };
		const { rerender } = render(<ScriptRunPanel run={runOf({ ...first, status: "running", output: "x" })} />);
		const button = () => toggleOf(steps()[0])!;
		expect(button()).toHaveAttribute("aria-expanded", "true");
		// Finishing closes it by default...
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf({ ...first, status: "succeeded", exitCode: 0, durationMs: 1000, output: "x" })} />);
		expect(button()).toHaveAttribute("aria-expanded", "false");
		// ...but a reader who opens it keeps it open.
		fireEvent.click(button());
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf(
			{ ...first, status: "succeeded", exitCode: 0, durationMs: 1000, output: "x" },
			{ step: 2, command: "make", status: "running", startedAt: "2026-10-18T10:00:01Z" },
		)} />);
		expect(button()).toHaveAttribute("aria-expanded", "true");
		expect(within(steps()[1]).getByText("No output yet")).toBeInTheDocument();
	});

	it("toggles a step from the keyboard", () => {
		render(<ScriptRunPanel run={runOf({ total: 1, step: 1, status: "succeeded", command: "true", exitCode: 0, durationMs: 5 })} />);
		const button = toggleOf(steps()[0])!;
		button.focus();
		expect(document.activeElement).toBe(button);
		// Native buttons activate on Enter and Space; the browser turns either into a click.
		fireEvent.click(button);
		expect(button).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("No output")).toBeInTheDocument();
	});

	it("draws an old Cleanup hook's steps without durations or a bogus truncation note", () => {
		const run = groupScriptRuns([
			stepEvent({ phase: "cleanup", runId: "c", total: 2, step: 1, status: "succeeded", exitCode: 0, command: "", output: "", outputTruncated: true, logPath: "/x/cleanup.log" }),
			stepEvent({ phase: "cleanup", runId: "c", total: 2, step: 2, status: "failed", exitCode: 1, command: "", output: "cleanup step 2 failed\nran as ao-worker", logPath: "/x/cleanup.log" }),
		])[0];
		render(<ScriptRunPanel run={run} />);
		expect(screen.getByText("Failed at step 2")).toBeInTheDocument();
		expect(screen.queryByLabelText(/Elapsed/)).toBeNull();
		const [first, second] = steps();
		expect(within(first).getByText("Step 1")).toBeInTheDocument();
		expect(within(first).queryByText(/\d:\d\d/)).toBeNull();
		fireEvent.click(toggleOf(first)!);
		expect(within(first).getByText("No output")).toBeInTheDocument();
		expect(within(first).getByText("Full output: /x/cleanup.log")).toBeInTheDocument();
		expect(within(first).queryByText(/Showing the end/)).toBeNull();
		expect(within(second).getByTestId("script-step-output")).toHaveTextContent("cleanup step 2 failed");
		expect(within(second).getByText("Exited with code 1")).toBeInTheDocument();
	});

	it("shows a cleanup run and an interrupted run truthfully", () => {
		const runs = groupScriptRuns([
			stepEvent({ runId: "old", total: 2, step: 1, status: "running", command: "npm ci", startedAt: "2026-10-18T09:00:00Z" }),
			stepEvent({ runId: "cleanup-1", phase: "cleanup", total: 1, step: 1, status: "succeeded", command: "docker compose down", exitCode: 0, startedAt: "2026-10-18T10:00:00Z", durationMs: 3000 }),
			stepEvent({ runId: "new", total: 2, step: 1, status: "running", command: "npm ci", startedAt: "2026-10-18T10:00:05Z" }),
		]);
		const { container, unmount } = render(<ScriptRunPanel run={runs[0]} />);
		expect(screen.getByText("Stopped before finishing")).toBeInTheDocument();
		expect(steps()[0]).toHaveAttribute("data-status", "stopped");
		expect(container.querySelector(".animate-spin")).toBeNull();
		unmount();
		render(<ScriptRunPanel run={runs[1]} />);
		expect(screen.getByText("Cleanup")).toBeInTheDocument();
		expect(screen.getByText("Succeeded", { selector: ".truncate" })).toBeInTheDocument();
		expect(screen.getByText("docker compose down")).toBeInTheDocument();
	});

	it("announces step transitions in a polite status region, never the output", () => {
		const announcer = () => screen.getByTestId("script-run-announcer");
		const first = { step: 1, command: "npm ci", startedAt: "2026-10-18T10:00:00Z" };
		const { rerender } = render(<ScriptRunPanel run={runOf({ ...first, status: "running", output: "a" })} />);
		expect(announcer()).toHaveAttribute("role", "status");
		expect(announcer()).toHaveAttribute("aria-live", "polite");
		// What the panel first draws is not announced.
		expect(announcer()).toHaveTextContent("");
		expect(screen.getByTestId("script-step-output")).not.toHaveAttribute("aria-live");
		// New output alone says nothing.
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf({ ...first, status: "running", output: "a\nb" })} />);
		expect(announcer()).toHaveTextContent("");
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf(
			{ ...first, status: "succeeded", exitCode: 0, durationMs: 1000, output: "a\nb" },
			{ step: 2, command: "make", status: "running", startedAt: "2026-10-18T10:00:01Z" },
		)} />);
		expect(announcer()).toHaveTextContent("Setup step 1 succeeded Setup step 2 of 3 started: make");
		sequence = 0;
		rerender(<ScriptRunPanel run={runOf(
			{ ...first, status: "succeeded", exitCode: 0, durationMs: 1000, output: "a\nb" },
			{ step: 2, command: "make", status: "failed", exitCode: 2, startedAt: "2026-10-18T10:00:01Z", durationMs: 500, output: "boom" },
			{ step: 3, command: "npm test", status: "skipped" },
		)} />);
		expect(announcer()).toHaveTextContent("Setup step 2 failed");
		expect(announcer().textContent).not.toContain("boom");
	});

	it("lists a hidden run's steps and statuses with a note instead of output, and nothing to copy", () => {
		const run = runOf(
			{ step: 1, status: "succeeded", command: "npm ci", exitCode: 0, durationMs: 1000, outputHidden: true, output: "" },
			{ step: 2, status: "failed", command: "make", exitCode: 1, durationMs: 500, outputHidden: true, output: "" },
			{ step: 3, status: "skipped", command: "npm test" },
		);
		render(<ScriptRunPanel run={run} />);
		expect(screen.getByText("Failed at step 2")).toBeInTheDocument();
		expect(steps().map((row) => row.getAttribute("data-status"))).toEqual(["succeeded", "failed", "skipped"]);
		const failed = steps()[1];
		expect(toggleOf(failed)).toHaveAttribute("aria-expanded", "true");
		expect(within(failed).getByText("Output is visible to organization members only.")).toBeInTheDocument();
		expect(within(failed).getByText("Exited with code 1")).toBeInTheDocument();
		expect(within(failed).queryByTestId("script-step-output")).toBeNull();
		expect(screen.queryByRole("button", { name: "Copy output" })).toBeNull();
		fireEvent.click(toggleOf(steps()[0])!);
		expect(within(steps()[0]).getByText("Output is visible to organization members only.")).toBeInTheDocument();
	});

	it("explains a Cleanup that could not run in the reader's language", () => {
		const run = groupScriptRuns([
			stepEvent({ phase: "cleanup", runId: "c", total: 2, step: 1, status: "skipped", command: "docker compose down", hint: "The sandbox was paused and did not resume." }),
			stepEvent({ phase: "cleanup", runId: "c", total: 2, step: 2, status: "skipped", command: "rm -rf tmp", hint: "The sandbox was paused and did not resume." }),
		])[0];
		const { container } = render(<ScriptRunPanel collapsible run={run} />);
		expect(screen.getByText("Did not run")).toBeInTheDocument();
		expect(screen.getByText("AO could not run Cleanup before deleting the sandbox, so none of its steps ran.")).toBeInTheDocument();
		expect(steps().map((row) => row.getAttribute("data-status"))).toEqual(["skipped", "skipped"]);
		expect(container.querySelector(".animate-spin")).toBeNull();
		expect(screen.queryByText(/did not resume/)).toBeNull();
	});

	it("stops the spinner of a run that ended without reporting its last step", () => {
		const run = groupScriptRuns([
			stepEvent({ total: 2, step: 1, status: "running", command: "npm ci", startedAt: "2026-10-18T10:00:00Z", output: "added 10" }),
		], { sessionEnded: true })[0];
		const { container } = render(<ScriptRunPanel run={run} />);
		expect(screen.getByText("Ended without reporting a result")).toBeInTheDocument();
		const [first, second] = steps();
		expect(first).toHaveAttribute("data-status", "unknown");
		expect(within(first).getByText("Result unknown")).toBeInTheDocument();
		expect(second).toHaveAttribute("data-status", "pending");
		expect(container.querySelector(".animate-spin")).toBeNull();
		expect(screen.queryByLabelText(/Elapsed/)).toBeNull();
	});
});

describe("ScriptRunPanel step time limit", () => {
	it("shows the running step's limit beside its time, and only while it runs", () => {
		const run = runOf(
			{ step: 1, status: "succeeded", command: "npm ci", exitCode: 0, startedAt: "2026-10-18T10:00:00Z", durationMs: 4000, output: "", timeoutMinutes: 10 },
			{ step: 2, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:04Z", output: "", timeoutMinutes: 45 },
		);
		render(<ScriptRunPanel run={run} />);
		const [first, second] = steps();
		expect(within(second).getByText("/ 45:00", { exact: false })).toBeInTheDocument();
		expect(within(second).getByText(", limit 45:00")).toHaveClass("sr-only");
		expect(within(first).queryByText(/\/ 10:00/)).toBeNull();
	});

	it("shows no limit for a step from a worker that does not report one", () => {
		const run = runOf({ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:04Z", output: "" });
		render(<ScriptRunPanel run={run} />);
		expect(within(steps()[0]).queryByText(/limit/)).toBeNull();
		expect(within(steps()[0]).getByText("0:08")).toBeInTheDocument();
	});
});

describe("ScriptRunPanel quiet step note", () => {
	function quietRun(createdAts: string[], payloads: Record<string, unknown>[]) {
		return groupScriptRuns(payloads.map((payload, index) => ({ ...stepEvent(payload), createdAt: createdAts[index] })))[0];
	}

	it("says a running step has printed nothing for 3 minutes, with its limit, and keeps counting", () => {
		vi.setSystemTime(new Date("2026-10-18T10:02:59Z"));
		// Started at 10:00:00; its output last changed at 10:00:30.
		const run = quietRun(["2026-10-18T10:00:00Z", "2026-10-18T10:00:30Z"], [
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "", timeoutMinutes: 10 },
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "seeding", timeoutMinutes: 10 },
		]);
		render(<ScriptRunPanel run={run} />);
		expect(screen.queryByTestId("script-step-quiet")).toBeNull();
		act(() => { vi.advanceTimersByTime(31_000); });
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 3 min. The step is still running (limit 10 min).");
		act(() => { vi.advanceTimersByTime(60_000); });
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 4 min.");
	});

	it("counts from when the step last wrote any output, not when its tail last changed", () => {
		vi.setSystemTime(new Date("2026-10-18T10:05:00Z"));
		// The tail has not changed since 10:00:30, but a heartbeat at 10:04:40
		// says the step wrote output at 10:04:20 (control plane time), so it is
		// not silent; the worker's own startedAt clock is far behind and ignored.
		const busy = quietRun(["2026-10-18T10:00:00Z", "2026-10-18T10:04:40Z"], [
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T09:40:00Z", output: "" },
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T09:40:00Z", output: "seeding", outputAt: "2026-10-18T10:04:20Z" },
		]);
		const { rerender } = render(<ScriptRunPanel run={busy} />);
		expect(screen.queryByTestId("script-step-quiet")).toBeNull();
		const silent = quietRun(["2026-10-18T10:00:00Z", "2026-10-18T10:04:40Z"], [
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "" },
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "seeding", outputAt: "2026-10-18T10:01:00Z" },
		]);
		rerender(<ScriptRunPanel run={silent} />);
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 4 min.");
	});

	it("announces a quiet step once when its note appears, not on every minute it counts", () => {
		vi.setSystemTime(new Date("2026-10-18T10:02:59Z"));
		const run = quietRun(["2026-10-18T10:00:00Z"], [
			{ runId: "run-quiet-once", step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "", timeoutMinutes: 10 },
		]);
		render(<ScriptRunPanel run={run} />);
		const announcer = screen.getByTestId("script-run-quiet-announcer");
		expect(announcer).toBeEmptyDOMElement();
		act(() => { vi.advanceTimersByTime(1_000); });
		expect(announcer).toHaveTextContent("No output for 3 min. The step is still running (limit 10 min).");
		act(() => { vi.advanceTimersByTime(60_000); });
		// The note counts on; the live region keeps its first wording, so it is not spoken again.
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 4 min.");
		expect(screen.getByTestId("script-step-quiet")).not.toHaveAttribute("role");
		expect(screen.getByTestId("script-step-quiet").closest("[role=status], [aria-live]")).toBeNull();
		expect(announcer).toHaveTextContent("No output for 3 min.");
	});

	it("announces a quiet step from only one of the panels showing its run", () => {
		vi.setSystemTime(new Date("2026-10-18T10:04:00Z"));
		const run = quietRun(["2026-10-18T10:00:00Z"], [
			{ runId: "run-quiet-shared", step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "" },
		]);
		render(<><ScriptRunPanel run={run} /><ScriptRunPanel run={run} showTitle={false} /><ScriptRunPanel collapsible run={run} /></>);
		expect(screen.getAllByTestId("script-step-quiet")).toHaveLength(3);
		const spoken = screen.getAllByTestId("script-run-quiet-announcer").filter((node) => node.textContent !== "");
		expect(spoken).toHaveLength(1);
	});

	it("measures the silence on the control plane's clock when the desktop clock runs fast", () => {
		// The desktop clock is 5 minutes ahead: control plane 10:00:00 is 10:05:00 here.
		vi.setSystemTime(new Date("2026-10-18T10:05:00Z"));
		const started = { runId: "run-fast-clock", step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T09:59:59Z", output: "" };
		const { rerender } = render(<ScriptRunPanel run={quietRun(["2026-10-18T09:59:59Z"], [started])} />);
		// Output arrives live: written at 10:00:10 control plane time, seen at 10:05:10 here.
		act(() => { vi.advanceTimersByTime(10_000); });
		rerender(<ScriptRunPanel run={quietRun(["2026-10-18T09:59:59Z", "2026-10-18T10:00:10Z"], [
			started,
			{ ...started, output: "seeding", outputAt: "2026-10-18T10:00:10Z" },
		])} />);
		act(() => { vi.advanceTimersByTime(1_000); });
		expect(screen.queryByTestId("script-step-quiet")).toBeNull();
		act(() => { vi.advanceTimersByTime(3 * 60_000); });
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 3 min.");
	});

	it("goes away when new output arrives or the step ends, and omits an unknown limit", () => {
		vi.setSystemTime(new Date("2026-10-18T10:05:00Z"));
		const quiet = quietRun(["2026-10-18T10:00:00Z"], [
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "" },
		]);
		const { rerender } = render(<ScriptRunPanel run={quiet} />);
		expect(screen.getByTestId("script-step-quiet")).toHaveTextContent("No output for 5 min. The step is still running.");
		expect(screen.getByTestId("script-step-quiet")).not.toHaveTextContent("limit");
		const fresh = quietRun(["2026-10-18T10:00:00Z", "2026-10-18T10:04:50Z"], [
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "" },
			{ step: 1, status: "running", command: "make seed", startedAt: "2026-10-18T10:00:00Z", output: "row 1" },
		]);
		rerender(<ScriptRunPanel run={fresh} />);
		expect(screen.queryByTestId("script-step-quiet")).toBeNull();
		const done = quietRun(["2026-10-18T10:00:00Z"], [
			{ step: 1, status: "succeeded", command: "make seed", startedAt: "2026-10-18T10:00:00Z", exitCode: 0, durationMs: 300_000, output: "" },
		]);
		rerender(<ScriptRunPanel run={done} />);
		expect(screen.queryByTestId("script-step-quiet")).toBeNull();
	});
});

describe("ScriptRunPanel beside the harness update notice", () => {
	afterEach(() => {
		act(() => publishHarnessUpdateNoticeRect(null));
		vi.restoreAllMocks();
	});

	it("pads its rows so the timers stay clear of the notice, and drops the padding once it goes", () => {
		vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			return this.dataset.testid === "script-run-panel" ? new DOMRect(404, 80, 766, 120) : new DOMRect();
		});
		const run = runOf({ step: 1, status: "running", command: "sleep 200", startedAt: "2026-10-18T10:00:00Z", timeoutMinutes: 5 });
		render(<ScriptRunPanel run={run} />);
		const panel = screen.getByTestId("script-run-panel");
		expect(panel.style.getPropertyValue("--script-run-notice-clearance")).toBe("");

		act(() => publishHarnessUpdateNoticeRect({ left: 1004, top: 72, right: 1304, bottom: 140 }));
		expect(panel.style.getPropertyValue("--script-run-notice-clearance")).toBe("178px");
		const timer = within(steps()[0]!).getByText("0:12");
		expect(timer.closest("button")).toHaveClass("pr-[calc(0.625rem+var(--script-run-notice-clearance,0px))]");

		act(() => publishHarnessUpdateNoticeRect(null));
		expect(panel.style.getPropertyValue("--script-run-notice-clearance")).toBe("");
	});
});

it("forgets the oldest announced quiet notes instead of keeping every one", () => {
	expect(claimQuietStepAnnouncement("prune-test:first")).toBe(true);
	expect(claimQuietStepAnnouncement("prune-test:first")).toBe(false);
	for (let index = 0; index < 200; index++) claimQuietStepAnnouncement(`prune-test:${index}`);
	expect(claimQuietStepAnnouncement("prune-test:199")).toBe(false);
	expect(claimQuietStepAnnouncement("prune-test:first")).toBe(true);
});
