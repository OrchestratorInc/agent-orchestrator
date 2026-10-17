import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { connectBrowserRuntime, type BrowserRuntimeLinkHandle } from "./browser-runtime-link";
import { resolveBrowserRuntimeToken } from "./browser-runtime-token";
import { HANDSHAKE_CLOSED_BEFORE_ACCEPT } from "../shared/daemon-takeover";

const handles: BrowserRuntimeLinkHandle[] = [];
const servers: net.Server[] = [];

const HELLO_ACK = `${JSON.stringify({ type: "helloAck", version: 2 })}\n`;

/** Mirror the daemon: acknowledge an accepted hello so the link counts as authenticated. */
function acknowledgeHello(socket: net.Socket, message: { type?: string }): void {
	if (message.type === "hello") socket.write(HELLO_ACK);
}

afterEach(async () => {
	handles.splice(0).forEach((handle) => handle.dispose());
	await Promise.all(
		servers.splice(0).map((server) =>
			new Promise<void>((resolve) => {
				server.close(() => resolve());
			}),
		),
	);
});

describe("browser runtime link", () => {
	it("handshakes and correlates a command result", async () => {
		const execute = vi.fn(async () => ({ text: "button Save [ref=e1]" }));
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					const message = JSON.parse(inbound.slice(0, newline));
					inbound = inbound.slice(newline + 1);
					messages.push(message);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		await vi.waitFor(() => expect(messages).toContainEqual({ type: "hello", version: 2 }));

		serverSocket!.write(
			`${JSON.stringify({ type: "command", requestId: "r1", sessionId: "s1", action: "snapshot", args: {} })}\n`,
		);

		await vi.waitFor(() =>
			expect(execute).toHaveBeenCalledWith(
				expect.objectContaining({ requestId: "r1" }),
				expect.any(AbortSignal),
			),
		);
		await vi.waitFor(() =>
			expect(messages).toContainEqual({
				type: "result",
				requestId: "r1",
				ok: true,
				result: { text: "button Save [ref=e1]" },
			}),
		);
	});

	it("reports disconnects so the desktop can offer reconnect", async () => {
		let serverSocket: net.Socket | null = null;
		const states: boolean[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					const message = JSON.parse(inbound.slice(0, newline));
					inbound = inbound.slice(newline + 1);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), onStateChange: (connected) => states.push(connected) },
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		await vi.waitFor(() => expect(states).toContain(true));
		serverSocket!.destroy();
		await vi.waitFor(() => expect(states).toContain(false));
		handle.dispose();
		expect(states).toEqual([true, false]);
	});

	it("returns structured command errors", async () => {
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) {
					if (!line) continue;
					const message = JSON.parse(line);
					messages.push(message);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async () => {
					throw { code: "STALE_REFERENCE", message: "snapshot again" };
				},
			},
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		serverSocket!.write(`${JSON.stringify({ type: "command", requestId: "r2", sessionId: "s1", action: "click" })}\n`);
		await vi.waitFor(() =>
			expect(messages).toContainEqual({
				type: "result",
				requestId: "r2",
				ok: false,
				error: { code: "STALE_REFERENCE", message: "snapshot again" },
			}),
		);
	});

	it("preserves UTF-8 code points split across socket chunks", async () => {
		let serverSocket: net.Socket | null = null;
		const execute = vi.fn(async () => ({}));
		const server = net.createServer((socket) => {
			serverSocket = socket;
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					const message = JSON.parse(inbound.slice(0, newline));
					inbound = inbound.slice(newline + 1);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		const frame = Buffer.from(
			`${JSON.stringify({
				type: "command",
				requestId: "utf8",
				sessionId: "s1",
				action: "fill",
				args: { text: "café 🎉" },
			})}\n`,
			"utf8",
		);
		const emojiStart = frame.indexOf(Buffer.from("🎉", "utf8"));
		serverSocket!.write(frame.subarray(0, emojiStart + 1));
		serverSocket!.write(frame.subarray(emojiStart + 1));

		await vi.waitFor(() =>
			expect(execute).toHaveBeenCalledWith(
				expect.objectContaining({ args: { text: "café 🎉" } }),
				expect.any(AbortSignal),
			),
		);
		serverSocket!.destroy();
	});

	it("queues per session, cancels on server close, and reconnects", async () => {
		let serverSocket: net.Socket | null = null;
		const messages: Array<Record<string, unknown>> = [];
		const executed: string[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) {
					if (!line) continue;
					const message = JSON.parse(line);
					messages.push(message);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async (command, signal) => {
					executed.push(command.requestId);
					if (command.requestId === "blocked") {
						await new Promise<void>((_, reject) => {
							signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
						});
					}
					return { requestId: command.requestId };
				},
			},
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		serverSocket!.write(
			[
				{ type: "command", requestId: "blocked", sessionId: "s1", action: "wait" },
				{ type: "command", requestId: "queued", sessionId: "s1", action: "click" },
				{ type: "command", requestId: "independent", sessionId: "s2", action: "snapshot" },
			]
				.map((message) => JSON.stringify(message))
				.join("\n") + "\n",
		);
		await vi.waitFor(() => expect(executed).toContain("independent"));
		expect(executed).not.toContain("queued");

		serverSocket!.end();
		for (const requestId of ["blocked", "queued"] as const) {
			await vi.waitFor(() =>
				expect(messages).toContainEqual({
					type: "result",
					requestId,
					ok: false,
					error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
				}),
			);
		}
		expect(
			messages.filter(
				(message) =>
					message.type === "result" &&
					message.requestId === "blocked" &&
					message.ok === false &&
					(message.error as { code?: string })?.code === "BROWSER_COMMAND_CANCELED",
			),
		).toHaveLength(1);
		await vi.waitFor(() => expect(handle.connected).toBe(false));
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		expect(executed).not.toContain("queued");
	});

	it("returns structured cancellation errors before disposing the link", async () => {
		let serverSocket: net.Socket | null = null;
		const messages: Array<Record<string, unknown>> = [];
		const executed: string[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) {
					if (!line) continue;
					const message = JSON.parse(line);
					messages.push(message);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async (command, signal) => {
					executed.push(command.requestId);
					if (command.requestId === "blocked") {
						await new Promise<void>((_, reject) => {
							signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
						});
					}
					return { requestId: command.requestId };
				},
			},
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		serverSocket!.write(
			[
				{ type: "command", requestId: "blocked", sessionId: "s1", action: "wait" },
				{ type: "command", requestId: "queued", sessionId: "s1", action: "click" },
			]
				.map((message) => JSON.stringify(message))
				.join("\n") + "\n",
		);
		await vi.waitFor(() => expect(executed).toContain("blocked"));

		handle.dispose();
		for (const requestId of ["blocked", "queued"] as const) {
			await vi.waitFor(() =>
				expect(messages).toContainEqual({
					type: "result",
					requestId,
					ok: false,
					error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
				}),
			);
		}
		expect(
			messages.filter(
				(message) =>
					message.type === "result" &&
					message.requestId === "blocked" &&
					message.ok === false &&
					(message.error as { code?: string })?.code === "BROWSER_COMMAND_CANCELED",
			),
		).toHaveLength(1);
	});

	it("returns structured cancellation errors for daemon-initiated cancel frames", async () => {
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const execute = vi.fn(async (_command, signal) => {
			await new Promise<void>((_, reject) => {
				signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
			});
		});
		const server = net.createServer((socket) => {
			serverSocket = socket;
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) {
					if (!line) continue;
					const message = JSON.parse(line);
					messages.push(message);
					acknowledgeHello(socket, message);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		serverSocket!.write(`${JSON.stringify({ type: "command", requestId: "r3", sessionId: "s1", action: "wait" })}\n`);
		await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
		serverSocket!.write(`${JSON.stringify({ type: "cancel", requestId: "r3" })}\n`);
		await vi.waitFor(() =>
			expect(messages).toContainEqual({
				type: "result",
				requestId: "r3",
				ok: false,
				error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
			}),
		);
	});

	it("backs off instead of hot-looping when the daemon rejects the handshake", async () => {
		let attempts = 0;
		const logs: string[] = [];
		const rejected: string[] = [];
		const server = net.createServer((socket) => {
			attempts += 1;
			socket.on("data", (chunk) => {
				for (const line of chunk.toString("utf8").split("\n")) {
					if (!line.trim()) continue;
					const message = JSON.parse(line) as { type?: string };
					if (message.type !== "hello") continue;
					socket.end(`${JSON.stringify({ type: "helloRejected", reason: "invalid-token" })}\n`);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async () => ({}),
				log: (message) => logs.push(message),
				onHandshakeRejected: (reason) => rejected.push(reason),
			},
		);
		handles.push(handle);

		await vi.waitFor(() => expect(attempts).toBe(1));
		await new Promise((resolve) => setTimeout(resolve, 1_500));

		// A rejected handshake is a hard failure: back off at the cap instead of
		// redialing every 200ms, and never claim the link is connected.
		expect(attempts).toBeLessThanOrEqual(2);
		expect(handle.connected).toBe(false);
		expect(logs).toContain("browser-runtime-link: daemon rejected the runtime handshake: invalid-token");
		expect(logs.filter((line) => line === "browser-runtime-link: connected")).toEqual([]);
		// The reason reaches the caller on every refusal so startup recovery can
		// react even when the log line is suppressed as a repeat.
		expect(rejected).not.toEqual([]);
		expect(new Set(rejected)).toEqual(new Set(["invalid-token"]));
	});

	it("authenticates a link held open by a daemon that predates the hello ack", async () => {
		const server = net.createServer((socket) => {
			socket.on("data", () => undefined);
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute: async () => ({}) });
		handles.push(handle);

		await vi.waitFor(() => expect(handle.connected).toBe(true), { timeout: 5_000 });
	});

	// The reproduced restart path: a kept-alive daemon outlives the Electron
	// process that spawned it, and the next launch adopts it. That daemon holds
	// the token from the previous launch, so a per-launch token could never
	// authenticate again and the browser panel stayed dead for the whole app
	// lifetime. The token is derived from the data dir, so it must survive.
	// A daemon predating helloRejected explains a refusal by hanging up. That is
	// the adoption path for a surviving daemon from before this change: it holds
	// a token we cannot derive, so without reporting the silent close the app
	// would retry forever and never recover.
	it("reports a daemon that hangs up before accepting the hello", async () => {
		let attempts = 0;
		const rejected: string[] = [];
		const server = net.createServer((socket) => {
			attempts += 1;
			socket.on("data", () => socket.destroy());
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), onHandshakeRejected: (reason) => rejected.push(reason) },
		);
		handles.push(handle);

		await vi.waitFor(() => expect(rejected).toContain(HANDSHAKE_CLOSED_BEFORE_ACCEPT));
		expect(handle.connected).toBe(false);
		expect(rejected.every((reason) => reason === HANDSHAKE_CLOSED_BEFORE_ACCEPT)).toBe(true);
		// Backing off still applies to an unexplained hang-up.
		await new Promise((resolve) => setTimeout(resolve, 1_500));
		expect(attempts).toBeLessThanOrEqual(2);
	});

	it("does not report a refusal for a link that had already connected", async () => {
		const rejected: string[] = [];
		const server = net.createServer((socket) => {
			socket.on("data", () => acknowledgeHello(socket, { type: "hello" }));
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), onHandshakeRejected: (reason) => rejected.push(reason) },
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		// A runtime that later disconnects is not a handshake refusal; startup
		// recovery must not retire the daemon for an ordinary drop.
		handle.dispose();
		await new Promise((resolve) => setTimeout(resolve, 100));
		expect(rejected).toEqual([]);
	});

	it("authenticates a restarted app against a daemon that survived it", async () => {
		const dataDir = mkdtempSync(path.join(os.tmpdir(), "ao-runtime-restart-"));
		// Stands in for the broker: authenticate against the token the daemon was
		// handed at spawn and hold the connection open across the "restart".
		const daemonToken = resolveBrowserRuntimeToken(dataDir);
		const rejections: string[] = [];
		const server = net.createServer((socket) => {
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					const message = JSON.parse(inbound.slice(0, newline)) as { type?: string; token?: string };
					inbound = inbound.slice(newline + 1);
					if (message.type !== "hello") continue;
					if (message.token !== daemonToken) {
						rejections.push(String(message.token));
						socket.end(`${JSON.stringify({ type: "helloRejected", reason: "invalid-token" })}\n`);
						continue;
					}
					socket.write(HELLO_ACK);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;

		const firstLaunch = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), token: resolveBrowserRuntimeToken(dataDir) },
		);
		handles.push(firstLaunch);
		await vi.waitFor(() => expect(firstLaunch.connected).toBe(true));

		// The app quits; the daemon (and the token it holds) survives.
		firstLaunch.dispose();
		handles.splice(handles.indexOf(firstLaunch), 1);

		// A fresh launch derives its token from the same data dir and adopts it.
		const secondLaunch = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), token: resolveBrowserRuntimeToken(dataDir) },
		);
		handles.push(secondLaunch);

		await vi.waitFor(() => expect(secondLaunch.connected).toBe(true));
		expect(rejections).toEqual([]);

		// A launch on a different data dir still cannot claim this daemon.
		const foreignLaunch = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{ execute: async () => ({}), token: randomBytes(32).toString("base64url") },
		);
		handles.push(foreignLaunch);
		await vi.waitFor(() => expect(rejections).toHaveLength(1));
		expect(foreignLaunch.connected).toBe(false);

		rmSync(dataDir, { recursive: true, force: true });
	});
});
