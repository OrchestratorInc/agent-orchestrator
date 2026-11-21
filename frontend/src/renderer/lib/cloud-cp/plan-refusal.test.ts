import { describe, expect, it, vi } from "vitest";
import { createCloudCpClient } from "./client";

function respond(status: number, body: unknown): typeof fetch {
	return (async () => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })) as typeof fetch;
}

describe("plan refusals", () => {
	it("reports a plan refusal code and still throws the error", async () => {
		const onPlanRefused = vi.fn();
		const client = createCloudCpClient({
			baseUrl: "https://cp.test",
			getToken: async () => "token",
			fetchImpl: respond(409, { code: "WORKER_SLOTS_FULL", message: "All your plan's running sessions are in use." }),
			onPlanRefused,
		});
		await expect(client.resumeSession("org-1", "session-1")).rejects.toMatchObject({ code: "WORKER_SLOTS_FULL" });
		expect(onPlanRefused).toHaveBeenCalledWith("WORKER_SLOTS_FULL");
	});

	it("ignores errors that are not about the plan", async () => {
		const onPlanRefused = vi.fn();
		const client = createCloudCpClient({
			baseUrl: "https://cp.test",
			getToken: async () => "token",
			fetchImpl: respond(409, { code: "WORKER_UNAVAILABLE", message: "The session worker is not connected." }),
			onPlanRefused,
		});
		await expect(client.resumeSession("org-1", "session-1")).rejects.toBeTruthy();
		expect(onPlanRefused).not.toHaveBeenCalled();
	});
});
