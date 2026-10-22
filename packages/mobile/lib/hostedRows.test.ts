import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { hostedProjectKey, hostedProjectSections, hostedSessionKey, sessionMachineIdentity, type HostedSession } from "./hostedRows";

describe("hosted rows", () => {
	it("keeps identical project and session IDs from two machines separate", () => {
		const session = (id: string) => ({ id, projectId: "same", status: "working", mode: "chat" }) as DashboardSession;
		const sections = hostedProjectSections([
			{ hostId: "a", name: "Laptop", appearance: { symbol: "laptop" }, projects: [{ id: "same", name: "App" }], sessions: [session("same-session")], orchestrators: [] },
			{ hostId: "b", name: "Desktop", appearance: { symbol: "desktop" }, connection: "closed", projects: [{ id: "same", name: "App" }], sessions: [], orchestrators: [] },
		]);
		const rows = sections.flatMap((section) => section.data);
		expect(rows.map((row) => hostedProjectKey(row.project))).toEqual(["[\"a\",\"same\"]", "[\"b\",\"same\"]"]);
		expect(rows.map((row) => row.workers.length)).toEqual([1, 0]);
		expect(rows.map((row) => row.project.name)).toEqual(["App", "App"]);
		expect(rows.map((row) => row.project.hostName)).toEqual(["Laptop", "Desktop (offline)"]);
		expect(rows.map((row) => row.project.hostAppearance)).toEqual([
			{ symbol: "laptop" },
			{ symbol: "desktop" },
		]);
		expect(hostedSessionKey({ ...session("same-session"), hostId: "a", hostName: "Laptop" } as HostedSession)).not.toBe(
			hostedSessionKey({ ...session("same-session"), hostId: "b", hostName: "Desktop" } as HostedSession),
		);
	});

	it("keeps machine identity separate from project text for worker rows", () => {
		const hosted = {
			id: "worker",
			projectId: "project",
			status: "working",
			mode: "chat",
			hostId: "host-a",
			hostName: "Studio Mac",
			hostAppearance: { symbol: "server" },
		} as HostedSession;

		expect(sessionMachineIdentity(hosted)).toEqual({
			name: "Studio Mac",
			appearance: { symbol: "server" },
		});
	});
});
