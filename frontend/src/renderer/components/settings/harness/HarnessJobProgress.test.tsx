import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../../i18n";
import type { InstallJob } from "../../../hooks/useHarnessUpdates";
import { formatElapsed, HarnessJobSlowDetail, JobElapsed, terminalText } from "./HarnessJobProgress";

const NOW = Date.parse("2026-10-20T12:00:00Z");

function job(overrides: Partial<InstallJob> = {}): InstallJob {
	return { target: "droid", status: "installing", startedAt: new Date(NOW - 161_000).toISOString(), ...overrides };
}

describe("harness job progress", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.setSystemTime(NOW);
	});
	afterEach(() => { cleanup(); vi.useRealTimers(); });

	it("formats elapsed time as a clock", () => {
		expect(formatElapsed(-5_000)).toBe("0:00");
		expect(formatElapsed(7_900)).toBe("0:07");
		expect(formatElapsed(161_000)).toBe("2:41");
		expect(formatElapsed(3_723_000)).toBe("1:02:03");
	});

	it("shows installer output as a terminal would", () => {
		expect(terminalText("\x1b[32mfetching\x1b[0m droid\r\n 10%\r 55%\r 90%\nready\n\n")).toBe("fetching droid\n 90%\nready");
	});

	it("counts up from when the job started and stops counting once it ends", () => {
		const { container, rerender } = render(<JobElapsed job={job()} />);
		expect(container).toHaveTextContent("2:41");
		act(() => { vi.advanceTimersByTime(3_000); });
		expect(container).toHaveTextContent("2:44");
		expect(container.querySelector("[data-job-elapsed]")).toHaveAttribute("aria-hidden", "true");
		rerender(<JobElapsed job={job({ status: "succeeded" })} />);
		expect(container).toBeEmptyDOMElement();
		rerender(<JobElapsed job={job({ status: "waiting_for_idle" })} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("times an update that waited for idle sessions from when its command started", () => {
		const waited = job({ startedAt: new Date(NOW - 20 * 60_000).toISOString(), runStartedAt: new Date(NOW - 3_000).toISOString() });
		const { container } = render(<><JobElapsed job={waited} /><HarnessJobSlowDetail job={waited} /></>);
		expect(container).toHaveTextContent("0:03");
		expect(screen.queryByText(/Still downloading/)).toBeNull();
	});

	it("waits a minute before explaining a slow download", () => {
		const { container } = render(<HarnessJobSlowDetail job={job({ startedAt: new Date(NOW - 45_000).toISOString() })} />);
		expect(container).toBeEmptyDOMElement();
		act(() => { vi.advanceTimersByTime(15_000); });
		expect(screen.getByRole("status")).toHaveTextContent("Still downloading. This can take a few minutes on slow networks.");
		expect(screen.getByRole("button", { name: "Show output" })).toHaveAttribute("aria-expanded", "false");
	});

	it("reveals the output printed so far and follows it as it grows", async () => {
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		const { rerender } = render(<HarnessJobSlowDetail job={job()} />);
		await user.click(screen.getByRole("button", { name: "Show output" }));
		expect(screen.getByRole("button", { name: "Hide output" })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByLabelText("Installer output")).toHaveTextContent("No output yet");
		rerender(<HarnessJobSlowDetail job={job({ output: "Downloading droid 0.245.0\n 42%" })} />);
		expect(screen.getByLabelText("Installer output")).toHaveTextContent("Downloading droid 0.245.0 42%");
		await user.click(screen.getByRole("button", { name: "Hide output" }));
		expect(screen.queryByLabelText("Installer output")).toBeNull();
	});

	it("keeps the output but drops the download hint while verifying", () => {
		render(<HarnessJobSlowDetail job={job({ status: "verifying" })} />);
		expect(screen.queryByRole("status")).toBeNull();
		expect(screen.getByRole("button", { name: "Show output" })).toBeInTheDocument();
	});
});
