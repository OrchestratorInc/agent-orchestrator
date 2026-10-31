import { beforeEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./api-client";
import { createCue, fetchProjectCues, invokeCloudCue, invokeCue, type CueHost } from "./cues";

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn() },
	apiErrorMessage: vi.fn(),
}));

describe("invokeCue", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("preserves an explicitly supplied empty session id", async () => {
		(apiClient.POST as ReturnType<typeof vi.fn>).mockResolvedValue({
			data: { kind: "agent", sessionId: "unused" },
			error: undefined,
		});

		await expect(invokeCue("cue-1", "", "pwsh")).resolves.toEqual({ kind: "agent", sessionId: "unused" });

		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/cues/{cueId}/invoke", {
			params: { path: { cueId: "cue-1" } },
			body: { sessionId: "", shell: "pwsh" },
		});
	});
});

describe("Cloud cues", () => {
	it("reads, creates and runs a Cloud project's cues on the control plane, never the local daemon", async () => {
		vi.clearAllMocks();
		const client = {
			listProjectCues: vi.fn().mockResolvedValue({ cues: [{ id: "c", name: "Test" }] }),
			createProjectCue: vi.fn().mockResolvedValue({ cue: { id: "c", name: "Test" } }),
			invokeProjectCue: vi.fn().mockResolvedValue({ kind: "command", sessionId: "s", command: "npm test" }),
		};
		const cloud = { client, orgId: "org" } as unknown as CueHost;
		expect(await fetchProjectCues("p", cloud)).toEqual([{ id: "c", name: "Test" }]);
		await createCue("p", { name: "Test", type: "command", command: "npm test", prompt: "dropped" }, cloud);
		expect(client.createProjectCue).toHaveBeenCalledWith("org", "p", { name: "Test", type: "command", command: "npm test" });
		expect(await invokeCloudCue(cloud, "c", "s")).toEqual({ kind: "command", sessionId: "s", command: "npm test" });
		expect(client.invokeProjectCue).toHaveBeenCalledWith("org", "c", "s");
		expect(apiClient.POST).not.toHaveBeenCalled();
	});
});
