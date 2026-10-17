import type { AddressInfo, Server } from "node:net";

// What the two loopback proxies that show an agent browser (remote-proxy.ts
// for a remote host's, cloud-agent-proxy.ts for an AO Cloud sandbox's) do
// alike. Their sockets differ: one tunnels to the host's daemon, the other
// opens a WebSocket to the control plane with the person's bearer.

/** Connection-specific headers: meaningless past one hop, and an HTTP/2 response refuses them. */
export const HOP_BY_HOP_HEADERS = ["connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade", "te", "http2-settings"];

/**
 * Listens on loopback at the port a key picks while it is free, else on any
 * port, and returns the port. The port is part of a preview's origin: the
 * same one keeps the preview's storage across restarts.
 */
export async function listenOnStablePort(server: Server, mac: Buffer): Promise<number> {
	const listened = (port: number) => new Promise<boolean>((resolve) => {
		const busy = () => resolve(false);
		server.once("error", busy);
		server.listen(port, "127.0.0.1", () => {
			server.off("error", busy);
			resolve(true);
		});
	});
	if (!(await listened(41000 + (mac.readUInt16BE(0) % 8000))) && !(await listened(0))) {
		throw new Error("could not listen on loopback");
	}
	return (server.address() as AddressInfo).port;
}

/** The agent browser's viewer; embed: the Browser panel's own toolbar drives it (remote-tab.ts). */
export const agentViewerUrl = (host: string, port: number): string => `https://${host}:${port}/v/cdp?embed=1&sq=70&ss=1`;

/**
 * Whether an agent browser's stats answer means an agent tab to show: it
 * answered, and its browser runs. A cloud sandbox's browser starts when its
 * agent first uses it and answers {"running":false} until then; showing the
 * viewer would start it.
 */
export async function agentBrowserRunning(response: Response | null): Promise<boolean> {
	if (!response?.ok) {
		await response?.body?.cancel().catch(() => undefined);
		return false;
	}
	const stats: unknown = await response.json().catch(() => undefined);
	return (stats as { running?: unknown } | null | undefined)?.running !== false;
}
