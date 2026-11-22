import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";

// The chat's changes pill through an app switch: it kept its totals only as
// long as `config` lived, so it left on every switch and came back on return.
const fixture = vi.hoisted(() => ({ asked: [] as string[] }));
vi.mock("expo-router", () => ({ useFocusEffect: () => {} }));
vi.mock("./backend", () => ({
	daemonFilesBackend: (config: ServerConfig) => ({
		key: config.host, cloud: false,
		manifest: async () => {
			fixture.asked.push(config.host);
			return { summary: { files: 5, additions: 45, deletions: 15 } };
		},
	}),
}));
import { useChangeSummary, type ChangeSummary } from "./useChangeSummary";

const lan = { host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "secret", hostId: "h1" } as ServerConfig;
let renderer: ReactTestRenderer | undefined;
let summary: ChangeSummary | null = null;
function Harness({ config, machine }: { config: ServerConfig | null; machine: string }) {
	summary = useChangeSummary(config, machine, "s1", "signal");
	return null;
}
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixture.asked = []; summary = null; });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("keeps the totals while the connection is away, and asks nothing until it is verified again", async () => {
	await act(async () => { renderer = create(<Harness config={lan} machine="host.h1" />); });
	expect(summary).toEqual({ files: 5, additions: 45, deletions: 15 });
	await act(async () => { renderer!.update(<Harness config={null} machine="host.h1" />); });
	expect(summary).toEqual({ files: 5, additions: 45, deletions: 15 });
	expect(fixture.asked).toEqual(["192.168.1.20"]);
});

it("starts over for another machine", async () => {
	await act(async () => { renderer = create(<Harness config={lan} machine="host.h1" />); });
	await act(async () => { renderer!.update(<Harness config={null} machine="host.h2" />); });
	expect(summary).toBeNull();
});
