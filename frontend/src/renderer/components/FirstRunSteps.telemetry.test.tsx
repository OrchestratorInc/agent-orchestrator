import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async (_event: string, _props?: unknown) => true));
const auth = vi.hoisted(() => ({ current: { isSuccess: true, isError: false, data: { satisfied: false } as { satisfied: boolean } | undefined, refetch: vi.fn() } }));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: capture }));
vi.mock("../hooks/useSystemRequirementsGate", () => ({
	useSystemRequirementsGate: () => ({ query: { refetch: vi.fn(), isSuccess: true, isError: false }, requirements: [{ id: "gh", satisfied: true }] }),
	useGitHubAuthRequirement: () => auth.current,
	useStartGitHubAuthTerminal: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
	useGitHubAuthTerminal: () => ({ data: null, clear: vi.fn() }),
}));
vi.mock("../hooks/useShellTerminals", () => ({ useCloseShellTerminal: () => ({ mutate: vi.fn() }) }));
vi.mock("./InstallDependencyDialog", () => ({
	InstallAction: () => null,
	useInstallRunner: () => ({ inspect: vi.fn(), running: false, jobFor: () => undefined, inspectionFinished: () => false, target: null, start: vi.fn() }),
}));
vi.mock("./GitHubRepoPicker", () => ({ GitHubRepoPicker: () => null, preloadGitHubRepos: vi.fn() }));
vi.mock("./TerminalPane", () => ({ TerminalPane: () => null }));

import { GitHubSetupPanel } from "./FirstRunSteps";

describe("GitHub onboarding step telemetry", () => {
	beforeEach(() => {
		capture.mockClear();
		window.localStorage.clear();
	});

	it("reports github_auth viewed while sign-in is needed and completed once signed in", () => {
		auth.current = { ...auth.current, data: { satisfied: false } };
		const onReady = vi.fn();
		const { rerender } = render(<GitHubSetupPanel onReady={onReady} />);
		auth.current = { ...auth.current, data: { satisfied: true } };
		rerender(<GitHubSetupPanel onReady={onReady} />);
		expect(capture.mock.calls).toEqual([
			["ao.onboarding.step_viewed", { step: "github_auth" }],
			["ao.onboarding.step_completed", { step: "github_auth" }],
		]);
		expect(onReady).toHaveBeenCalled();
	});

	it("reports nothing when no GitHub setup was needed", () => {
		auth.current = { ...auth.current, data: { satisfied: true } };
		render(<GitHubSetupPanel onReady={vi.fn()} />);
		expect(capture).not.toHaveBeenCalled();
	});
});
