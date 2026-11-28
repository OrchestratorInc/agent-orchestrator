import { resolveHref } from "expo-router/build/link/href";
import { describe, expect, it } from "vitest";
import { hostIdFromMachineRoute, machineRouteId, UNIDENTIFIED_MACHINE_ROUTE_ID } from "./machineRoute";

describe("a machine page's address", () => {
	it("is the machine's id, and reads back as it", () => {
		expect(machineRouteId("h_2f6c")).toBe("h_2f6c");
		expect(hostIdFromMachineRoute("h_2f6c")).toBe("h_2f6c");
	});

	// A machine carried over from the single-server config, or connected by hand
	// when its identity probe failed, has no id until it first connects.
	it("stands in for a machine with no id yet, and reads back as no id", () => {
		expect(machineRouteId("")).toBe(UNIDENTIFIED_MACHINE_ROUTE_ID);
		expect(hostIdFromMachineRoute(UNIDENTIFIED_MACHINE_ROUTE_ID)).toBe("");
	});

	// An empty segment resolves to /settings/machine/, which has no page: the
	// app showed expo-router's "Unmatched Route" instead of the machine.
	it("always resolves to the machine's page", () => {
		expect(resolveHref({ pathname: "/settings/machine/[id]", params: { id: machineRouteId("") } }))
			.toBe(`/settings/machine/${UNIDENTIFIED_MACHINE_ROUTE_ID}`);
	});

	// Daemons issue "h_" + a UUID, so the stand-in can never be a real id.
	it("can't be mistaken for a real machine's id", () => {
		expect(UNIDENTIFIED_MACHINE_ROUTE_ID.startsWith("h_")).toBe(false);
	});

	it("matches no machine when the page has no id at all", () => {
		expect(hostIdFromMachineRoute(undefined)).toBeNull();
		expect(hostIdFromMachineRoute("")).toBeNull();
	});
});
