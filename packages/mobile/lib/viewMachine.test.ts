import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));

import type { DashboardSession } from "./api";
import type { ServerConfig } from "./config";
import type { ConnStatus } from "./store";
import { sessionRowsForMachine } from "./projectFilter";
import { sessionRouteView, type SessionLookup } from "./session/sessionRoute";
import { viewMachine } from "./viewMachine";

const lan: ServerConfig = { host: "192.168.1.20", httpPort: "3011", muxPort: "3011", password: "pw", hostId: "h1", endpointKind: "lan" };
const tunnel: ServerConfig = { host: "x.trycloudflare.com", httpPort: "443", muxPort: "443", secure: true, password: "pw", hostId: "h1", endpointKind: "tunnel" };
const chat = { id: "proj-1", projectId: "proj", mode: "chat", isTerminated: false } as DashboardSession;
const pending: SessionLookup = { state: "pending" };

describe("viewMachine", () => {
	it("names the machine from its verified connection", () => {
		expect(viewMachine({ config: lan, selectedHostId: "h1", configResolved: true })).toEqual({ configured: true, machine: "host.h1", paired: true });
	});

	// The resume flicker: backgrounding drops `config`, and everything keyed on
	// it took the chat down with it.
	it("keeps the same machine while the connection is dropped or being raced", () => {
		expect(viewMachine({ config: null, selectedHostId: "h1", configResolved: false })).toEqual({ configured: false, machine: "host.h1", paired: true });
		expect(viewMachine({ config: null, selectedHostId: "h1", configResolved: true })).toEqual({ configured: false, machine: "host.h1", paired: true });
	});

	it("is the same machine on every endpoint it is reached by", () => {
		expect(viewMachine({ config: tunnel, selectedHostId: "h1", configResolved: true }).machine)
			.toBe(viewMachine({ config: lan, selectedHostId: "h1", configResolved: true }).machine);
	});

	it("does not know yet before the first resolution, and knows there is none after it", () => {
		expect(viewMachine({ config: null, selectedHostId: null, configResolved: false })).toEqual({ configured: false, machine: "", paired: null });
		expect(viewMachine({ config: null, selectedHostId: null, configResolved: true })).toEqual({ configured: false, machine: "", paired: false });
	});

	it("names a pairing migrated without a host id by its endpoint", () => {
		const legacy = { ...lan, hostId: undefined };
		expect(viewMachine({ config: legacy, selectedHostId: null, configResolved: true })).toEqual({ configured: true, machine: "http.192.168.1.20.3011", paired: true });
	});
});

// The store's rows are filtered with sessionRowsForMachine against `machine`,
// and the session route decides from them. Together they must keep an open
// chat mounted through every state a paired machine passes through between
// leaving the app and the first poll after coming back.
describe("an open chat across an app switch", () => {
	const rowsMachine = "host.h1"; // the machine the last poll's rows came from
	const route = (state: { config: ServerConfig | null; configResolved: boolean; connection: ConnStatus; loading: boolean; selectedHostId?: string | null }) => {
		const { machine, paired } = viewMachine({ config: state.config, selectedHostId: state.selectedHostId ?? "h1", configResolved: state.configResolved });
		const listed = sessionRowsForMachine([chat], rowsMachine, machine).find((row) => row.id === chat.id);
		return sessionRouteView({ listed, paired, connection: state.connection, loading: state.loading, lookup: pending, routeHostId: "h1", currentHostId: state.selectedHostId ?? "h1" });
	};

	it("stays on the chat screen in every connection state", () => {
		for (const config of [lan, tunnel, null]) {
			for (const configResolved of [true, false]) {
				for (const connection of ["open", "connecting", "closed"] as const) {
					for (const loading of [true, false]) {
						expect(route({ config, configResolved, connection, loading }), JSON.stringify({ config: config?.endpointKind ?? null, configResolved, connection, loading }))
							.toEqual({ kind: "screen", session: chat });
					}
				}
			}
		}
	});

	it("follows the sequence the store goes through on a resume", () => {
		const resume = [
			{ config: lan, configResolved: true, connection: "open", loading: false }, // in use
			{ config: null, configResolved: false, connection: "closed", loading: true }, // backgrounded
			{ config: null, configResolved: false, connection: "closed", loading: true }, // back, racing
			{ config: tunnel, configResolved: true, connection: "connecting", loading: true }, // raced onto another endpoint
			{ config: null, configResolved: true, connection: "closed", loading: false }, // that endpoint rejected, racing again
			{ config: lan, configResolved: true, connection: "connecting", loading: true }, // upgraded back to LAN
			{ config: lan, configResolved: true, connection: "open", loading: false }, // first poll
		] as const;
		for (const state of resume) expect(route(state)).toEqual({ kind: "screen", session: chat });
	});

	// The other half of the contract: a different machine is never shown the
	// previous one's session while it has not answered.
	it("hides the session once another machine is selected", () => {
		expect(route({ config: null, configResolved: false, connection: "closed", loading: true, selectedHostId: "h2" })).not.toEqual({ kind: "screen", session: chat });
	});
});
