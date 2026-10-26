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
		expect(isDraftDirty(initial, { ...initial, config: { ...initial.config, secure: false } })).toBe(true);
	});

	it("saves only with a name, a host and a real port", () => {
		const draft = machineEditDraft(host, onTailscale);
		expect(canSaveDraft(draft)).toBe(true);
		expect(canSaveDraft({ ...draft, machineName: "  " })).toBe(false);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, host: "" } })).toBe(false);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, httpPort: "70000" } })).toBe(false);
		expect(canSaveDraft({ ...draft, config: { ...draft.config, httpPort: "" } })).toBe(false);
	});
});
