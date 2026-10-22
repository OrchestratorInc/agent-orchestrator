import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	initialize: vi.fn(async (_path: string) => undefined),
	requestNewTask: vi.fn(),
	completeWelcome: vi.fn(),
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ cloneProject: vi.fn(), createProject: vi.fn(), initializeProjectRepository: mocks.initialize }),
}));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: { requestNewTask: typeof mocks.requestNewTask }) => unknown) => select({ requestNewTask: mocks.requestNewTask }),
}));
vi.mock("../lib/onboarding-telemetry", () => ({ useOnboardingStep: () => mocks.completeWelcome }));
vi.mock("./WelcomePanel", () => ({ WelcomePanel: ({ children }: { children: ReactNode }) => <>{children}</> }));
vi.mock("./GitHubOnboardingNotice", () => ({ GitHubOnboardingNotice: () => null }));
vi.mock("./CreateProjectFlow", () => ({
	CreateProjectFlow: ({ onInitializeProject, onCreateStandaloneAgent }: {
		onInitializeProject: (path: string) => Promise<void>;
		onCreateStandaloneAgent: () => void;
	}) => <>
		<button onClick={() => { void onInitializeProject("/tmp/project").catch(() => undefined); }}>Initialize project</button>
		<button onClick={onCreateStandaloneAgent}>Standalone agent</button>
	</>,
}));

import { BoardWelcome } from "./BoardEmptyStates";

describe("BoardWelcome onboarding completion", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.initialize.mockResolvedValue(undefined);
	});

	it("completes welcome after initializing a project", async () => {
		render(<BoardWelcome />);
		fireEvent.click(screen.getByRole("button", { name: "Initialize project" }));
		await waitFor(() => expect(mocks.completeWelcome).toHaveBeenCalledOnce());
	});

	it("does not complete welcome when project initialization fails", async () => {
		mocks.initialize.mockRejectedValueOnce(new Error("failed"));
		render(<BoardWelcome />);
		fireEvent.click(screen.getByRole("button", { name: "Initialize project" }));
		await waitFor(() => expect(mocks.initialize).toHaveBeenCalledOnce());
		expect(mocks.completeWelcome).not.toHaveBeenCalled();
	});

	it("completes welcome when opening a standalone agent", () => {
		render(<BoardWelcome />);
		fireEvent.click(screen.getByRole("button", { name: "Standalone agent" }));
		expect(mocks.completeWelcome).toHaveBeenCalledOnce();
		expect(mocks.requestNewTask).toHaveBeenCalledWith("__standalone__");
	});
});
