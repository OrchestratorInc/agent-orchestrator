import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { SharedProjectMark } from "./SharedProjectMark";

const mocks = vi.hoisted(() => ({ teams: true }));
vi.mock("../../hooks/useTeamsEnabled", () => ({ useTeamsEnabled: () => mocks.teams }));

const share = { byName: "Asha", wholeProject: { byName: "Asha", viaTeamName: "Platform", source: "team" as const } };
const mark = () => render(<TooltipProvider><SharedProjectMark share={share} /></TooltipProvider>);

describe("SharedProjectMark", () => {
	beforeEach(() => {
		mocks.teams = true;
	});

	it("marks a shared project with who shared it, in a build with Teams", () => {
		const { container } = mark();
		const span = container.querySelector("[data-shared-project-mark]");
		expect(span).not.toBeNull();
		expect(span?.getAttribute("aria-label")).toMatch(/Asha/);
	});

	it("shows nothing in a build without Teams", () => {
		mocks.teams = false;
		const { container } = mark();
		expect(container.querySelector("[data-shared-project-mark]")).toBeNull();
		expect(container.textContent).toBe("");
	});
});
