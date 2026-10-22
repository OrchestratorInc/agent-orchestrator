import net from "node:net";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserRuntimeState } from "./browser-view-host";
import { connectBrowserRuntime, type BrowserRuntimeLinkHandle } from "./browser-runtime-link";

const handles: BrowserRuntimeLinkHandle[] = [];
const servers: net.Server[] = [];

const CONNECTING: BrowserRuntimeState = { connected: false, connecting: true, rejected: false };
const CONNECTED: BrowserRuntimeState = { connected: true, connecting: false, rejected: false };
const DISCONNECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: false };
const REJECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: true };

afterEach(async () => {
	vi.useRealTimers();
	handles.splice(0).forEach((handle) => handle.dispose());
	await Promise.all(
		servers.splice(0).map((server) =>
			new Promise<void>((resolve) => {
				server.close(() => resolve());
			}),
		),
	);
});

// Answer the hello the way the daemon's broker accepts a valid token.
function ackHello(socket: net.Socket) {
	socket.once("data", () => socket.write(`${JSON.stringify({ type: "hello-ok" })}\n`));
}

async function listen(server: net.Server) {
	servers.push(server);
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const address = server.address() as net.AddressInfo;
	return { host: address.address, port: address.port };
}

// Waits on socket I/O without advancing fake timers, so a test controls every
// backoff step itself. setImmediate and performance.now() are never faked here.
async function until(check: () => boolean, label: string) {
	const deadline = performance.now() + 5_000;
	while (!check()) {
		if (performance.now() > deadline) throw new Error(`timed out waiting for ${label}`);
		await new Promise((resolve) => setImmediate(resolve));
	}
}

function useBackoffTimers() {
	vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
}

describe("browser runtime link", () => {
	it("handshakes and correlates a command result", async () => {
		const execute = vi.fn(async () => ({ text: "button Save [ref=e1]" }));
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			ackHello(socket);
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					messages.push(JSON.parse(inbound.slice(0, newline)));
					inbound = inbound.slice(newline + 1);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));
		await vi.waitFor(() => expect(messages).toContainEqual({ type: "hello", version: 3 }));

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

	it("reports connecting until hello-ok, and connecting again while an accepted link redials", async () => {
		useBackoffTimers();
		let serverSocket: net.Socket | null = null;
		let connections = 0;
		const states: BrowserRuntimeState[] = [];
		const address = await listen(
			net.createServer((socket) => {
				connections += 1;
				serverSocket = socket;
				ackHello(socket);
			}),
		);
		const handle = connectBrowserRuntime(address, { execute: async () => ({}), onStateChange: (state) => states.push(state) });
		handles.push(handle);
		await until(() => handle.state.connected, "hello-ok");

		serverSocket!.destroy();
		await until(() => handle.state.connecting, "the drop");
		await vi.advanceTimersByTimeAsync(200);
		await until(() => handle.state.connected && connections === 2, "the redial");

		// The panel never sees a disconnect: the link was always connected or about to be.
		expect(states).toEqual([CONNECTING, CONNECTED, CONNECTING, CONNECTED]);
	});

	it("reports a disconnect once the redial after a drop fails too", async () => {
		useBackoffTimers();
		let serverSocket: net.Socket | null = null;
		const states: BrowserRuntimeState[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			ackHello(socket);
		});
		const address = await listen(server);
		const handle = connectBrowserRuntime(address, { execute: async () => ({}), onStateChange: (state) => states.push(state) });
		handles.push(handle);
		await until(() => handle.state.connected, "hello-ok");

		servers.splice(servers.indexOf(server), 1);
		await new Promise<void>((resolve) => {
			server.close(() => resolve());
			serverSocket!.destroy();
		});
		await until(() => handle.state.connecting, "the drop");
		await vi.advanceTimersByTimeAsync(200);
		await until(() => !handle.state.connecting, "the failed redial");

		expect(states).toEqual([CONNECTING, CONNECTED, CONNECTING, DISCONNECTED]);
	});

	it("answers broker pings out of band", async () => {
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			ackHello(socket);
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
			});
		});
		const address = await listen(server);
		const handle = connectBrowserRuntime(address, { execute: async () => ({}) });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));

		serverSocket!.write(`${JSON.stringify({ type: "ping" })}\n`);
		await vi.waitFor(() => expect(messages).toContainEqual({ type: "pong" }));
	});

	it("returns structured command errors", async () => {
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			ackHello(socket);
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
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
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));
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
			ackHello(socket);
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));

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
			ackHello(socket);
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
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
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));

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
		await vi.waitFor(() => expect(handle.state.connected).toBe(false));
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));
		expect(executed).not.toContain("queued");
	});

	it("returns structured cancellation errors before disposing the link", async () => {
		let serverSocket: net.Socket | null = null;
		const messages: Array<Record<string, unknown>> = [];
		const executed: string[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			ackHello(socket);
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
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
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));

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
			ackHello(socket);
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.state.connected).toBe(true));

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

	it("stops on hello-rejected, reports it once, and never redials with the refused token (#6466)", async () => {
		useBackoffTimers();
		let connections = 0;
		const address = await listen(
			net.createServer((socket) => {
				connections += 1;
				// The broker's answer to a stale token: an explicit refusal, then close.
				socket.once("data", () => {
					socket.end(
						`${JSON.stringify({
							type: "hello-rejected",
							error: { code: "BROWSER_RUNTIME_TOKEN_REJECTED", message: "token mismatch" },
						})}\n`,
					);
				});
			}),
		);
		const states: BrowserRuntimeState[] = [];
		const logs: string[] = [];
		const handle = connectBrowserRuntime(address, {
			execute: async () => ({}),
			token: "stale-token",
			log: (message) => logs.push(message),
			onStateChange: (state) => states.push(state),
		});
		handles.push(handle);
		await until(() => handle.state.rejected, "hello-rejected");
		await vi.advanceTimersByTimeAsync(60_000);
		await new Promise((resolve) => setImmediate(resolve));

		// Before the fix every refused attempt reported connected and redialed every 200 ms.
		expect(connections).toBe(1);
		// The daemon's reason reaches the Browser panel, which names a cause only for
		// a code that identifies one.
		expect(states).toEqual([CONNECTING, { ...REJECTED, rejectionCode: "BROWSER_RUNTIME_TOKEN_REJECTED" }]);
		expect(logs.filter((line) => line.includes("connected"))).toEqual([]);
		expect(logs.filter((line) => line.includes("refused"))).toEqual([
			expect.stringContaining("BROWSER_RUNTIME_TOKEN_REJECTED: token mismatch"),
		]);
	});

	it("backs off without claiming a refusal when the link closes before the daemon answers", async () => {
		useBackoffTimers();
		let connections = 0;
		// A transport failure mid-handshake, or an older daemon closing on a hello it refused.
		const address = await listen(
			net.createServer((socket) => {
				connections += 1;
				socket.once("data", () => socket.destroy());
			}),
		);
		const states: BrowserRuntimeState[] = [];
		const logs: string[] = [];
		const handle = connectBrowserRuntime(address, {
			execute: async () => ({}),
			log: (message) => logs.push(message),
			onStateChange: (state) => states.push(state),
		});
		handles.push(handle);
		// A close has been handled once its redial is scheduled.
		await until(() => connections === 1 && vi.getTimerCount() === 1, "the first close");

		// Each close doubles the wait to the 2 s cap; nothing resets it.
		for (const [delay, attempt] of [[200, 2], [400, 3], [800, 4], [1_600, 5], [2_000, 6], [2_000, 7]] as const) {
			await vi.advanceTimersByTimeAsync(delay - 1);
			expect(connections).toBe(attempt - 1);
			await vi.advanceTimersByTimeAsync(1);
			await until(() => connections === attempt && vi.getTimerCount() === 1, `attempt ${attempt}`);
		}
		expect(states).toEqual([CONNECTING, DISCONNECTED]);
		expect(logs.filter((line) => line.includes("closed before the daemon answered"))).toHaveLength(1);
	});

	it("does not report a refusal while the daemon is unreachable", async () => {
		const closed = net.createServer();
		const address = await listen(closed);
		servers.splice(servers.indexOf(closed), 1);
		await new Promise<void>((resolve) => closed.close(() => resolve()));
		const states: BrowserRuntimeState[] = [];
		const handle = connectBrowserRuntime(address, { execute: async () => ({}), onStateChange: (state) => states.push(state) });
		handles.push(handle);

		await vi.waitFor(() => expect(states).toEqual([CONNECTING, DISCONNECTED]));
		expect(handle.state.rejected).toBe(false);
	});

	it("accepts an older daemon, which never sends hello-ok, on its first command", async () => {
		const execute = vi.fn(async () => ({}));
		const address = await listen(
			net.createServer((socket) => {
				socket.once("data", () =>
					socket.write(`${JSON.stringify({ type: "command", requestId: "r1", sessionId: "s1", action: "snapshot" })}\n`),
				);
			}),
		);
		const handle = connectBrowserRuntime(address, { execute });
		handles.push(handle);

		await vi.waitFor(() => expect(handle.state).toEqual(CONNECTED));
		await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
	});
});
