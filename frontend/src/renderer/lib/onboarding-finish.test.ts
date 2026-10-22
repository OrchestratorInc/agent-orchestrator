import { beforeEach, describe, expect, it, vi } from "vitest";
import { hasCompletedOnboarding, markOnboardingComplete, runOnboardingFinish } from "./onboarding-finish";
import { readTaskComposerPreferences } from "./task-composer-preferences";
import type { OnboardingFinishRequest } from "../stores/ui-store";

const request: OnboardingFinishRequest = {
	nonce: 1,
	orchestratorAgent: "claude-code",
	path: "/tmp/acme/project",
	workerAgent: "codex",
};

beforeEach(() => {
	window.localStorage.clear();
});

describe("runOnboardingFinish", () => {
	it("marks onboarding complete only after the project is registered", async () => {
		const createProject = vi.fn().mockResolvedValue(undefined);
		const outcome = await runOnboardingFinish(request, {
			createProject,
			createStandaloneSession: vi.fn(),
		});

		expect(outcome).toEqual({ ok: true });
		expect(createProject).toHaveBeenCalledWith({
			asWorkspace: undefined,
			clonePreparationId: undefined,
			orchestratorAgent: "claude-code",
			path: "/tmp/acme/project",
			workerAgent: "codex",
		});
		expect(hasCompletedOnboarding()).toBe(true);
	});

	it("starts a standalone agent right away without registering a project", async () => {
		const createProject = vi.fn();
		const createStandaloneSession = vi.fn().mockResolvedValue("session-1");
		const outcome = await runOnboardingFinish({ nonce: 2, orchestratorAgent: "codex", path: "", standalone: true, workerAgent: "codex" }, { createProject, createStandaloneSession });

		expect(outcome).toEqual({ ok: true, sessionId: "session-1" });
		expect(createStandaloneSession).toHaveBeenCalledWith({ displayName: "Codex", harness: "codex" });
		expect(createProject).not.toHaveBeenCalled();
		expect(hasCompletedOnboarding()).toBe(true);
		expect(readTaskComposerPreferences("__standalone__")?.lastAgent).toBe("codex");
	});

	it("keeps onboarding open when the standalone agent fails to start", async () => {
		const outcome = await runOnboardingFinish(
			{ nonce: 3, orchestratorAgent: "codex", path: "", standalone: true, workerAgent: "codex" },
			{ createProject: vi.fn(), createStandaloneSession: vi.fn().mockRejectedValue(new Error("codex is not signed in")) },
		);

		expect(outcome).toEqual({ message: "codex is not signed in", ok: false });
		expect(hasCompletedOnboarding()).toBe(false);
	});

	it("leaves onboarding incomplete and reports the reason when the project fails", async () => {
		const outcome = await runOnboardingFinish(request, {
			createProject: vi.fn().mockRejectedValue(new Error("clone failed: repository not found")),
			createStandaloneSession: vi.fn(),
		});

		expect(outcome).toEqual({ message: "clone failed: repository not found", ok: false });
		expect(hasCompletedOnboarding()).toBe(false);
	});

	it("survives storage being unavailable", () => {
		const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
			throw new Error("quota exceeded");
		});
		expect(() => markOnboardingComplete()).not.toThrow();
		setItem.mockRestore();
	});
});
