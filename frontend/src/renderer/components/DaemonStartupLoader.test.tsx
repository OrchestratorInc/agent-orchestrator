import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { aoBridge } from "../lib/bridge";

vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({
		query: { isSuccess: true, refetch: vi.fn() },
		requirements: [
			{ id: "git", label: "git", satisfied: true, required: true, detail: "/usr/bin/git" },
			{ id: "tmux", label: "tmux", satisfied: true, required: true, detail: "/usr/bin/tmux" },
			{ id: "gh", label: "gh", satisfied: true, required: false, detail: "/usr/bin/gh" },
		],
		blocked: false,
		requirementsBlocked: false,
	}),
}));

describe("DaemonStartupLoader", () => {
	it("shows only the mascot and announces startup to assistive tech", () => {
		render(<DaemonStartupLoader />);

		const status = screen.getByRole("status", { name: "Agent Orchestrator is starting" });
		expect(status.textContent).toBe("");
		expect(status.querySelector(".ao-startup-logo")).not.toBeNull();
	});

	it("offers one-click reporting while the daemon has failed, without a Shell or daemon API", async () => {
		vi.spyOn(aoBridge.daemon, "getStatus").mockResolvedValue({ state: "error", code: "spawn_failed", details: "daemon failed" });
		vi.spyOn(aoBridge.triage, "catalog").mockResolvedValue([]);
		const start = vi.spyOn(aoBridge.triage, "start");
		render(<DaemonStartupLoader />);
		fireEvent.click(await screen.findByRole("button", { name: "Report a problem" }));
		await waitFor(() => expect(screen.getByRole("button", { name: "Start investigation" })).toBeDisabled());
		expect(start).not.toHaveBeenCalled();
		expect(screen.getByRole("dialog", { name: "Report a problem" })).toBeInTheDocument();
		vi.restoreAllMocks();
	});
});
