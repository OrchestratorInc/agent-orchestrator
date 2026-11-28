import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExitFeedbackRequest, ExitFeedbackState } from "../../shared/exit-feedback";

const telemetry = vi.hoisted(() => ({
	captureRendererEvent: vi.fn(async (_event: string, _properties?: Record<string, unknown>) => true),
	exitFeedbackAllowed: vi.fn(async () => true),
}));
vi.mock("../lib/telemetry", () => telemetry);

import { aoBridge } from "../lib/bridge";
import { ExitFeedbackPrompt } from "./ExitFeedbackPrompt";

let request: ((value: ExitFeedbackRequest) => void) | undefined;
let secondClose: (() => void) | undefined;
let report: ReturnType<typeof vi.fn<(state: ExitFeedbackState) => void>>;

beforeEach(() => {
	telemetry.captureRendererEvent.mockReset().mockResolvedValue(true);
	telemetry.exitFeedbackAllowed.mockReset().mockResolvedValue(true);
	vi.spyOn(aoBridge.app, "onExitFeedbackRequest").mockImplementation((listener) => {
		request = listener;
		return () => undefined;
	});
	vi.spyOn(aoBridge.app, "onExitFeedbackSecondClose").mockImplementation((listener) => {
		secondClose = listener;
		return () => undefined;
	});
	report = vi.fn<(state: ExitFeedbackState) => void>();
	vi.spyOn(aoBridge.app, "reportExitFeedback").mockImplementation(report);
});
afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

async function open(version: ExitFeedbackRequest["version"]) {
	render(<ExitFeedbackPrompt />);
	await act(async () => request?.({ version }));
	return screen.findByRole("dialog");
}

const eventsNamed = (name: string) => telemetry.captureRendererEvent.mock.calls.filter(([event]) => event === name).map(([, properties]) => properties);

describe("ExitFeedbackPrompt", () => {
	it("shows version A with the exact wording, disclosure and skip", async () => {
		const dialog = await open("no_project");
		expect(dialog).toHaveAccessibleName("What stopped you from getting started today?");
		const answers = screen.getAllByRole("button").map((button) => button.textContent);
		expect(answers).toEqual([
			"Just looking around for now",
			"I wasn't sure where to start",
			"I didn't have a repo or coding agent ready",
			"Something didn't work",
			"It's not what I was looking for",
			"Skip and close",
		]);
		expect(screen.getByPlaceholderText("Anything else? (optional)")).toHaveAttribute("maxLength", "500");
		expect(dialog).toHaveAccessibleDescription("Your answer is sent to Orchestrator and stored with your usage analytics. What we collect");
		expect(screen.getByRole("link", { name: "What we collect" })).toHaveAttribute("href", expect.stringContaining("docs/telemetry.md"));
		expect(report).toHaveBeenCalledWith("shown");
		expect(eventsNamed("ao.exit_feedback.shown")).toEqual([{ version: "no_project" }]);
	});

	it("shows version B to an install with a project", async () => {
		const dialog = await open("project_no_work");
		expect(dialog).toHaveAccessibleName("You added a project but didn't start any work. What held you back?");
		for (const label of ["I didn't have a task in mind yet", "I wasn't sure what to ask it", "The coding agent wasn't ready", "I tried, but nothing started", "I wasn't comfortable letting it change my code"]) {
			expect(screen.getByRole("button", { name: label })).toBeInTheDocument();
		}
	});

	it("opens with focus off every answer, so Enter or Space picks nothing", async () => {
		const dialog = await open("no_project");
		await waitFor(() => expect(dialog).toHaveFocus());
		for (const button of screen.getAllByRole("button")) expect(button).not.toHaveFocus();
		fireEvent.keyDown(dialog, { key: "Enter" });
		fireEvent.keyDown(dialog, { key: " " });
		expect(eventsNamed("ao.exit_feedback.answered")).toEqual([]);
		expect(report).not.toHaveBeenCalledWith("done");
	});

	it("sends the tapped answer with the typed text, then lets the app close", async () => {
		await open("project_no_work");
		fireEvent.change(screen.getByPlaceholderText("Anything else? (optional)"), { target: { value: "needs docs" } });
		fireEvent.click(screen.getByRole("button", { name: "The coding agent wasn't ready" }));
		await waitFor(() => expect(report).toHaveBeenCalledWith("done"));
		expect(eventsNamed("ao.exit_feedback.answered")).toEqual([
			{ version: "project_no_work", answer: "agent_not_ready", text: "needs docs", seconds_open: 0 },
		]);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});

	it.each([
		["Skip and close", "skip"],
		["Escape", "escape"],
		["a second quit", "second_close"],
	])("records %s as a skip without the typed text", async (action, how) => {
		const dialog = await open("no_project");
		// Escape is ignored once something is typed, so it is pressed on an empty box.
		if (how !== "escape") fireEvent.change(screen.getByPlaceholderText("Anything else? (optional)"), { target: { value: "private note" } });
		if (how === "skip") fireEvent.click(screen.getByRole("button", { name: action }));
		else if (how === "escape") fireEvent.keyDown(dialog, { key: "Escape" });
		else await act(async () => secondClose?.());
		await waitFor(() => expect(report).toHaveBeenCalledWith("done"));
		expect(eventsNamed("ao.exit_feedback.dismissed")).toEqual([{ version: "no_project", how, seconds_open: 0 }]);
		expect(JSON.stringify(telemetry.captureRendererEvent.mock.calls)).not.toContain("private note");
	});

	it("keeps the dialog and the text when Escape follows typing or ends an input method composition", async () => {
		const dialog = await open("no_project");
		fireEvent.keyDown(dialog, { key: "Escape", isComposing: true });
		fireEvent.keyDown(dialog, { key: "Escape", keyCode: 229 });
		const box = screen.getByPlaceholderText("Anything else? (optional)");
		fireEvent.change(box, { target: { value: "half a thought" } });
		fireEvent.keyDown(box, { key: "Escape" });
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(eventsNamed("ao.exit_feedback.dismissed")).toEqual([]);
		expect(box).toHaveValue("half a thought");
	});

	it("times out after 15 seconds of nothing, and typing stops the countdown for good", async () => {
		vi.useFakeTimers();
		render(<ExitFeedbackPrompt />);
		await act(async () => request?.({ version: "no_project" }));
		await act(async () => vi.advanceTimersByTimeAsync(14_999));
		expect(report).not.toHaveBeenCalledWith("done");
		await act(async () => vi.advanceTimersByTimeAsync(1));
		expect(eventsNamed("ao.exit_feedback.dismissed")).toEqual([{ version: "no_project", how: "timeout", seconds_open: 15 }]);
		expect(report).toHaveBeenCalledWith("done");
	});

	it("waits for an answer once the user starts typing", async () => {
		vi.useFakeTimers();
		render(<ExitFeedbackPrompt />);
		await act(async () => request?.({ version: "no_project" }));
		await act(async () => vi.advanceTimersByTimeAsync(5_000));
		fireEvent.change(screen.getByPlaceholderText("Anything else? (optional)"), { target: { value: "hm" } });
		expect(report).toHaveBeenCalledWith("typing");
		await act(async () => vi.advanceTimersByTimeAsync(120_000));
		expect(eventsNamed("ao.exit_feedback.dismissed")).toEqual([]);
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("declines without drawing or sending when analytics is off or the events are switched off", async () => {
		telemetry.exitFeedbackAllowed.mockResolvedValue(false);
		render(<ExitFeedbackPrompt />);
		await act(async () => request?.({ version: "no_project" }));
		expect(report).toHaveBeenCalledWith("declined");
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(telemetry.captureRendererEvent).not.toHaveBeenCalled();
	});

	it("declines when the analytics check fails", async () => {
		telemetry.exitFeedbackAllowed.mockRejectedValue(new Error("bridge gone"));
		render(<ExitFeedbackPrompt />);
		await act(async () => request?.({ version: "no_project" }));
		expect(report).toHaveBeenCalledWith("declined");
	});

	it("still lets the app close when sending fails", async () => {
		telemetry.captureRendererEvent.mockRejectedValue(new Error("offline"));
		await open("no_project");
		fireEvent.click(screen.getByRole("button", { name: "Something didn't work" }));
		await waitFor(() => expect(report).toHaveBeenCalledWith("done"));
	});
});
