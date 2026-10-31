import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import type { CloudCpCreateSessionRequest } from "./cloud-cp";
import { cloudDefaultInterfaceMode, createCloudSession } from "./cloud-session-create";

const body: CloudCpCreateSessionRequest = {
	projectId: "project-1",
	kind: "worker",
	harness: "claude-code",
	displayName: "Task",
	prompt: "do the work",
};

describe("cloudDefaultInterfaceMode", () => {
	// The control plane starts an orchestrator created without a default in
	// the terminal, as an older desktop expects, so "tui" is sent too.
	it("always sends the default session interface", () => {
		expect(cloudDefaultInterfaceMode({ defaultSessionMode: "chat" })).toBe("chat");
		expect(cloudDefaultInterfaceMode({ defaultSessionMode: "tui" })).toBe("tui");
		expect(cloudDefaultInterfaceMode(undefined)).toBe("tui");
	});
});

describe("createCloudSession", () => {
	it("sends the interface with the create", async () => {
		const createSession = vi.fn().mockResolvedValue({ session: { id: "s1" } });
		await createCloudSession({ createSession }, "org-1", { ...body, interfaceMode: "chat" });
		expect(createSession).toHaveBeenCalledTimes(1);
		expect(createSession).toHaveBeenCalledWith("org-1", { ...body, interfaceMode: "chat" });
	});

	it("sends the request an older desktop sent when there is no interface to ask for", async () => {
		const createSession = vi.fn().mockResolvedValue({ session: { id: "s1" } });
		await createCloudSession({ createSession }, "org-1", body);
		expect(createSession).toHaveBeenCalledWith("org-1", body);
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("interfaceMode");
		expect(createSession.mock.calls[0]?.[1]).not.toHaveProperty("defaultInterfaceMode");
	});

	it.each([
		["a task's chat interface", { interfaceMode: "chat" as const }],
		["a task's tui interface", { interfaceMode: "tui" as const }],
		["an orchestrator's default", { defaultInterfaceMode: "chat" as const }],
	])("retries %s without the interface fields on a control plane that rejects them", async (_name, fields) => {
		const createSession = vi.fn()
			.mockRejectedValueOnce(new CloudCpError("The request body is invalid.", { status: 400, code: "invalid_request" }))
			.mockResolvedValueOnce({ session: { id: "s1" } });
		await expect(createCloudSession({ createSession }, "org-1", { ...body, ...fields })).resolves.toEqual({ session: { id: "s1" } });
		expect(createSession).toHaveBeenNthCalledWith(1, "org-1", { ...body, ...fields });
		// The prompt stays: an older control plane starts the session in the
		// terminal with the prompt on the agent's command line.
		expect(createSession).toHaveBeenNthCalledWith(2, "org-1", body);
	});

	it("does not retry any other refusal", async () => {
		for (const refusal of [
			new CloudCpError("Connect a credential.", { status: 422, code: "agent_provider_required" }),
			new CloudCpError("aider can't run in Chat in AO Cloud.", { status: 409, code: "SESSION_MODE_UNSUPPORTED" }),
		]) {
			const createSession = vi.fn().mockRejectedValue(refusal);
			await expect(createCloudSession({ createSession }, "org-1", { ...body, interfaceMode: "chat" })).rejects.toBe(refusal);
			expect(createSession).toHaveBeenCalledTimes(1);
		}
	});
});
