import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { CloudCpError } from "../lib/cloud-cp";
import { invalidateTeamsAndShares, retryCloudRead, teamsUnavailable } from "./useTeams";

describe("invalidateTeamsAndShares", () => {
	it("also refreshes the projects shared with the person, so leaving a team drops them from the sidebar", async () => {
		const queryClient = new QueryClient();
		const key = ["cloud-shared-workspaces", "https://cp.test"];
		queryClient.setQueryData(key, []);
		invalidateTeamsAndShares(queryClient);
		expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true);
	});
});

describe("retryCloudRead", () => {
	it("retries a failed read once, and never one the control plane refused", () => {
		const network = new CloudCpError("offline", { status: 0 });
		expect(retryCloudRead(0, network)).toBe(true);
		expect(retryCloudRead(1, network)).toBe(false);
		expect(retryCloudRead(0, new CloudCpError("boom", { status: 502 }))).toBe(true);
		expect(retryCloudRead(0, new CloudCpError("missing", { status: 404 }))).toBe(false);
		expect(retryCloudRead(0, new CloudCpError("denied", { status: 403 }))).toBe(false);
	});

	it("reads a 404 as a control plane from before teams", () => {
		expect(teamsUnavailable(new CloudCpError("missing", { status: 404 }))).toBe(true);
		// A coded 404 is about what was asked for (an archived project, a
		// session not in the project), not about teams.
		expect(teamsUnavailable(new CloudCpError("The requested resource was not found.", { status: 404, code: "not_found" }))).toBe(false);
		expect(teamsUnavailable(new CloudCpError("boom", { status: 500 }))).toBe(false);
		expect(teamsUnavailable(new Error("other"))).toBe(false);
	});
});
