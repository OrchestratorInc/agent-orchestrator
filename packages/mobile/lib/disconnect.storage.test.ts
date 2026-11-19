import { beforeEach, expect, it, vi } from "vitest";

const plain = new Map<string, string>();
const secure = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => plain.get(key) ?? null,
	setItem: vi.fn(async (key: string, value: string) => void plain.set(key, value)),
	removeItem: async (key: string) => void plain.delete(key),
} }));
vi.mock("expo-secure-store", () => ({
	getItemAsync: async (key: string) => secure.get(key) ?? null,
	setItemAsync: async (key: string, value: string) => void secure.set(key, value),
	deleteItemAsync: async (key: string) => void secure.delete(key),
}));
vi.mock("./account", () => ({ loadAccount: async () => null }));
vi.mock("./push", () => ({ unpairFromServer: async () => {}, retryPendingUnpairs: async () => {} }));
vi.mock("./pendingUnpair", () => ({ markMachineUnpaired: async () => {} }));
vi.mock("./chat/eventCursor", () => ({ clearEventCursorsForHost: async () => {} }));

import { forgetPairedHost, forgetServer } from "./disconnect";
import { rememberedTurnOptions, rememberTurnOptions } from "./chat/lastTurnOptions";
import { loadConfig, saveConfig } from "./config";
import { activeHost, loadHosts, migrateLegacyConfig, persistResolvedConfig, saveHost } from "./hosts";
import { resolveActiveConfig } from "./resolveConfig";

const host = { id: "h_one", name: "One", platform: "darwin", endpoints: [{ kind: "lan" as const, host: "host-one", port: 3011, secure: false }], token: "secret", lastConnected: 1, accountUserId: "user-a" };
beforeEach(() => { plain.clear(); secure.clear(); });

it("does not resurrect the last account host from cached connection data after sign-out", async () => {
	await saveHost(host);
	await saveConfig({ hostId: host.id, host: "host-one", httpPort: "3011", muxPort: "3011", password: host.token });
	await forgetPairedHost(host, { ignoreAccount: false });
	await migrateLegacyConfig();
	expect(await loadHosts()).toEqual([]);
	expect((await loadConfig()).password).toBe("");
});

it("keeps another machine's cached connection when removing a stale account host", async () => {
	await saveHost(host);
	await saveHost({ ...host, id: "h_other", token: "other", accountUserId: undefined });
	await saveConfig({ hostId: "h_other", host: "other-host", httpPort: "3011", muxPort: "3011", password: "other" });
	await forgetPairedHost(host, { ignoreAccount: false });
	expect((await loadConfig()).password).toBe("other");
	expect((await loadHosts()).map((item) => item.id)).toEqual(["h_other"]);
});

it("does not restore a forgotten machine when its pending connection finishes late", async () => {
	await saveHost(host);
	let finish!: (result: import("./connect").ConnectResult) => void;
	const connecting = vi.fn(() => new Promise<import("./connect").ConnectResult>((resolve) => { finish = resolve; }));
	const { runtimeResolveDeps } = await import("./resolveConfig");
	const pending = resolveActiveConfig({ ...runtimeResolveDeps(), migrate: migrateLegacyConfig, activeHost, connect: connecting });
	await vi.waitFor(() => expect(connecting).toHaveBeenCalledOnce());
	await forgetPairedHost(host, { ignoreAccount: false });
	finish({ ok: true, hostId: host.id, endpoint: host.endpoints[0], config: { hostId: host.id, host: "host-one", httpPort: "3011", muxPort: "3011", password: host.token } });
	expect(await pending).toBeNull();
	await migrateLegacyConfig();
	expect(await loadHosts()).toEqual([]);
	expect((await loadConfig()).password).toBe("");
});

it("clears an already-writing connection before Forget finishes", async () => {
	await saveHost(host);
	const storage = (await import("@react-native-async-storage/async-storage")).default;
	let release!: () => void;
	vi.mocked(storage.setItem).mockImplementationOnce(async (key, value) => {
		await new Promise<void>((resolve) => { release = resolve; });
		plain.set(key, value);
	});
	const persistence = persistResolvedConfig({ hostId: host.id, host: "host-one", httpPort: "3011", muxPort: "3011", password: host.token });
	await vi.waitFor(() => expect(release).toBeTypeOf("function"));
	const removal = forgetPairedHost(host, { ignoreAccount: false });
	release();
	await Promise.all([persistence, removal]);
	await migrateLegacyConfig();
	expect(await loadHosts()).toEqual([]);
	expect((await loadConfig()).password).toBe("");
});

it("drops remembered turn-setting catalogs when a machine is forgotten", async () => {
	const catalog = { models: [], configOptions: [{ id: "model", name: "Model", type: "select" as const, currentValue: "opus", choices: [{ value: "opus", name: "Opus 5.5" }] }] };
	await saveHost(host);
	rememberTurnOptions("chat-on-one", catalog);
	await forgetPairedHost(host, { ignoreAccount: false });
	expect(rememberedTurnOptions("chat-on-one")).toBeUndefined();

	await saveHost(host);
	rememberTurnOptions("chat-on-one", catalog);
	await forgetServer(host);
	expect(rememberedTurnOptions("chat-on-one")).toBeUndefined();
});
