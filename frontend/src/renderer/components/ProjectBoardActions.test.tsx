import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { TooltipProvider } from "./ui/tooltip";

const actions: ProjectOrchestratorAction = {
	projectId: undefined,
	orchestrator: undefined,
	isSpawning: false,
	isProjectRestarting: false,
	isProvisioning: false,
	spawnError: "",
	canCreateAsTui: false,
	openNewTask: vi.fn(),
	openOrchestrator: vi.fn(),
};

describe("ProjectBoardActions", () => {
	it("keeps New task the one filled header action on a cloud board", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} cloud placement="header" />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--primary", "bg-accent-strong");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--action");
		expect(screen.getByRole("button", { name: /orchestrator/i })).not.toHaveClass("bg-accent-strong");
		// Orchestrator leads, matching the session top bar.
		const buttons = screen.getAllByRole("button");
		expect(buttons.indexOf(screen.getByRole("button", { name: /orchestrator/i }))).toBeLessThan(buttons.indexOf(screen.getByRole("button", { name: "New task" })));
	});

	it("keeps New task the one filled header action on an empty local board", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} placement="header" quiet />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--primary", "bg-accent-strong");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--action");
		expect(screen.getByRole("button", { name: /orchestrator/i })).not.toHaveClass("bg-accent-strong");
		// Orchestrator leads, matching the session top bar.
		const buttons = screen.getAllByRole("button");
		expect(buttons.indexOf(screen.getByRole("button", { name: /orchestrator/i }))).toBeLessThan(buttons.indexOf(screen.getByRole("button", { name: "New task" })));
	});
});
