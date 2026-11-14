import { readFileSync } from "node:fs";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
const secureStore = vi.hoisted(() => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo-secure-store", () => secureStore);
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import { getSessions } from "../api";
import { getConversationPage, sendConversationMessage, streamGlobalConversationEvents } from "../chat/api";
import type { ServerConfig } from "../config";
import { UnreachableError } from "../connectionError";
import { probeIdentity } from "../connectRuntime";
import { addDemoMachine } from "./machine";
import { DEMO_HOST_ID, DEMO_HOSTNAME, DEMO_PORT, DemoServer } from "./server";
import { DemoSocket, demoEventStream, demoFetch, demoServer, installDemoTransport, isDemoUrl, orderedDelivery, resetDemoServer, setDemoNetwork } from "./transport";
import { demoWorkspace } from "./workspace";

const cfg: ServerConfig = { host: DEMO_HOSTNAME, httpPort: String(DEMO_PORT), muxPort: String(DEMO_PORT), secure: false, password: "demo-token", hostId: DEMO_HOST_ID };

describe("demo machine", () => {
	beforeEach(() => {
		vi.stubGlobal("__DEV__", true);
		resetDemoServer();
		vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => demoFetch(input, init));
		vi.spyOn(console, "info").mockImplementation(() => {});
	});
	afterEach(() => {
		resetDemoServer();
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	it("answers only its own address", () => {
		expect(isDemoUrl(`http://${DEMO_HOSTNAME}:${DEMO_PORT}/api/v1/identity`)).toBe(true);
		expect(isDemoUrl(`ws://${DEMO_HOSTNAME}:${DEMO_PORT}/mux`)).toBe(true);
		expect(isDemoUrl("http://192.168.1.4:3011/api/v1/identity")).toBe(false);
		expect(isDemoUrl(`http://${DEMO_HOSTNAME}.evil.test/api/v1/identity`)).toBe(false);
	});

	it("identifies as the demo machine to the connect probe", async () => {
		await expect(probeIdentity(cfg)).resolves.toBe(DEMO_HOST_ID);
	});

	it("fills the board through the app's own session loader", async () => {
		const board = await getSessions(cfg, "all");
		expect(board.projects?.map((p) => p.id)).toEqual(["web-app", "api-server", "mobile", "platform"]);
		expect(board.sessions).toHaveLength(40);
		expect(new Set(board.sessions.map((s) => s.kanbanColumn))).toEqual(new Set(["building", "validating", "needs_review", "ready"]));
		expect(board.sessions.some((s) => s.pr?.ciStatus === "failing")).toBe(true);
		expect(board.orchestrators).toHaveLength(4);
		expect(board.archive).toEqual({ total: 48, byProject: { "web-app": 12, "api-server": 12, mobile: 12, platform: 12 } });
	});

	it("pages a chat's history the way the daemon does", async () => {
		const latest = await getConversationPage(cfg, "mob-2");
		expect(latest.items.length).toBeGreaterThan(0);
		expect(latest.items.length).toBeLessThanOrEqual(50);
		expect(latest.items.map((item) => item.sequence)).toEqual([...latest.items.map((item) => item.sequence)].sort((a, b) => a - b));
		if (latest.hasMoreBefore) {
			const older = await getConversationPage(cfg, "mob-2", latest.oldestSequence);
			expect(Math.max(...older.items.map((item) => item.sequence))).toBeLessThan(latest.oldestSequence);
		}
	});

	it("streams a reply: text grows each tick and every step is a change event", () => {
		const server = new DemoServer(0);
		const events: string[] = [];
		server.onEvent((event) => events.push(event.sessionId));
		server.tick(0); // autopilot starts both live chats
		const page = () => server.handle("GET", "/api/v1/sessions/web-1/conversation?limit=50").body as { controller: string; messages: { text: string; streaming: boolean }[] };
		const first = page().messages.at(-1)!;
		server.tick(100);
		server.tick(200);
		const later = page().messages.at(-1)!;
		expect(page().controller).toBe("busy");
		expect(later.streaming).toBe(true);
		expect(later.text.length).toBeGreaterThan(first.text.length);
		expect(events.filter((id) => id === "web-1").length).toBe(3);
	});

	it("replies to a message sent from the phone", async () => {
		const sent = await sendConversationMessage(cfg, "plat-4", { text: "Hello demo", clientMessageId: "c1" });
		expect(sent.duplicate).toBe(false);
		const page = await getConversationPage(cfg, "plat-4");
		const texts = page.items.flatMap((item) => item.kind === "message" ? [item.text] : []);
		expect(texts.at(-2)).toBe("Hello demo");
		expect(page.controller.state).toBe("busy");
	});

	it("pushes change events down the live stream", async () => {
		const expoFetch = (await import("expo/fetch")).fetch as unknown as ReturnType<typeof vi.fn>;
		const controller = new AbortController();
		const seen: string[] = [];
		const done = streamGlobalConversationEvents(cfg, 0, controller.signal, (event) => {
			seen.push(event.sessionId ?? "");
			if (seen.length === 2) controller.abort();
		});
		await vi.waitFor(() => expect(seen.length).toBe(0));
		demoServer().tick(Date.now());
		demoServer().tick(Date.now() + 100);
		await done;
		expect(seen).toHaveLength(2);
		expect(expoFetch).not.toHaveBeenCalled();
	});

	it("serves a worktree to the Files screens through the app's own readers", async () => {
		const { getWorkspaceFile, getWorkspaceManifest, getWorkspaceTree, searchWorkspaceFiles } = await import("../files/api");
		const manifest = await getWorkspaceManifest(cfg, "web-1");
		expect(manifest.files.length).toBeGreaterThan(0);
		const changed = manifest.files[0];
		const file = await getWorkspaceFile(cfg, "web-1", changed.path);
		expect(file.content.split("\n").length).toBeGreaterThan(40);
		expect(file.diff).toContain(`+++ b/${changed.path}`);
		const root = await getWorkspaceTree(cfg, "web-1");
		expect(root.entries.some((entry) => entry.type === "dir")).toBe(true);
		expect((await searchWorkspaceFiles(cfg, "web-1", "README")).results.map((result) => result.path)).toEqual(["README.md"]);
	});

	it("serves artifacts, a preview and a worktree shell", async () => {
		const { getArtifactText, getPreview } = await import("../api");
		const { openSessionShell } = await import("../chat/api");
		const board = await getSessions(cfg, "all");
		const worker = board.sessions.find((session) => session.id === "web-1")!;
		expect((worker.artifacts ?? []).map((artifact) => artifact.path)).toContain("report.md");
		expect(await getArtifactText(cfg, "web-1", "report.md")).toContain("# web-1 report");
		expect(await getPreview(cfg, "web-1")).toMatchObject({ url: "https://example.com/", authenticated: false });
		const shell = await openSessionShell(cfg, "web-1", "web-app");
		expect(await openSessionShell(cfg, "web-1", "web-app")).toEqual(shell);
	});

	it("fails like an unreachable machine when switched off", async () => {
		setDemoNetwork("offline");
		await expect(getSessions(cfg, "all")).rejects.toBeInstanceOf(UnreachableError);
	});

	it("holds every answer for the chosen network delay", async () => {
		vi.useFakeTimers();
		setDemoNetwork("bad");
		let settled = false;
		const pending = getConversationPage(cfg, "web-2").then(() => { settled = true; });
		await vi.advanceTimersByTimeAsync(1_000);
		expect(settled).toBe(false);
		await vi.advanceTimersByTimeAsync(1_000);
		await pending;
		expect(settled).toBe(true);
	});

	it("fails a request still on its way when the machine is switched off", async () => {
		vi.useFakeTimers();
		setDemoNetwork("bad");
		const outcome = demoFetch(`http://${DEMO_HOSTNAME}:${DEMO_PORT}/api/v1/projects`).then(() => "answered", (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(500);
		setDemoNetwork("offline");
		await vi.advanceTimersByTimeAsync(2_000);
		expect(await outcome).toEqual(new TypeError("Network request failed"));
	});

	// A frame never overtakes one sent ahead of it, however the jitter falls.
	it("hands over a stream's frames in the order they were sent", async () => {
		vi.useFakeTimers();
		const delivered: number[] = [];
		const delivery = orderedDelivery();
		[400, 10, 250, 0, 90].forEach((delayMs, index) => delivery.send(() => delivered.push(index), delayMs));
		await vi.advanceTimersByTimeAsync(399);
		expect(delivered).toEqual([]);
		await vi.advanceTimersByTimeAsync(1);
		expect(delivered).toEqual([0, 1, 2, 3, 4]);
		// Still late: a later frame waits out its own delay.
		delivery.send(() => delivered.push(5), 100);
		await vi.advanceTimersByTimeAsync(99);
		expect(delivered).toHaveLength(5);
		await vi.advanceTimersByTimeAsync(1);
		expect(delivered).toEqual([0, 1, 2, 3, 4, 5]);
		delivery.send(() => delivered.push(6), 50);
		delivery.clear();
		await vi.advanceTimersByTimeAsync(100);
		expect(delivered).toHaveLength(6);
	});

	it("keeps the live stream's events in order on a jittery link", async () => {
		vi.useFakeTimers();
		setDemoNetwork("terrible");
		const random = vi.spyOn(Math, "random");
		const opening = demoEventStream("");
		await vi.advanceTimersByTimeAsync(5_000);
		const reader = (await opening).body.getReader();
		// The first frame draws the longest delay, the next ones the shortest.
		random.mockReturnValueOnce(1).mockReturnValue(0);
		const server = demoServer();
		server.tick(Date.now());
		server.tick(Date.now() + 100);
		server.tick(Date.now() + 200);
		await vi.advanceTimersByTimeAsync(5_000);
		const seqs: number[] = [];
		for (let i = 0; i < 3; i++) {
			const { value } = await reader.read();
			seqs.push(Number(/^id: (\d+)/.exec(new TextDecoder().decode(value))![1]));
		}
		expect(seqs).toEqual([...seqs].sort((a, b) => a - b));
		await reader.cancel();
	});

	it("keeps a terminal's frames in order on a jittery link", async () => {
		vi.useFakeTimers();
		setDemoNetwork("terrible");
		const socket = new DemoSocket(`ws://${DEMO_HOSTNAME}:${DEMO_PORT}/mux`);
		await vi.advanceTimersByTimeAsync(5_000);
		const frames: string[] = [];
		socket.onmessage = ({ data }) => frames.push((JSON.parse(data) as { type: string }).type);
		const random = vi.spyOn(Math, "random").mockReturnValueOnce(1).mockReturnValue(0);
		socket.send(JSON.stringify({ ch: "terminal", id: "t1", type: "open" }));
		await vi.advanceTimersByTimeAsync(5_000);
		expect(random).toHaveBeenCalled();
		expect(frames).toEqual(["opened", "data"]);
		socket.close();
	});

	it("passes a message sent to a session to its chat, as the chat's own send does", async () => {
		const { sendMessage } = await import("../api");
		await sendMessage(cfg, "api-3", "Please also add tests", { userAuthored: true });
		const page = await getConversationPage(cfg, "api-3");
		const texts = page.items.flatMap((item) => item.kind === "message" ? [item.text] : []);
		expect(texts.at(-2)).toBe("Please also add tests");
		expect(page.controller.state).toBe("busy");
		await expect(sendMessage(cfg, "api-3", "And again")).rejects.toThrow();
		expect(demoServer().handle("POST", "/api/v1/sessions/api-3/send", "{}").status).toBe(400);
	});

	it("gives every generated module a body, whatever its session's seed", () => {
		const modules = /\/(requests|queue|store|session|cache|router|auth|billing|upload|search)\.(ts|md)$/;
		let checked = 0;
		for (const prefix of ["web", "api", "mob", "plat"]) {
			for (let n = 1; n <= 12; n++) {
				for (const file of demoWorkspace(`${prefix}-${n}`).filter((f) => modules.test(f.path))) {
					checked++;
					expect(file.content.split("\n").length, file.path).toBeGreaterThanOrEqual(40);
				}
			}
		}
		expect(checked).toBe(4 * 12 * 10);
	});
});

describe("demo machine in a release build", () => {
	const demoUrl = `http://${DEMO_HOSTNAME}:${DEMO_PORT}/api/v1/identity`;
	beforeEach(() => vi.stubGlobal("__DEV__", false));
	afterEach(() => {
		resetDemoServer();
		vi.unstubAllGlobals();
		secureStore.setItemAsync.mockClear();
	});

	it("never claims a request, so the demo address goes to the real network and fails", () => {
		expect(isDemoUrl(demoUrl)).toBe(false);
	});

	it("refuses to answer even when called directly", async () => {
		await expect(demoFetch(demoUrl)).rejects.toThrow("Network request failed");
		await expect(demoEventStream(demoUrl)).rejects.toThrow("Network request failed");
	});

	it("leaves fetch and WebSocket alone", () => {
		const fetchBefore = globalThis.fetch;
		const socketBefore = globalThis.WebSocket;
		installDemoTransport();
		expect(globalThis.fetch).toBe(fetchBefore);
		expect(globalThis.WebSocket).toBe(socketBefore);
	});

	it("does not pair the demo machine", async () => {
		await addDemoMachine();
		expect(secureStore.setItemAsync).not.toHaveBeenCalled();
	});

	it("shows its Settings section only behind __DEV__, and its manager returns early otherwise", () => {
		const layout = readFileSync(new URL("../../app/_layout.tsx", import.meta.url), "utf8");
		const settings = readFileSync(new URL("../../app/settings/index.tsx", import.meta.url), "utf8");
		expect(layout).toContain("<DemoMachineManager />");
		expect(readFileSync(new URL("./index.ts", import.meta.url), "utf8")).toContain("if (!isDevBuild()) return;");
		expect(settings).toContain("{__DEV__ ? <DemoMachineSection onMachinesChanged={reloadMachines} /> : null}");
	});
});
