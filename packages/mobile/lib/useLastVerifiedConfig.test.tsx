import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";

vi.mock("./config", () => ({ machineIdentity: (config: ServerConfig) => `host.${config.hostId}` }));
import { useLastVerifiedConfigs } from "./useLastVerifiedConfig";

const a = { host: "a.lan", httpPort: "3011", muxPort: "3011", password: "pa", hostId: "a" } as ServerConfig;
const b = { host: "b.lan", httpPort: "3011", muxPort: "3011", password: "pb", hostId: "b" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

// The pull requests list several machines: each card reads its summary with its
// own machine's last verified connection while that one is away.
it("keeps each machine's last verified config through its own app switch", () => {
	let live: Record<string, ServerConfig | null> = { a, b };
	let read: (hostId: string) => ServerConfig | null = () => null;
	function Harness({ tick }: { tick: number }) {
		read = useLastVerifiedConfigs((hostId) => live[hostId] ?? null);
		return tick ? null : null;
	}
	act(() => { renderer = create(<Harness tick={0} />); });
	expect(read("a")).toBe(a);
	expect(read("b")).toBe(b);
	live = { a: null, b };
	act(() => renderer!.update(<Harness tick={1} />));
	expect(read("a")).toBe(a);
	expect(read("b")).toBe(b);
	expect(read("never-connected")).toBeNull();
	const rotated = { ...a, password: "new" };
	live = { a: rotated, b };
	expect(read("a")).toBe(rotated);
});
