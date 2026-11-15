import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InstallJob } from "../../../hooks/useHarnessUpdates";
import { installPercent, installStage } from "./describe-harness";
import { HarnessInstallProgress, INSTALL_DONE_VISIBLE_MS, useInstallJustFinished } from "./HarnessInstallProgress";

const startedAt = "2026-10-29T10:00:00Z";

function job(overrides: Partial<InstallJob>): InstallJob {
	return { target: "codex", status: "installing", method: "npm", startedAt, ...overrides } as InstallJob;
}

function DoneProbe({ value }: { value: InstallJob }) {
	return <span>{useInstallJustFinished(value) ? "done" : "not done"}</span>;
}

afterEach(() => {
	vi.useRealTimers();
});

describe("install stage", () => {
	it("follows the installer's phase, then verification", () => {
		expect(installStage(job({}))).toBeUndefined();
		// Output that names no phase (npm's warnings, a silent script's banner) marks no stage.
		expect(installStage(job({ progress: { detail: "npm warn deprecated glob@7.2.3" } }))).toBeUndefined();
		expect(installStage(job({ progress: { phase: "downloading" } }))).toBe("downloading");
		expect(installStage(job({ progress: { phase: "installing" } }))).toBe("installing");
		expect(installStage(job({ status: "verifying", progress: { phase: "installing", percent: 80 } }))).toBe("verifying");
		expect(installStage(job({ status: "succeeded" }))).toBeUndefined();
		expect(installStage(job({ status: "waiting_for_idle" }))).toBeUndefined();
	});

	it("reports a percentage only while the installer stated one", () => {
		expect(installPercent(job({ progress: { phase: "downloading", percent: 45.6 } }))).toBe(46);
		// A script printing only a curl-style bar names no phase, but its percentage still counts.
		expect(installPercent(job({ progress: { percent: 45 } }))).toBe(45);
		expect(installPercent(job({ progress: { phase: "downloading" } }))).toBeUndefined();
		expect(installPercent(job({ status: "verifying", progress: { phase: "installing", percent: 80 } }))).toBeUndefined();
	});
});

describe("HarnessInstallProgress", () => {
	it("fills the bar to the downloaded percentage and shows the installer's line", () => {
		render(<HarnessInstallProgress job={job({ progress: { phase: "downloading", percent: 45, detail: "Downloading mistral-vibe (36.2MiB)" } })} stage="downloading" label="Downloading 45%" />);
		const bar = screen.getByRole("progressbar", { name: "Downloading 45%" });
		expect(bar).toHaveAttribute("aria-valuenow", "45");
		expect(bar.firstElementChild).toHaveStyle({ width: "45%" });
		expect(screen.getByText("Downloading mistral-vibe (36.2MiB)")).toBeInTheDocument();
		expect(screen.queryByRole("list")).not.toBeInTheDocument();
	});

	it("sweeps the bar when the installer states no percentage", () => {
		render(<HarnessInstallProgress job={job({ progress: { phase: "installing", detail: "added 2 packages in 3s" } })} stage="installing" label="Installing…" />);
		expect(screen.getByRole("progressbar", { name: "Installing…" })).not.toHaveAttribute("aria-valuenow");
		expect(screen.getByText("added 2 packages in 3s")).toBeInTheDocument();
	});

	it("shows only the sweep before the installer reports progress", () => {
		render(<HarnessInstallProgress job={job({})} label="Installing…" />);
		expect(screen.getByRole("progressbar", { name: "Installing…" })).not.toHaveAttribute("aria-valuenow");
		expect(screen.queryByRole("paragraph")).not.toBeInTheDocument();
	});

	it("explains verification and ends on a full bar", () => {
		const { rerender } = render(<HarnessInstallProgress job={job({ status: "verifying" })} stage="verifying" label="Verifying…" />);
		expect(screen.getByText("Checking the installed version")).toBeInTheDocument();
		rerender(<HarnessInstallProgress job={job({ status: "succeeded" })} stage="done" label="Done" />);
		expect(screen.getByRole("progressbar", { name: "Done" })).toHaveAttribute("aria-valuenow", "100");
		expect(screen.getByText("Done")).toBeInTheDocument();
	});
});

describe("useInstallJustFinished", () => {
	it("shows Done briefly after a watched job succeeds", () => {
		vi.useFakeTimers();
		const { rerender } = render(<DoneProbe value={job({ status: "verifying" })} />);
		expect(screen.getByText("not done")).toBeInTheDocument();
		rerender(<DoneProbe value={job({ status: "succeeded" })} />);
		expect(screen.getByText("done")).toBeInTheDocument();
		// A later poll of the same finished job keeps the timer.
		rerender(<DoneProbe value={job({ status: "succeeded", output: "codex 1.2.3" })} />);
		act(() => vi.advanceTimersByTime(INSTALL_DONE_VISIBLE_MS));
		expect(screen.getByText("not done")).toBeInTheDocument();
	});

	it("does not replay Done for a job that had finished before the page opened", () => {
		render(<DoneProbe value={job({ status: "succeeded" })} />);
		expect(screen.getByText("not done")).toBeInTheDocument();
	});

	it("does not show Done when the job fails", () => {
		const { rerender } = render(<DoneProbe value={job({ status: "verifying" })} />);
		rerender(<DoneProbe value={job({ status: "failed" })} />);
		expect(screen.getByText("not done")).toBeInTheDocument();
	});
});
