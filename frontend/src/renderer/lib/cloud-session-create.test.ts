import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import type { CloudCpCreateSessionRequest } from "./cloud-cp";
import { CloudTaskFilesNeedChatError, CloudTaskFilesUncertainError, CloudTaskFilesUndeliveredError, cloudDefaultInterfaceMode, createCloudChatSessionWithFiles, createCloudSession } from "./cloud-session-create";

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

	// A create whose answer was lost may have made the session: a retry with
	// the same request id replays it rather than starting a second session.
	it("replays a create whose answer was lost, so its retry reaches the same session", async () => {
		const client = clientFor();
		client.createSession.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		const upload = vi.fn().mockResolvedValue({ id: "f" });
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-lost" })).rejects.toBeInstanceOf(TypeError);
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-lost" });
		const [first, second] = client.createSession.mock.calls.map((call) => call[2].idempotencyKey);
		expect(first).toMatch(/^req-lost:files:/);
		expect(second).toBe(first);
		expect(client.sendSessionMessage).toHaveBeenCalledOnce();
		// Settled, the request id starts afresh.
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-lost" });
		expect(client.createSession.mock.calls[2]![2].idempotencyKey).not.toBe(first);
	});

	// The cleanup could not delete the session, so it is still there: the
	// retry continues it, with the files it already holds, under the same keys.
	it("continues the session a failed cleanup left, with the files it holds", async () => {
		const client = clientFor();
		client.sendSessionMessage.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		client.deleteSession.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		const upload = vi.fn(async (_sessionId: string, file: { name?: string }) => ({ id: `id-${file.name}` }));
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-left" })).rejects.toBeInstanceOf(CloudTaskFilesUncertainError);
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: "req-left" });
		const [first, second] = client.createSession.mock.calls.map((call) => call[2].idempotencyKey);
		expect(second).toBe(first);
		expect(upload).toHaveBeenCalledTimes(files.length);
		expect(client.sendSessionMessage.mock.calls[1]).toEqual(client.sendSessionMessage.mock.calls[0]);
	});

	// Nothing was made by a refused create or is left of a deleted session.
	it.each([
		["a refused create", "create"],
		["a deleted session", "delete"],
	])("starts afresh after %s", async (_name, step) => {
		const client = clientFor();
		const upload = vi.fn().mockResolvedValue({ id: "f" });
		if (step === "create") client.createSession.mockRejectedValueOnce(new CloudCpError("All your plan's running sessions are in use.", { status: 409, code: "WORKER_SLOTS_FULL" }));
		else upload.mockRejectedValueOnce(new Error("This file type is not allowed."));
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: `req-${step}` })).rejects.toBeInstanceOf(Error);
		await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload, { idempotencyKey: `req-${step}` });
		const [first, second] = client.createSession.mock.calls.map((call) => call[2].idempotencyKey);
		expect(second).not.toBe(first);
	});

	// The other files stop uploading once one fails, and the session is
	// deleted only after they stopped, so none lands in it afterwards.
	it("stops the other uploads when one fails, before deleting the session", async () => {
		const client = clientFor();
		const order: string[] = [];
		client.deleteSession.mockImplementation(async () => { order.push("delete"); });
		const upload = vi.fn((_sessionId: string, file: { name?: string }, signal: AbortSignal) => file.name === "screen.png"
			? Promise.reject(new Error("This file type is not allowed."))
			: new Promise<{ id: string }>((_resolve, reject) => signal.addEventListener("abort", () => {
				order.push("stopped");
				reject(new DOMException("Aborted", "AbortError"));
			})));
		await expect(createCloudChatSessionWithFiles(client, "org-1", chatBody, files, upload)).rejects.toBeInstanceOf(CloudTaskFilesUndeliveredError);
		expect(order).toEqual(["stopped", "delete"]);
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

	// Only a session known to be gone makes the failure final: one the delete
	// did not reach may still take the brief, so its error stays uncertain.
	it("makes an upload failure final once its session is deleted, and only then", async () => {
		const failure = new Error("Couldn’t reach Orchestrator Cloud. Check the connection and try again.");
		const deleted = clientFor();
		const outcome = await createCloudChatSessionWithFiles(deleted, "org-1", chatBody, files, vi.fn().mockRejectedValue(failure)).catch((error: unknown) => error);
		expect(outcome).toBeInstanceOf(CloudTaskFilesUndeliveredError);
		expect(outcome).toMatchObject({ code: "CLOUD_TASK_FILES_UNDELIVERED", message: failure.message });
		const kept = clientFor();
		kept.deleteSession.mockRejectedValue(new TypeError("Failed to fetch"));
		const uncertain = await createCloudChatSessionWithFiles(kept, "org-1", chatBody, files, vi.fn().mockRejectedValue(failure)).catch((error: unknown) => error);
		expect(uncertain).toBeInstanceOf(CloudTaskFilesUncertainError);
		expect(uncertain).toMatchObject({ message: failure.message, cause: failure });
	});

	// A refusal of the brief or a session that did not start in Chat is final
	// only once the cleanup deleted the session: one the delete did not reach
	// is still there, so the start stays uncertain whatever the refusal said,
	// and a retry with the same request id continues it.
	it.each([
		["a refused brief", "send"],
		["a session that did not start in Chat", "terminal"],
	])("keeps the start uncertain after %s when the cleanup could not delete the session", async (_name, failing) => {
		const client = clientFor(failing === "terminal" ? "tui" : "chat");
		const refusal = new CloudCpError("This session's sandbox runs an older AO worker that cannot receive attached files.", { status: 409, code: "ATTACHMENTS_UNSUPPORTED" });
		if (failing === "send") client.sendSessionMessage.mockRejectedValue(refusal);
		client.deleteSession.mockRejectedValue(new TypeError("Failed to fetch"));
		const outcome = await createCloudChatSessionWithFiles(client, "org-1", chatBody, files, vi.fn().mockResolvedValue({ id: "f" }), { idempotencyKey: `req-${failing}` })
			.catch((error: unknown) => error);
		expect(outcome).toBeInstanceOf(CloudTaskFilesUncertainError);
		expect(outcome).not.toBeInstanceOf(CloudCpError);
		expect(outcome).not.toHaveProperty("code");
		expect((outcome as Error).cause).toBeInstanceOf(failing === "send" ? CloudCpError : CloudTaskFilesNeedChatError);
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
