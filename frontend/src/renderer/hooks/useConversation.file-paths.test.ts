import { afterEach, expect, it, vi } from "vitest";
import { connectHost, disconnectHost } from "../lib/host-clients";
import { fetchWorkspaceFilePaths } from "./useConversation";

const { remoteConnect } = vi.hoisted(() => ({ remoteConnect: vi.fn() }));
vi.mock("../lib/bridge", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/bridge")>();
	return { ...actual, aoBridge: { ...actual.aoBridge, remotes: { connect: remoteConnect, disconnect: vi.fn() } } };
});
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));

afterEach(async () => {
	await disconnectHost("box-a");
	vi.unstubAllGlobals();
});

async function hostAnswering(hasPathsRoute: boolean) {
	remoteConnect.mockImplementation(async (url: string) => ({ hostId: "box-a", label: "Box A", url, base: "http://127.0.0.1:4000" }));
	const requested: string[] = [];
	vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
		const url = new URL(input instanceof Request ? input.url : String(input));
		requested.push(url.pathname);
		if (hasPathsRoute && url.pathname.endsWith("/workspace/paths")) {
			return Response.json({ sessionId: "s-1", paths: ["README.md", "src/app.ts"], truncated: false });
		}
		if (url.pathname.endsWith("/workspace/files")) {
			return Response.json({
				sessionId: "s-1",
				files: [{ path: "README.md", status: "modified" }, { path: "gone.ts", status: "deleted" }],
				truncated: true,
			});
		}
		return Response.json({ code: "ROUTE_NOT_FOUND", error: "not_found", message: "GET has no handler" }, { status: 404 });
	}));
	await connectHost("http://box-a:3001");
	return requested;
}

it("reads paths from the paths route", async () => {
	const requested = await hostAnswering(true);

	const { data, error } = await fetchWorkspaceFilePaths("s-1", "box-a");

	expect(error).toBeUndefined();
	expect(data).toMatchObject({ paths: ["README.md", "src/app.ts"], truncated: false });
	expect(requested.some((path) => path.endsWith("/workspace/files"))).toBe(false);
});

it("lists paths through the file list on a host whose daemon has no paths route", async () => {
	const requested = await hostAnswering(false);

	const { data, error } = await fetchWorkspaceFilePaths("s-1", "box-a");
	await fetchWorkspaceFilePaths("s-1", "box-a");

	expect(error).toBeUndefined();
	expect(data).toMatchObject({ paths: ["README.md"], truncated: true });
	expect(requested.filter((path) => path.endsWith("/workspace/paths"))).toHaveLength(1);
});
