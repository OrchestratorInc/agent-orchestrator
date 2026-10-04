import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { StartupCueBanner } from "./StartupCueBanner";
import type { components } from "../../api/schema";

const { toast } = vi.hoisted(() => ({ toast: vi.fn() }));
vi.mock("../stores/ui-store", () => ({ useUiStore: { getState: () => ({ showGlobalToast: toast }) } }));
beforeEach(() => toast.mockReset());
afterEach(cleanup);
const run: components["schemas"]["StartupCueRun"] = {
	cueId: "cue", name: "Dependencies", command: "npm install", shell: "sh", timeoutSeconds: 600,
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
