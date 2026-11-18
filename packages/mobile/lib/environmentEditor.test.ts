import { describe, expect, it } from "vitest";
import type { ServerConfig } from "./config";
import { canSaveDraft, editableEndpointIndex, isDraftDirty, machineEditDraft } from "./environmentEditor";
import type { Host } from "./hosts";

const host: Host = {
	id: "host-1",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 1,
	appearance: { symbol: "laptop" },
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
	],
};
const onTailscale: ServerConfig = { host: "studio.tail123.ts.net", httpPort: "3011", muxPort: "3012", secure: true, password: "secret", hostId: "host-1", endpointKind: "tailscale" };

describe("machine editor draft", () => {
	it("lets a machine be renamed although its saved address predates the Host checks", () => {
		const legacy: Host = { ...host, endpoints: [{ kind: "lan", host: "box/ao", port: 3011, secure: false }] };
		const renamed = { ...machineEditDraft(legacy), machineName: "Box" };
		expect(canSaveDraft(renamed, legacy.endpoints[0])).toBe(true);
		expect(canSaveDraft({ ...renamed, config: { ...renamed.config, host: "box/other" } }, legacy.endpoints[0])).toBe(false);
	});

	it("edits the route the phone is connected over, else the first", () => {
		expect(editableEndpointIndex(host, onTailscale)).toBe(1);
		expect(editableEndpointIndex(host)).toBe(0);
		expect(editableEndpointIndex(host, { ...onTailscale, hostId: "other" })).toBe(0);
		expect(machineEditDraft(host, onTailscale)).toEqual({
			machineName: "Studio Mac",
			appearance: { symbol: "laptop" },
			endpointIndex: 1,
			config: { host: "studio.tail123.ts.net", httpPort: "3011", muxPort: "14801", secure: true, password: "secret", hostId: "host-1", endpointKind: "tailscale" },
		});
	});

	it("is dirty only when something the editor shows has changed", () => {
		const initial = machineEditDraft(host, onTailscale);
		expect(isDraftDirty(initial, { ...initial })).toBe(false);
		expect(isDraftDirty(initial, { ...initial, machineName: "Travel Mac" })).toBe(true);
		expect(isDraftDirty(initial, { ...initial, appearance: { symbol: "cloud" } })).toBe(true);
		expect(isDraftDirty(initial, { ...initial, config: { ...initial.config, password: "new" } })).toBe(true);
	});

	it("saves only with a name, a host and a real port", () => {
		const draft = machineEditDraft(host, onTailscale);
		expect(canSaveDraft(draft)).toBe(true);
		expect(canSaveDraft({ ...draft, machineName: "  " })).toBe(false);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, host: "" } })).toBe(false);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, httpPort: "70000" } })).toBe(false);
		// An empty port follows the address (443 for TLS, 3011 otherwise), whatever
		// the route was saved with: TLS is no longer the draft's to decide.
		expect(canSaveDraft({ ...draft, config: { ...draft.config, httpPort: "" } })).toBe(true);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, secure: false, httpPort: "" } })).toBe(true);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, host: "192.168.1.24:3011", httpPort: "" } })).toBe(true);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, host: "studio.tail123.ts.net:443" } })).toBe(false);
	});
});
