import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OrchestratorProjectRow } from "./orchestratorView";

const state = vi.hoisted(() => ({
	environment: "cloud" as "cloud" | "local",
	orgId: "org-1" as string | null,
	accountId: "user-1", sessionEpoch: 0, client: {},
	refresh: vi.fn(async () => {}),
	launchConductor: vi.fn(async () => ({ id: "local-session" })),
	launchConductorOn: vi.fn(async () => ({ id: "local-session" })),
	refreshSource: vi.fn(async () => {}),
	sourceFor: vi.fn(() => ({ kind: "local" })),
	spawnCloudOrchestrator: vi.fn(async () => "cloud-session"),
	push: vi.fn(),
	alert: vi.fn(),
}));

vi.mock("react-native", () => ({ Alert: { alert: state.alert }, Platform: { OS: "ios" } }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: state.push }) }));
vi.mock("expo-crypto", () => ({ randomUUID: () => "request-1" }));
vi.mock("./api", () => ({ ApiError: class ApiError extends Error {} }));
vi.mock("./config", () => ({ isConfigured: () => true, machineIdentity: () => "mac-1" }));
vi.mock("./cloud/authStore", () => ({ useCloudAuth: () => { const epoch = state.sessionEpoch; return { client: state.client, orgId: state.orgId, accountId: state.accountId, sessionEpoch: epoch, isCurrentSession: () => state.sessionEpoch === epoch }; } }));
vi.mock("./cloud/orchestrator", () => ({ spawnCloudOrchestrator: state.spawnCloudOrchestrator }));
vi.mock("./store", () => ({ useApp: () => ({ environment: state.environment, config: null, refresh: state.refresh, launchConductor: state.launchConductor,
	launchConductorOn: state.launchConductorOn, refreshSource: state.refreshSource, sourceFor: state.sourceFor }) }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn(), error: vi.fn() } }));

import { useOrchestratorLauncher } from "./useOrchestratorLauncher";

const row = (action: OrchestratorProjectRow["action"], id?: string): OrchestratorProjectRow => ({
	project: { id: "project-1", name: "Tappy", kind: "single_repo" },
	link: id ? { id } as OrchestratorProjectRow["link"] : null,
	action,
} as OrchestratorProjectRow);

let renderer: ReactTestRenderer | undefined;
let launcher: ReturnType<typeof useOrchestratorLauncher>;

function Probe() {
	launcher = useOrchestratorLauncher();
	return null;
}

beforeEach(async () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	state.environment = "cloud";
	state.orgId = "org-1";
	state.accountId = "user-1"; state.sessionEpoch = 0;
	vi.clearAllMocks();
	await act(async () => { renderer = create(<Probe />); });
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("project orchestrator button", () => {
	it("does not navigate or refresh the previous account after a delayed Cloud launch", async () => {
		let resolve!: (id: string) => void;
		state.spawnCloudOrchestrator.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		await act(async () => launcher.openOrchestrator(row("start")));
		state.accountId = "user-2"; state.sessionEpoch++;
		await act(async () => renderer!.update(<Probe />));
		await act(async () => resolve("old-session"));
		expect(state.push).not.toHaveBeenCalled(); expect(state.refreshSource).not.toHaveBeenCalled();
	});
	it("does not navigate after the project screen unmounts during launch", async () => {
		let resolve!: (id: string) => void;
		state.spawnCloudOrchestrator.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
		await act(async () => launcher.openOrchestrator(row("start")));
		await act(async () => renderer!.unmount());
		await act(async () => resolve("old-session")); expect(state.push).not.toHaveBeenCalled();
	});
	it("starts a Local orchestrator by its source even when the legacy view says Cloud", async () => {
		await act(async () => launcher.openOrchestrator({ source: { kind: "local", id: "mac-1" }, value: row("start") }));
		expect(state.launchConductorOn).toHaveBeenCalledWith({ kind: "local", id: "mac-1" }, "project-1", false, "chat");
		expect(state.spawnCloudOrchestrator).not.toHaveBeenCalled();
		expect(state.push).toHaveBeenCalledWith({ pathname: "/session/[id]", params: {
			id: "local-session", projectId: "project-1", source: "local", sourceId: "mac-1",
		} });
	});
	it("opens an existing Cloud orchestrator without creating a new session", async () => {
		await act(async () => launcher.openOrchestrator(row("open", "existing")));
		expect(state.spawnCloudOrchestrator).not.toHaveBeenCalled();
		expect(state.push).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "existing", projectId: "project-1", source: "cloud", sourceId: "org-1" } });
	});

	it("creates a missing Cloud orchestrator and opens the returned session", async () => {
		await act(async () => launcher.openOrchestrator(row("start")));
		expect(state.spawnCloudOrchestrator).toHaveBeenCalledWith({}, "org-1", "project-1", "request-1");
		expect(state.refreshSource).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
		expect(state.push).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "cloud-session", projectId: "project-1", source: "cloud", sourceId: "org-1", startup: "spawn" } });
		expect(state.launchConductor).not.toHaveBeenCalled();
	});
});
