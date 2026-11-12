import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import type { CloudCpCreateSessionRequest } from "./cloud-cp";
import { CloudTaskFilesNeedChatError, cloudDefaultInterfaceMode, createCloudChatSessionWithFiles, createCloudSession } from "./cloud-session-create";

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

describe("createCloudChatSessionWithFiles", () => {
	const chatBody = { ...body, interfaceMode: "chat" as const, model: "opus", reasoningEffort: "high", approvalMode: "auto" as const };
	const files = [{ mimeType: "image/png", data: "aGk=", name: "screen.png" }, { mimeType: "image/jpeg", data: "aGk=", name: "photo.jpg" }];
	const clientFor = (interfaceMode = "chat") => ({
		createSession: vi.fn().mockResolvedValue({ session: { id: "s1", interfaceMode } }),
		sendSessionMessage: vi.fn().mockResolvedValue({ event: {} }),
		deleteSession: vi.fn().mockResolvedValue({}),
	});

	// The prompt would queue the first turn before the files exist, so the
	// session starts empty and the brief follows as its first message, with the
	// files and the task's settings.
	it("creates the session without its prompt and sends the brief with the uploaded files", async () => {
		const client = clientFor();
		const upload = vi.fn(async (_sessionId: string, file: { name?: string }) => ({ id: `id-${file.name}` }));
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-1" }))
			.resolves.toEqual({ session: { id: "s1", interfaceMode: "chat" } });
		expect(client.createSession).toHaveBeenCalledTimes(1);
		const [, created, createOptions] = client.createSession.mock.calls[0]!;
		expect(created).toEqual({ ...chatBody, prompt: "" });
		expect(createOptions.idempotencyKey).toMatch(/^req-1:files:/);
		expect(upload.mock.calls.map(([sessionId, file]) => [sessionId, file.name])).toEqual([["s1", "screen.png"], ["s1", "photo.jpg"]]);
		expect(client.sendSessionMessage).toHaveBeenCalledWith("org-1", "s1", {
			text: "do the work", attachmentIds: ["id-screen.png", "id-photo.jpg"], model: "opus", reasoningEffort: "high", approvalMode: "auto",
		}, { idempotencyKey: `${createOptions.idempotencyKey}:brief` });
		expect(client.deleteSession).not.toHaveBeenCalled();
	});

	// A retry after a deleted attempt must not replay that attempt's create.
	it("creates each attempt under its own key", async () => {
		const client = clientFor();
		const upload = vi.fn().mockResolvedValue({ id: "f" });
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-1" });
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-1" });
		expect(client.createSession.mock.calls[0]![2].idempotencyKey).not.toBe(client.createSession.mock.calls[1]![2].idempotencyKey);
	});

	it.each([
		["a file that fails to upload", "upload"],
		["a brief the control plane refuses", "send"],
	])("deletes the session and keeps the task for %s", async (_name, failing) => {
		const client = clientFor();
		const refusal = new CloudCpError("An attachment can be at most 25 MiB.", { status: 413, code: "attachment_too_large" });
		const upload = failing === "upload" ? vi.fn().mockRejectedValue(refusal) : vi.fn().mockResolvedValue({ id: "f" });
		if (failing === "send") client.sendSessionMessage.mockRejectedValue(refusal);
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload)).rejects.toBe(refusal);
		expect(client.deleteSession).toHaveBeenCalledWith("org-1", "s1");
	});

	// A control plane that predates interfaceMode starts the session in the
	// terminal, where the brief would not reach the agent with its files.
	it("deletes a session that did not start in Chat", async () => {
		const client = clientFor("tui");
		const upload = vi.fn();
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload)).rejects.toBeInstanceOf(CloudTaskFilesNeedChatError);
		expect(upload).not.toHaveBeenCalled();
		expect(client.sendSessionMessage).not.toHaveBeenCalled();
		expect(client.deleteSession).toHaveBeenCalledWith("org-1", "s1");
	});
});
