import { describe, expect, it, vi } from "vitest";
import { spawnCloudWorker, type CloudActionContext } from "./actions";
import { base64Bytes, composerUploadSources, uploadCloudSource } from "./attachments";
import type { CloudClient } from "./client";
import { cloudChatStageTitle } from "./lifecycle";
import { ARCHIVE_CLOUD_SESSION_CONFIRM, ARCHIVE_CLOUD_SESSION_HINT, cloudSessionMenuGroups } from "./sessionMenu";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { cloudSettingsStatus } from "./settingsStatus";
import { cloudModelCatalog } from "./spawnSource";
import type { CloudProviderConnection, CloudSession } from "./types";

// Orchestrator Cloud on the native screens: the "⋯" sheet, the machine row in
// Settings, the composer's uploads and New task's model controls.

const connection = (provider: string, validationState: CloudProviderConnection["validationState"] = "valid"): CloudProviderConnection =>
	({ id: provider, provider, label: "default", config: {}, validationState, createdAt: "", updatedAt: "" });

describe("cloud session actions sheet", () => {
	const titles = (groups: ReturnType<typeof cloudSessionMenuGroups>) => groups.map((group) => [group.title, group.items.map((item) => item.label)]);

	it("offers the shell, pin, PR Review and Archive, in the local sheet's sections", () => {
		const groups = cloudSessionMenuGroups({ terminated: false, pinned: false, canShowAgentTerminal: false, pullRequest: { number: 42 }, projectName: "web" });
		expect(titles(groups)).toEqual([
			["Open", ["Shell", "Project"]],
			["Worker", ["Pin", "PR Review"]],
			["Session", ["Archive"]],
		]);
		const archive = groups.at(-1)!.items[0];
		expect(archive).toMatchObject({ action: "delete", destructive: true });
		expect(groups[0].items[1].subtitle).toBe("web");
		expect(groups[1].items[1]).toMatchObject({ action: "pull_requests", subtitle: "Pull request #42" });
	});

	it("opens the session's artifacts, as a local session's, once there are some", () => {
		const open = (artifactCount?: number) => cloudSessionMenuGroups({ terminated: false, pinned: false, canShowAgentTerminal: false, artifactCount })[0].items.map((item) => [item.label, item.subtitle]);
		expect(open(3)).toContainEqual(["Artifacts", "3 files"]);
		expect(open(1)).toContainEqual(["Artifacts", "1 file"]);
		expect(open(0).map(([label]) => label)).not.toContain("Artifacts");
		expect(open(undefined).map(([label]) => label)).not.toContain("Artifacts");
	});

	// As a local chat's sheet: the switch sits with the session, above Archive,
	// and reads as under way while it runs.
	it("offers switching a Chat session to Terminal UI and a Terminal UI one back to Chat", () => {
		const session = (interfaceSwitch?: { to: "chat" | "tui"; switching: boolean }, terminated = false) =>
			cloudSessionMenuGroups({ terminated, pinned: false, canShowAgentTerminal: false, interfaceSwitch }).at(-1)!.items.map((item) => [item.action, item.label, item.disabled ?? false]);
		expect(session({ to: "tui", switching: false })).toEqual([["switch_tui", "Switch to Terminal UI", false], ["delete", "Archive", false]]);
		expect(session({ to: "chat", switching: false })[0]).toEqual(["switch_chat", "Switch to Chat", false]);
		expect(session({ to: "chat", switching: true })[0]).toEqual(["switch_chat", "Switching to Chat…", true]);
		expect(session(undefined).map(([action]) => action)).toEqual(["delete"]);
		expect(session({ to: "tui", switching: false }, true).map(([action]) => action)).toEqual(["restore"]);
	});

	it("offers the agent's terminal beside the chat of a Terminal UI session, and Restore once archived", () => {
		const groups = cloudSessionMenuGroups({ terminated: true, pinned: true, canShowAgentTerminal: true });
		expect(titles(groups)).toEqual([
			["Open", ["Agent terminal"]],
			["Worker", ["Unpin"]],
			["Session", ["Restore"]],
		]);
		expect(groups.at(-1)!.items[0]).toMatchObject({ action: "restore" });
		expect(groups.at(-1)!.items[0].destructive).toBeUndefined();
	});

	// Archiving deletes the files attached to its messages, and a restored session
	// comes back without them (the control plane's deleteSessionAttachmentsTx).
	it("says what archiving a cloud session keeps and what it deletes, in the sheet and in the confirmation", () => {
		const archive = cloudSessionMenuGroups({ terminated: false, pinned: false, canShowAgentTerminal: false }).at(-1)!.items[0];
		expect(archive.subtitle).toBe(ARCHIVE_CLOUD_SESSION_HINT);
		expect(ARCHIVE_CLOUD_SESSION_HINT).toBe("Stops the agent. Keeps its work; deletes attached files.");
		expect(ARCHIVE_CLOUD_SESSION_CONFIRM).toEqual({
			...ARCHIVE_SESSION_CONFIRM,
			// The desktop's archive.bodyCloud, then what it leaves out.
			message: "You can always restore it from the Archive section later — its conversation and work are kept. Files attached to its messages are deleted.",
		});
	});

	it("never offers rename: the board's row renames a session, as for a local one", () => {
		const labels = cloudSessionMenuGroups({ terminated: false, pinned: false, canShowAgentTerminal: true, pullRequest: { number: 1 }, projectName: "p" })
			.flatMap((group) => group.items.map((item) => item.label));
		expect(labels).not.toContain("Rename");
	});
});

describe("Orchestrator Cloud's row in Settings", () => {
	const ready = (connections: CloudProviderConnection[]) => cloudSettingsStatus({ status: "ready", connections, connectionsLoaded: true });

	it("says whether it answers, then whether an agent can run there", () => {
		expect(cloudSettingsStatus({ status: "connecting", connections: [], connectionsLoaded: false })).toEqual({ label: "Connecting…", tone: "default" });
		expect(cloudSettingsStatus({ status: "error", connections: [], connectionsLoaded: false })).toEqual({ label: "Can't reach Orchestrator Cloud", tone: "bad" });
		expect(ready([])).toEqual({ label: "No coding agent connected", tone: "default" });
		expect(ready([connection("claude-code")])).toEqual({ label: "Connected · 1 coding agent", tone: "good" });
		expect(ready([connection("claude-code"), connection("codex")])).toEqual({ label: "Connected · 2 coding agents", tone: "good" });
	});

	it("puts a credential the provider stopped accepting first", () => {
		expect(ready([connection("claude-code"), connection("codex", "invalid")])).toEqual({ label: "Sign in to Codex again", tone: "bad" });
	});
});

describe("the shared composer's attachments, uploaded to a cloud session", () => {
	it("names photos in pick order, sends text files as their text, and reads other files from the phone", async () => {
		const readBytes = vi.fn(async () => new Uint8Array([9, 9]));
		const sources = composerUploadSources(
			[{ mimeType: "image/jpeg", data: "AQID" }, { mimeType: "image/png", data: "BA==" }],
			[{ uri: "mobile-attachment://notes.md", name: "notes.md", mimeType: "text/markdown", text: "hi" }, { uri: "file:///cache/spec.pdf", name: "spec.pdf", mimeType: "application/pdf" }],
			readBytes,
		);
		expect(sources.map(({ name, mimeType, size }) => ({ name, mimeType, size }))).toEqual([
			{ name: "photo-1.jpg", mimeType: "image/jpeg", size: 3 },
			{ name: "photo-2.png", mimeType: "image/png", size: 1 },
			{ name: "notes.md", mimeType: "text/markdown", size: 2 },
			{ name: "spec.pdf", mimeType: "application/pdf", size: undefined },
		]);
		expect([...await sources[0].read()]).toEqual([1, 2, 3]);
		expect(new TextDecoder().decode(await sources[2].read())).toBe("hi");
		await sources[3].read();
		expect(readBytes).toHaveBeenCalledWith("file:///cache/spec.pdf");
	});

	it("decodes base64 the photo picker hands over", () => {
		expect([...base64Bytes("AQID")]).toEqual([1, 2, 3]);
		expect([...base64Bytes("AQ\nID")]).toEqual([1, 2, 3]);
	});

	it("refuses a file over the organization's limit before reading it", async () => {
		const read = vi.fn(async () => new Uint8Array(10));
		const uploadAttachment = vi.fn();
		await expect(uploadCloudSource({ uploadAttachment } as never, { orgId: "o", sessionId: "s" }, { name: "big.bin", size: 11, read }, 10, async () => "x")).rejects.toThrow("big.bin is larger than 10 bytes");
		expect(read).not.toHaveBeenCalled();
		expect(uploadAttachment).not.toHaveBeenCalled();
	});

	it("uploads with the file's hash, type and a name the control plane keeps", async () => {
		const uploadAttachment = vi.fn(async () => ({ id: "a1", fileName: "a.txt", contentType: "text/plain", size: 1 }));
		const uploaded = await uploadCloudSource({ uploadAttachment } as never, { orgId: "o", sessionId: "s" }, { name: "dir/a.txt", mimeType: "text/plain", read: async () => new Uint8Array([1]) }, 10, async () => "abc");
		expect(uploaded.id).toBe("a1");
		expect(uploadAttachment).toHaveBeenCalledWith("o", "s", expect.objectContaining({ name: "dir_a.txt", sha256: "abc", contentType: "text/plain" }));
	});
});

describe("New task on Orchestrator Cloud", () => {
	const session = { id: "new", projectId: "p1", kind: "worker", harness: "codex", displayName: "x", desiredState: "running", createdAt: "", updatedAt: "" } as unknown as CloudSession;
	const context = (createSession: CloudClient["createSession"]): CloudActionContext => ({
		client: { createSession } as unknown as CloudClient,
		org: { id: "o" } as CloudActionContext["org"],
		projects: [],
		connections: [connection("codex"), connection("opencode")],
		chatHarnesses: ["codex", "claude-code"],
		sessions: [],
		upsertSession: vi.fn(),
		refreshSessions: vi.fn(async () => {}),
	});

	it("starts the session with the effort and approval mode picked", async () => {
		const createSession = vi.fn(async () => ({ session }));
		await spawnCloudWorker(context(createSession as never), { projectId: "p1", prompt: "Fix it", harness: "codex", effort: "high", approvalMode: "accept-edits" });
		expect(createSession.mock.calls[0]).toEqual(["o", expect.objectContaining({ reasoningEffort: "high", approvalMode: "accept-edits", interfaceMode: "chat" }), undefined]);
	});

	it("leaves both to the model and the session when nothing was picked, and the approval mode out of a terminal session", async () => {
		const createSession = vi.fn(async () => ({ session }));
		await spawnCloudWorker(context(createSession as never), { projectId: "p1", prompt: "Fix it", harness: "codex" });
		await spawnCloudWorker(context(createSession as never), { projectId: "p1", prompt: "Fix it", harness: "opencode", approvalMode: "auto" });
		const [first, second] = createSession.mock.calls.map((call) => (call as unknown[])[1] as Record<string, unknown>);
		expect(first).not.toHaveProperty("reasoningEffort");
		expect(first).not.toHaveProperty("approvalMode");
		expect(second).toMatchObject({ interfaceMode: "tui" });
		expect(second).not.toHaveProperty("approvalMode");
	});

	it("lists each model's effort levels, so the effort control works as on a machine", () => {
		const catalog = cloudModelCatalog("codex", { models: [
			{ id: "gpt-5", displayName: "GPT-5", default: true, efforts: ["low", "medium", "high"], defaultEffort: "medium" },
			{ id: "mini", displayName: "Mini", default: false },
		] }, new Date(0));
		expect(catalog.models).toEqual([
			{ id: "gpt-5", label: "GPT-5", isDefault: true, efforts: ["low", "medium", "high"], defaultEffort: "medium" },
			{ id: "mini", label: "Mini", isDefault: false },
		]);
	});
});

describe("the cloud chat's banner for the sandbox's stage", () => {
	const quiet = { agentLive: false, hasHistory: false };

	it("says nothing about a runtime link the row reports behind an agent that is working or was seen connected", () => {
		expect(cloudChatStageTitle("restoring", { agentLive: true, hasHistory: false })).toBeUndefined();
		expect(cloudChatStageTitle("restoring", { agentLive: true, hasHistory: true })).toBeUndefined();
		expect(cloudChatStageTitle("connected", quiet)).toBeUndefined();
		expect(cloudChatStageTitle(undefined, quiet)).toBeUndefined();
	});

	it("calls a new session's gap before the agent checks in part of starting, and only an older one's a reconnect", () => {
		expect(cloudChatStageTitle("restoring", quiet)).toBe("Starting agent");
		expect(cloudChatStageTitle("restoring", { agentLive: false, hasHistory: true })).toBe("Reconnecting");
	});

	it("keeps naming a sandbox that is genuinely starting or waking, whatever the chat shows", () => {
		expect(cloudChatStageTitle("provisioning", { agentLive: true, hasHistory: true })).toBe("Creating sandbox");
		expect(cloudChatStageTitle("starting", quiet)).toBe("Starting agent");
		expect(cloudChatStageTitle("resuming", quiet)).toBe("Waking up");
	});
});
