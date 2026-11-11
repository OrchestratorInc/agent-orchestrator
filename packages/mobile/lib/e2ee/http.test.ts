import { sha1 } from "@noble/hashes/legacy.js";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("expo-crypto", () => ({ getRandomBytes: (count: number) => new Uint8Array(count).fill(7) }));

type FakeChannel = {
	written: Uint8Array[];
	closed: boolean;
	onData: ((bytes: Uint8Array) => void) | null;
	onClose: ((error?: Error) => void) | null;
	write(bytes: Uint8Array): void;
	close(): void;
	send(text: string | Uint8Array): void;
	text(): string;
};

const opened: FakeChannel[] = [];

function fakeChannel(): FakeChannel {
	const channel: FakeChannel = {
		written: [],
		closed: false,
		onData: null,
		onClose: null,
		write(bytes) {
			channel.written.push(bytes.slice());
		},
		close() {
			if (channel.closed) return;
			channel.closed = true;
			channel.onClose?.();
		},
		send(data) {
			channel.onData?.(typeof data === "string" ? new TextEncoder().encode(data) : data);
		},
		text: () => channel.written.map((b) => new TextDecoder().decode(b)).join(""),
	};
	return channel;
}

vi.mock("./channel", () => ({
	openChannel: async () => {
		const channel = fakeChannel();
		opened.push(channel);
		return channel;
	},
}));

const { secureFetch, closeIdleChannels } = await import("./http");
const { SecureWebSocket } = await import("./socket");

const target = { url: "ws://host/api/v1/e2ee", hostKey: new Uint8Array(32), authority: "host:3011" };

beforeEach(() => {
	closeIdleChannels();
	opened.length = 0;
});

describe("secureFetch", () => {
	it("reads a sized response and sends the next request on the same channel", async () => {
		const first = secureFetch(target, { method: "POST", path: "/api/v1/x", headers: { Authorization: "Bearer t" }, body: "{}" });
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		expect(opened[0].text()).toBe("POST /api/v1/x HTTP/1.1\r\nHost: host:3011\r\nAuthorization: Bearer t\r\nContent-Length: 2\r\n\r\n{}");
		opened[0].send("HTTP/1.1 201 Created\r\nContent-Length: 5\r\n\r\nhello");
		const res = await first;
		expect([res.status, res.statusText, res.ok]).toEqual([201, "Created", true]);
		expect(await res.text()).toBe("hello");

		const second = secureFetch(target, { method: "GET", path: "/b" });
		await vi.waitFor(() => expect(opened[0].written.length).toBe(2));
		opened[0].send("HTTP/1.1 404 Not Found\r\nContent-Length: 2\r\n\r\n{}");
		expect((await second).status).toBe(404);
		expect(opened).toHaveLength(1);
	});

	it("streams a chunked body that arrives split across records", async () => {
		const pending = secureFetch(target, { method: "GET", path: "/events" });
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		opened[0].send("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nX-AO-Event-After: 4\r\n\r\n5\r\nhel");
		const res = await pending;
		expect(res.headers.get("x-ao-event-after")).toBe("4");
		const reader = res.body.getReader();
		expect(new TextDecoder().decode((await reader.read()).value)).toBe("hel");
		opened[0].send("lo\r\n3;x=1\r\nabc\r\n0\r\n\r\n");
		expect(new TextDecoder().decode((await reader.read()).value)).toBe("lo");
		expect(new TextDecoder().decode((await reader.read()).value)).toBe("abc");
		expect((await reader.read()).done).toBe(true);
	});

	it("sends a GET again when a pooled channel closed while idle", async () => {
		const first = secureFetch(target, { method: "GET", path: "/a" });
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		opened[0].send("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
		await (await first).text();

		const second = secureFetch(target, { method: "GET", path: "/b" });
		await vi.waitFor(() => expect(opened[0].written.length).toBe(2));
		opened[0].close();
		await vi.waitFor(() => expect(opened[1]?.written.length).toBe(1));
		opened[1].send("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok");
		expect(await (await second).text()).toBe("ok");
	});

	it("does not send a POST again after the channel closed", async () => {
		const first = secureFetch(target, { method: "GET", path: "/a" });
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		opened[0].send("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n");
		await (await first).text();
		const post = secureFetch(target, { method: "POST", path: "/kill", body: "" });
		await vi.waitFor(() => expect(opened[0].written.length).toBe(2));
		opened[0].close();
		await expect(post).rejects.toThrow();
		expect(opened).toHaveLength(1);
	});

	it("refuses a header value with a line break", async () => {
		await expect(secureFetch(target, { method: "GET", path: "/a", headers: { X: "a\r\nB: c" } })).rejects.toThrow("line break");
	});
});

describe("SecureWebSocket", () => {
	it("upgrades, exchanges text, answers a ping and reports the close", async () => {
		const events: string[] = [];
		const ws = new SecureWebSocket(target, "/mux", { Origin: "http://localhost" });
		ws.onopen = () => events.push("open");
		ws.onmessage = (e) => events.push(`message ${String(e.data)}`);
		ws.onclose = (e) => events.push(`close ${e.code}`);
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		const key = /Sec-WebSocket-Key: (\S+)/.exec(opened[0].text())?.[1] ?? "";
		const accept = btoa(String.fromCharCode(...sha1(new TextEncoder().encode(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`))));
		opened[0].send(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
		await vi.waitFor(() => expect(events).toContain("open"));

		ws.send("hi");
		const sent = opened[0].written[1];
		expect([sent[0], sent[1]]).toEqual([0x81, 0x80 | 2]);
		expect(Array.from(sent.subarray(6), (b, i) => b ^ sent[2 + (i & 3)])).toEqual([104, 105]);

		opened[0].send(Uint8Array.of(0x01, 2, 104, 101));
		opened[0].send(Uint8Array.of(0x89, 1, 9));
		opened[0].send(Uint8Array.of(0x80, 3, 108, 108, 111));
		await vi.waitFor(() => expect(events).toContain("message hello"));
		expect(opened[0].written[2][0]).toBe(0x8a);

		opened[0].send(Uint8Array.of(0x88, 2, 0x03, 0xe8));
		await vi.waitFor(() => expect(events).toContain("close 1000"));
		expect(ws.readyState).toBe(SecureWebSocket.CLOSED);
		expect(opened[0].closed).toBe(true);
	});

	it("fails when the machine refuses the upgrade", async () => {
		const events: string[] = [];
		const ws = new SecureWebSocket(target, "/mux", {});
		ws.onerror = () => events.push("error");
		ws.onclose = (e) => events.push(`close ${e.code}`);
		await vi.waitFor(() => expect(opened[0]?.written.length).toBe(1));
		opened[0].send("HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n");
		await vi.waitFor(() => expect(events).toEqual(["error", "close 1006"]));
	});
});
