import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DaemonStartupLoader } from "./DaemonStartupLoader";

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
});
