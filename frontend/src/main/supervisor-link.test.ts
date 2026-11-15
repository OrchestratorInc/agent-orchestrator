// @vitest-environment node
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { describe, it, expect, afterEach } from "vitest";
import { connectSupervisor, type SupervisorLinkHandle } from "./supervisor-link";

function withTimeout<T>(promise: Promise<T>, timeoutMs: number, label: string): Promise<T> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => reject(new Error(`Timeout: ${label}`)), timeoutMs);
		promise.then(
			(v) => {
				clearTimeout(timer);
				resolve(v);
			},
			(e) => {
				clearTimeout(timer);
				reject(e);
			},
		);
	});
}

function tmpSocketPath(): string {
	return path.join(os.tmpdir(), `ao-svlink-test-${process.pid}-${Date.now()}.sock`);
}

function nextConnection(server: net.Server): Promise<net.Socket> {
	return new Promise((resolve) => {
		server.once("connection", resolve);
	});
}

describe("supervisor-link", () => {
	const handles: SupervisorLinkHandle[] = [];
	const servers: net.Server[] = [];

	afterEach(async () => {
		for (const h of handles.splice(0)) h.dispose();
		await Promise.all(
			servers.splice(0).map(
				(s) =>
					new Promise<void>((resolve) => {
						s.close(() => resolve());
					}),
			),
		);
	});

	it("retries until connected: connects after server is started later", async () => {
		const addr = tmpSocketPath();

		const link = connectSupervisor(addr, { log: () => undefined });
		handles.push(link);

		await new Promise<void>((r) => setTimeout(r, 400));

		const server = net.createServer();
		servers.push(server);
		const connectionPromise = nextConnection(server);
		await new Promise<void>((resolve, reject) => {
			server.listen(addr, () => resolve());
			server.once("error", reject);
		});

		const conn = await withTimeout(
			connectionPromise,
			5_000,
			"retry-until-connected: server did not receive connection",
		);
		expect(conn).toBeTruthy();
		conn.destroy();
	});

	it("reconnects on drop: re-establishes after the accepted socket is closed", async () => {
		const addr = tmpSocketPath();

		const server = net.createServer();
		servers.push(server);

		let connectionCount = 0;
		const secondConnection = new Promise<net.Socket>((resolve) => {
			let first = true;
			server.on("connection", (sock) => {
				connectionCount++;
				if (first) {
					first = false;
					setTimeout(() => sock.destroy(), 50);
				} else {
					resolve(sock);
				}
			});
		});

		await new Promise<void>((resolve, reject) => {
			server.listen(addr, () => resolve());
			server.once("error", reject);
		});

		const link = connectSupervisor(addr, { log: () => undefined });
		handles.push(link);

		const reconn = await withTimeout(secondConnection, 6_000, "reconnect-on-drop: second connection never arrived");
		expect(connectionCount).toBeGreaterThanOrEqual(2);
		reconn.destroy();
	});

	it("connected flag: true after connect, false after server closes connection", async () => {
		const addr = tmpSocketPath();

		const server = net.createServer();
		servers.push(server);
		const connectionPromise = nextConnection(server);
		await new Promise<void>((resolve, reject) => {
			server.listen(addr, () => resolve());
			server.once("error", reject);
		});

		const link = connectSupervisor(addr, { log: () => undefined });
		handles.push(link);

		const conn = await withTimeout(connectionPromise, 3_000, "connected-flag: server did not receive connection");

		await withTimeout(
			new Promise<void>((resolve) => {
				const check = () => {
					if (link.connected) {
						resolve();
						return;
					}
					setTimeout(check, 20);
				};
				check();
			}),
			1_000,
			"connected-flag: handle.connected never became true",
		);
		expect(link.connected).toBe(true);

		conn.destroy();

		await withTimeout(
			new Promise<void>((resolve) => {
				const check = () => {
					if (!link.connected) {
						resolve();
						return;
					}
					setTimeout(check, 20);
				};
				check();
			}),
			3_000,
			"connected-flag: handle.connected never became false after server closed",
		);
		expect(link.connected).toBe(false);

		link.dispose();
	});

	it("dispose stops reconnect: no connection arrives after dispose", async () => {
		const addr = tmpSocketPath();

		const link = connectSupervisor(addr, { log: () => undefined });

		link.dispose();

		const server = net.createServer();
		servers.push(server);
		let receivedConnection = false;
		server.on("connection", () => {
			receivedConnection = true;
		});
		await new Promise<void>((resolve, reject) => {
			server.listen(addr, () => resolve());
			server.once("error", reject);
		});

		await new Promise<void>((r) => setTimeout(r, 600));

		expect(receivedConnection).toBe(false);
	});
});
