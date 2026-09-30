import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { TooltipProvider } from "./ui/tooltip";

const actions: ProjectOrchestratorAction = {
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
	it("keeps cloud actions plain until hover", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} cloud placement="header" />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--secondary");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--secondary", "hover:bg-interactive-hover", "hover:text-foreground");
		expect(screen.getByRole("button", { name: /orchestrator/i })).not.toHaveClass("bg-interactive-hover", "text-foreground");
	});

	it("preserves the existing empty local board controls", () => {
		render(
			<TooltipProvider>
				<ProjectBoardActions actions={actions} placement="header" quiet />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "New task" })).toHaveClass("topbar-control--secondary");
		expect(screen.getByRole("button", { name: /orchestrator/i })).toHaveClass("topbar-control--secondary");
	});
});
