// @vitest-environment node
import { createServer, type Server } from "node:http";
import type { ListenOptions } from "node:net";
import { QueryClient } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { getApiBaseUrl, setApiBaseUrl } from "../lib/api-client";
import { sessionArtifactFileQueryOptions } from "./useSessionWorkspaceFiles";

vi.mock("../lib/bridge", () => ({ aoBridge: {} }));

it.each(["image.png", "video.webm", "audio.mp3"])("loads %s metadata from a GET-only artifact route and cancels the body", async (path) => {
	const requests: { method: string | undefined; url: string | undefined }[] = [];
	let cancelled = false;
	const modified = "Wed, 14 Oct 2026 12:00:00 GMT";
	const daemon = createServer((request, response) => {
		requests.push({ method: request.method, url: request.url });
		if (request.method !== "GET") {
			response.writeHead(405).end();
			return;
		}
		response.on("close", () => { cancelled = true; });
		response.writeHead(200, { "Content-Length": "104857600", "Last-Modified": modified });
		// Leave the body open: metadata loading must finish without downloading it.
		response.flushHeaders();
	});
	const previousBase = getApiBaseUrl();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	try {
		await new Promise<void>((resolve) => daemon.listen(0, "127.0.0.1", resolve));
		const address = daemon.address();
		if (!address || typeof address === "string") throw new Error("Expected a TCP listener");
		setApiBaseUrl(`http://127.0.0.1:${address.port}`);
		const rawUrl = `http://ao-preview-artifact.session.localhost:${address.port}/${path}?raw=true`;

		const detail = await client.fetchQuery(sessionArtifactFileQueryOptions("session-1", path, rawUrl));

		expect(detail).toMatchObject({ binary: true, content: "", editable: false, size: 104857600, mediaVersion: modified });
		expect(requests).toEqual([{ method: "GET", url: `/api/v1/sessions/session-1/preview/files/${path}?source=artifact&raw=true` }]);
		await vi.waitFor(() => expect(cancelled).toBe(true));
	} finally {
		client.clear();
		setApiBaseUrl(previousBase || null);
		await new Promise<void>((resolve, reject) => {
			daemon.close((error) => error ? reject(error) : resolve());
			daemon.closeAllConnections();
		});
	}
});

it.for([
	{ simulateCollision: false, artifactPath: "sub/report (1).md", escapedPath: "sub/report%20(1).md" },
	{ simulateCollision: true, artifactPath: "sub/a+b.md", escapedPath: "sub/a%2Bb.md" },
])("reads $artifactPath from IPv4 AO while another app owns the same port on IPv6 (IPv4 collision: $simulateCollision)", async ({ simulateCollision, artifactPath, escapedPath }, context) => {
	const requests: string[] = [];
	const daemon = createServer((request, response) => {
		requests.push(request.url ?? "");
		response.end("# The AO artifact");
	});
	let competingRequests = 0;
	const competitor = createServer((_request, response) => {
		competingRequests++;
		response.writeHead(404);
		response.end("Unrelated application");
	});
	const daemonListen = vi.spyOn(daemon, "listen");
	if (simulateCollision) {
		daemonListen.mockImplementationOnce(() => {
			queueMicrotask(() => daemon.emit("error", Object.assign(new Error("Port already occupied on IPv4"), { code: "EADDRINUSE" })));
			return daemon;
		});
	}
	const close = (server: Server) => new Promise<void>((resolve, reject) => {
		if (!server.listening) return resolve();
		server.close((error) => error ? reject(error) : resolve());
		server.closeAllConnections();
	});
	const listen = (server: Server, options: ListenOptions) => new Promise<void>((resolve, reject) => {
		const onError = (error: Error) => { server.off("listening", onListening); reject(error); };
		const onListening = () => { server.off("error", onError); resolve(); };
		server.once("error", onError);
		server.once("listening", onListening);
		server.listen(options);
	});
	const nativeFetch = globalThis.fetch;
	const previousBase = getApiBaseUrl();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	try {
		let port = 0;
		for (let attempt = 0; attempt < 10; attempt++) {
			try {
				await listen(competitor, { host: "::1", port: 0, ipv6Only: true });
			} catch (error) {
				if (["EAFNOSUPPORT", "EADDRNOTAVAIL"].includes((error as NodeJS.ErrnoException).code ?? "")) {
					context.skip("IPv6 loopback is unavailable");
					return;
				}
				throw error;
			}
			const address = competitor.address();
			if (!address || typeof address === "string") throw new Error("Expected a TCP listener");
			port = address.port;
			try {
				await listen(daemon, { host: "127.0.0.1", port });
				break;
			} catch (error) {
				// IPv6's ephemeral port can still be occupied on IPv4. Release
				// our reservation and try a fresh port, keeping real errors fatal.
				if ((error as NodeJS.ErrnoException).code !== "EADDRINUSE" || attempt === 9) throw error;
				await close(competitor);
			}
		}
		expect(daemonListen.mock.calls.length).toBeGreaterThanOrEqual(simulateCollision ? 2 : 1);
		const hostname = "ao-preview-artifact.session.localhost";
		const rawUrl = `http://${hostname}:${port}/${artifactPath}?raw=true`;
		// Reproduce Chromium choosing ::1 deterministically, without relying on
		// the test host's DNS support or address ordering for *.localhost.
		const fetch = vi.fn((input: string | URL | Request, init?: RequestInit) =>
			nativeFetch(String(input).replace(`http://${hostname}:`, "http://[::1]:"), init));
		vi.stubGlobal("fetch", fetch);
		expect((await fetch(rawUrl)).status).toBe(404);
		expect(competingRequests).toBe(1);
		fetch.mockClear();
		setApiBaseUrl(`http://127.0.0.1:${port}`);

		const detail = await client.fetchQuery(sessionArtifactFileQueryOptions("session-1", artifactPath, rawUrl));

		expect(detail.content).toBe("# The AO artifact");
		expect(detail.editable).toBe(true);
		expect(detail.fileFingerprint).toMatch(/^[a-f0-9]{64}$/);
		expect(requests).toEqual([`/api/v1/sessions/session-1/preview/files/${escapedPath}?source=artifact&raw=true`]);
		expect(competingRequests).toBe(1);
		expect(fetch).toHaveBeenCalledExactlyOnceWith(`http://127.0.0.1:${port}${requests[0]}`);

		// A stopped/restarting daemon must not fall back to the ambiguous host.
		client.clear();
		fetch.mockClear();
		setApiBaseUrl(null);
		await expect(client.fetchQuery(sessionArtifactFileQueryOptions("session-1", artifactPath, rawUrl))).rejects.toThrow("Unable to load artifact");
		expect(fetch).not.toHaveBeenCalled();
	} finally {
		daemonListen.mockRestore();
		client.clear();
		setApiBaseUrl(previousBase || null);
		vi.unstubAllGlobals();
		await Promise.all([close(daemon), close(competitor)]);
	}
});
