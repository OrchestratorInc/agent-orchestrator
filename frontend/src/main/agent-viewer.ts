import type { AddressInfo, Server } from "node:net";

export const HOP_BY_HOP_HEADERS = ["connection", "keep-alive", "proxy-connection", "transfer-encoding", "upgrade", "te", "http2-settings"];

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

export const agentViewerUrl = (host: string, port: number): string => `https://${host}:${port}/v/cdp?embed=1&sq=70&ss=1`;

export async function agentBrowserRunning(response: Response | null): Promise<boolean> {
	if (!response?.ok) {
		await response?.body?.cancel().catch(() => undefined);
		return false;
	}
	const stats: unknown = await response.json().catch(() => undefined);
	return (stats as { running?: unknown } | null | undefined)?.running !== false;
}
