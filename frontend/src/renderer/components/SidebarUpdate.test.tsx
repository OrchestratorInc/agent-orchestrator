import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { UpdateStatus } from "../../main/update-settings";
import { SidebarUpdateRow, sidebarUpdateAction, updateFailurePhase } from "./SidebarUpdate";
import { TooltipProvider } from "./ui/tooltip";

function renderRow(status: UpdateStatus) {
	const row = (next: UpdateStatus) => (
		<TooltipProvider>
			<SidebarUpdateRow
				onRequestInstall={vi.fn()}
				status={next}
				tabIndex={0}
			/>
		</TooltipProvider>
	);
	const view = render(row(status));
	return { rerender: (next: UpdateStatus) => view.rerender(row(next)) };
}

describe("sidebarUpdateAction", () => {
	it("prefers an in-flight download over everything else", () => {
		expect(sidebarUpdateAction({ state: "downloading", percent: 140 })).toEqual({ kind: "downloading", percent: 100 });
		expect(sidebarUpdateAction({ state: "downloading" })).toEqual({ kind: "downloading", percent: undefined });
	});

	it("shows a failed download or install ahead of a staged build", () => {
		const status: UpdateStatus = { state: "error", staged: { version: "1.0.0", stagedAt: 1, escalated: false } };
		expect(sidebarUpdateAction(status, "install")).toEqual({ kind: "failed", phase: "install" });
		expect(sidebarUpdateAction(status, null)).toEqual({ kind: "install", version: "1.0.0" });
	});

	it("offers a newer build over a staged one", () => {
		const status: UpdateStatus = {
			state: "available",
			version: "1.1.0",
			releaseNotes: "notes",
			staged: { version: "1.0.0", stagedAt: 1, escalated: false },
		};
		expect(sidebarUpdateAction(status)).toEqual({ kind: "download", version: "1.1.0", releaseNotes: "notes" });
		expect(sidebarUpdateAction({ ...status, version: "1.0.0" })).toEqual({ kind: "install", version: "1.0.0" });
	});

	it("ignores failing checks", () => {
		expect(sidebarUpdateAction({ state: "idle", checksFailing: true })).toBeNull();
		expect(sidebarUpdateAction({ state: "error", checksFailing: true })).toBeNull();
	});
});

describe("updateFailurePhase", () => {
	it("trusts the phase the main process attached", () => {
		expect(updateFailurePhase({ state: "error", errorInfo: { kind: "generic", phase: "download" } }, null)).toBe("download");
		expect(updateFailurePhase({ state: "error", errorInfo: { kind: "generic", phase: "install" } }, null)).toBe("install");
		expect(updateFailurePhase({ state: "error", errorInfo: { kind: "timeout", phase: "check" } }, "download")).toBeNull();
		expect(updateFailurePhase({ state: "error", errorInfo: { kind: "generic", phase: "return" } }, "download")).toBeNull();
	});

	it("attributes an unlabelled error to the operation that was in flight", () => {
		const stalled: UpdateStatus = { state: "error", message: "Download stopped responding. Try again." };
		expect(updateFailurePhase(stalled, "download")).toBe("download");
		expect(updateFailurePhase(stalled, "install")).toBe("install");
		// With nothing in flight it is a failed check, which stays out of the sidebar.
		expect(updateFailurePhase(stalled, null)).toBeNull();
	});

	it("is null for anything but an error", () => {
		expect(updateFailurePhase({ state: "downloading" }, "download")).toBeNull();
	});
});

describe("SidebarUpdateRow", () => {
	it("turns an unlabelled error into a failed row only after a download it saw", () => {
		const view = renderRow({ state: "downloading", percent: 30 });
		expect(screen.getByTestId("sidebar-update-downloading")).toBeVisible();

		view.rerender({ state: "error", message: "Download stopped responding. Try again." });
		expect(screen.getByTestId("sidebar-update-failed")).toHaveTextContent("Update failed");
	});

	it("stays empty for the same error with nothing in flight", () => {
		const view = renderRow({ state: "idle" });
		view.rerender({ state: "error", message: "Download stopped responding. Try again." });
		expect(screen.queryByTestId("sidebar-update-failed")).not.toBeInTheDocument();
	});
});
