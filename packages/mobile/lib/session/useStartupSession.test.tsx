import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardSession } from "../api";
import type { ServerConfig } from "../config";

const fixture = vi.hoisted(() => ({ getSession: vi.fn() }));
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove: () => {} }) } }));
vi.mock("expo-router/react-navigation", () => ({ useIsFocused: () => true }));
vi.mock("../api", () => ({ getSession: fixture.getSession }));
vi.mock("../config", () => ({ machineIdentity: (config: ServerConfig) => config.hostId ?? config.host }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), success: vi.fn() } }));

import { useStartupSession } from "./useStartupSession";

const config = { hostId: "host-a", host: "a.test", httpPort: "3011", muxPort: "", password: "password" } satisfies ServerConfig;
const board = { id: "same-id", provisionState: "provisioning", provisionSteps: [], branch: "board-branch" } as unknown as DashboardSession;
let renderer: ReactTestRenderer | undefined;
let visible: DashboardSession;
function Harness({ cfg }: { cfg: ServerConfig | null }) {
	visible = useStartupSession(cfg, board);
	return null;
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	fixture.getSession.mockReset();
	fixture.getSession.mockResolvedValue({ ...board, provisionState: "ready", branch: "host-a-branch" });
});
afterEach(async () => {
	await act(async () => renderer?.unmount());
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

it("does not reuse another local host's startup facts for a same-ID session", async () => {
	await act(async () => { renderer = create(<Harness cfg={config} />); });
	expect(visible).toMatchObject({ provisionState: "ready", branch: "host-a-branch" });
	fixture.getSession.mockImplementation(() => new Promise(() => {}));
	const otherHost = { ...config, hostId: "host-b", host: "b.test" };
	await act(async () => { renderer!.update(<Harness cfg={otherHost} />); });
	expect(visible).toBe(board);
	expect(fixture.getSession).toHaveBeenLastCalledWith(otherHost, board.id);
});

it("drops local startup facts when the same-ID route becomes Cloud without a daemon config", async () => {
	await act(async () => { renderer = create(<Harness cfg={config} />); });
	expect(visible.branch).toBe("host-a-branch");
	await act(async () => { renderer!.update(<Harness cfg={null} />); });
	expect(visible).toBe(board);
	await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
	expect(fixture.getSession).toHaveBeenCalledOnce();
});
