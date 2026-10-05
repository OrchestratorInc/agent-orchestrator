import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { StartupCueBanner } from "./StartupCueBanner";
import type { components } from "../../api/schema";

const { toast, dismiss } = vi.hoisted(() => ({ toast: vi.fn(), dismiss: vi.fn() }));
vi.mock("../stores/ui-store", () => ({ useUiStore: { getState: () => ({ showGlobalToast: toast, dismissGlobalToast: dismiss, globalToasts: [] }) } }));
beforeEach(() => { toast.mockReset(); dismiss.mockReset(); localStorage.clear(); });
afterEach(cleanup);
const run: components["schemas"]["StartupCueRun"] = {
	cueId: "cue", name: "Dependencies", command: "npm install",
	state: "running", startedAt: new Date().toISOString(),
};

test("explains startup hold above the input without a success toast", () => {
	const view = render(<StartupCueBanner sessionId="session" run={run} />);
	expect(screen.getByRole("status")).toHaveTextContent("Running startup cue: Dependencies");
	expect(screen.getByRole("status")).toHaveTextContent("Messages are queued");
	view.rerender(<StartupCueBanner sessionId="session" run={{ ...run, state: "succeeded" }} />);
	expect(screen.queryByRole("status")).toBeNull();
	expect(toast).not.toHaveBeenCalled();
});

test("failed setup clears its loader and retains output while allowing the session to continue", () => {
	render(<StartupCueBanner sessionId="session" run={{ ...run, state: "failed", error: "Exited with code 1", output: "dependency missing" }} />);
	expect(screen.getByRole("alert")).toHaveTextContent("Startup cue failed; session continued.");
	expect(screen.queryByRole("status")).toBeNull();
	fireEvent.click(screen.getByText("Command and output: Dependencies"));
	expect(screen.getByText(/dependency missing/)).toBeInTheDocument();
	expect(toast).toHaveBeenCalledExactlyOnceWith("Startup cue failed; session continued", "Exited with code 1", {
		tone: "error", dedupeKey: `startup-cue:session:${run.startedAt}`,
	});
});

test("dismissal survives remount and suppresses the toast", () => {
	const failed = { ...run, state: "failed" as const, error: "Exited with code 1", output: "dependency missing" };
	const view = render(<StartupCueBanner sessionId="session" run={failed} />);
	fireEvent.click(screen.getByRole("button", { name: "Dismiss startup cue error" }));
	expect(screen.queryByRole("alert")).toBeNull();
	view.unmount();
	render(<StartupCueBanner sessionId="session" run={failed} />);
	expect(screen.queryByRole("alert")).toBeNull();
	expect(toast).toHaveBeenCalledOnce();
});
