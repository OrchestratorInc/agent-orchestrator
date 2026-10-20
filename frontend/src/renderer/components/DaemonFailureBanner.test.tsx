import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { captureRendererEvent } from "../lib/telemetry";
import { appI18n } from "../i18n";
import { DaemonFailureBanner } from "./DaemonFailureBanner";

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

describe("DaemonFailureBanner", () => {
	afterEach(async () => {
		vi.useRealTimers();
		vi.restoreAllMocks();
		await appI18n.changeLanguage("en");
	});

	it("shows the daemon failure message, code, and actionable hint", () => {
		render(
			<DaemonFailureBanner
				status={{
					state: "stopped",
					code: "exited",
					message: "AO daemon exited with code 1",
					details: "go: go.mod requires go >= 1.25.7",
				}}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("AO daemon failed to start");
		expect(screen.getByRole("alert")).toHaveTextContent("AO daemon exited with code 1");
		expect(screen.getByText("exited")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Restart daemon" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByText("go: go.mod requires go >= 1.25.7")).toBeInTheDocument();
	});

	it("resets copy feedback when failure details change", async () => {
		const { rerender } = render(
			<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "first failure" }} />,
		);

		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
		});
		expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();

		rerender(<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "second failure" }} />);

		expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
	});

	it("resets copy feedback after two seconds", async () => {
		vi.useFakeTimers();
		render(<DaemonFailureBanner status={{ state: "stopped", code: "exited", details: "failure" }} />);

		await act(async () => {
			fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
		});
		expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();

		act(() => vi.advanceTimersByTime(2_000));

		expect(screen.getByRole("button", { name: "Copy details" })).toBeInTheDocument();
	});

	it("renders nothing while the daemon is not in an error state", () => {
		const { container } = render(<DaemonFailureBanner status={{ state: "starting" }} />);

		expect(container).toBeEmptyDOMElement();
	});

	it("shows slow startup as progress without offering a restart", () => {
		render(
			<DaemonFailureBanner
				status={{
					state: "starting",
					message: "AO daemon is still starting. Session recovery can take a while.",
					details: "restoring session mer-3",
				}}
			/>,
		);

		expect(screen.getByRole("status")).toHaveTextContent("AO daemon is not ready yet");
		expect(screen.getByRole("status")).toHaveTextContent("AO daemon is still starting");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Restart daemon" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Show details" }));
		expect(screen.getByText("restoring session mer-3")).toBeInTheDocument();
	});

	it("updates an already-mounted failure when the language changes", async () => {
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);
		expect(screen.getByRole("button", { name: "Restart daemon" })).toBeInTheDocument();

		await act(() => appI18n.changeLanguage("zh-CN"));

		expect(screen.getByRole("button", { name: "重启守护进程" })).toBeInTheDocument();
		expect(screen.getByRole("alert")).toHaveTextContent("AO 守护进程尚未就绪");
	});

	it("restarts a daemon that timed out during startup", async () => {
		const restart = vi.spyOn(aoBridge.daemon, "restart").mockResolvedValue({ state: "starting" });
		render(
			<DaemonFailureBanner
				status={{
					state: "error",
					code: "not_ready",
					message: "AO daemon did not finish starting within 30 seconds.",
				}}
			/>,
		);

		fireEvent.click(screen.getByRole("button", { name: "Restart daemon" }));

		await waitFor(() => expect(restart).toHaveBeenCalledTimes(1));
	});

	it("shows a restart failure inline", async () => {
		vi.spyOn(aoBridge.daemon, "restart").mockRejectedValue(new Error("Daemon did not stop"));
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);

		fireEvent.click(screen.getByRole("button", { name: "Restart daemon" }));

		expect(await screen.findByText("Daemon did not stop")).toBeInTheDocument();
	});
});

describe("Report a problem telemetry", () => {
	beforeEach(() => vi.mocked(captureRendererEvent).mockClear());
	afterEach(() => vi.restoreAllMocks());

	it.each([true, false])("reports the terminal launch outcome when ok is %s", async (ok) => {
		const launch = vi.spyOn(aoBridge.triage, "launch").mockResolvedValue(ok ? { ok: true } : { ok: false, error: "report.terminalFailed" });
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);
		fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(launch).toHaveBeenCalledWith();
		await waitFor(() => expect(captureRendererEvent).toHaveBeenCalledExactlyOnceWith("ao.renderer.support_submitted", { destination: "terminal", outcome: ok ? "succeeded" : "failed" }));
		if (!ok) expect(screen.getByText("Could not open a terminal. Try again.")).toBeInTheDocument();
	});

	it("reports a failed terminal launch when IPC rejects", async () => {
		vi.spyOn(aoBridge.triage, "launch").mockRejectedValue(new Error("IPC unavailable"));
		render(<DaemonFailureBanner status={{ state: "error", code: "not_ready" }} />);
		fireEvent.click(screen.getByRole("button", { name: "Report a problem" }));
		expect(await screen.findByText("Could not start the report. Try again.")).toBeInTheDocument();
		expect(captureRendererEvent).toHaveBeenCalledExactlyOnceWith("ao.renderer.support_submitted", { destination: "terminal", outcome: "failed" });
	});
});
