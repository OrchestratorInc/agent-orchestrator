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
	it("uses the same filled treatment for both cloud header actions", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} cloud placement="header" />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--action", "bg-raised", "border-0");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--action", "bg-raised", "border-0");
	});

	it("keeps the same filled header actions on an empty local board", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} placement="header" quiet />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--action", "bg-raised", "border-0");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--action", "bg-raised", "border-0");
	});
});
