import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtifactRow } from "../lib/artifacts";
import { ArtifactThumbnail } from "./ArtifactThumbnail";

const mocks = vi.hoisted(() => ({
	access: vi.fn(),
	fetch: vi.fn(),
	ready: true,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { createSessionArtifactAccess: mocks.access },
		ready: mocks.ready,
		baseUrl: "https://cp.test",
	}),
}));
vi.mock("../lib/cloud-cp/renderer-client", () => ({ cloudCpFetch: mocks.fetch }));

const ROOT = "https://cp.test/api/cloud/v1/artifact-files/token/";
const VERSION = "2026-10-28T10:00:00Z";

function artifact(path: string, kind: ArtifactRow["kind"] = "file"): ArtifactRow {
	return {
		path,
		name: path,
		kind,
		updatedAt: VERSION,
		sessionId: "session-1",
		sessionTitle: "Cloud output",
		cloud: { orgId: "org-1" },
		generating: false,
	};
}

function renderThumbnails(rows: ArtifactRow[]) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrap = (current: ArtifactRow[]) => (
		<QueryClientProvider client={client}>
			{current.map((row) => <ArtifactThumbnail key={row.path} row={row} />)}
		</QueryClientProvider>
	);
	const view = render(wrap(rows));
	return { ...view, client, rerender: (current: ArtifactRow[]) => view.rerender(wrap(current)) };
}

describe("cloud artifact thumbnails", () => {
	beforeEach(() => {
		mocks.ready = true;
		mocks.access.mockReset().mockResolvedValue({ filesPrefix: "/api/cloud/v1/artifact-files/token/", expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() });
		mocks.fetch.mockReset().mockImplementation(async () => new Response("# Cloud preview\n\nA generated report."));
		vi.stubGlobal("IntersectionObserver", class {
			constructor(private callback: (entries: { isIntersecting: boolean }[]) => void) {}
			observe() { this.callback([{ isIntersecting: true }]); }
			disconnect() {}
		});
		vi.stubGlobal("ResizeObserver", class {
			observe() {}
			unobserve() {}
			disconnect() {}
		});
	});
	afterEach(() => vi.unstubAllGlobals());

	it("renders metadata-only Markdown and images with one shared session access request", async () => {
		const { container, client } = renderThumbnails([artifact("notes.md", "markdown"), artifact("logo.svg"), artifact("cover.png")]);

		await screen.findByText("Cloud preview");
		await waitFor(() => expect(container.querySelectorAll("img")).toHaveLength(2));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(mocks.access).toHaveBeenCalledTimes(1);
		expect(mocks.access).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}notes.md?v=${encodeURIComponent(VERSION)}`, { method: "GET" });
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}cover.png?v=${encodeURIComponent(VERSION)}`, { method: "HEAD" });
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}logo.svg?v=${encodeURIComponent(VERSION)}`, { method: "HEAD" });
		expect(mocks.fetch).not.toHaveBeenCalledWith(`${ROOT}logo.svg?v=${encodeURIComponent(VERSION)}`, { method: "GET" });
		expect(container.querySelector('img[src*="logo.svg"]')).not.toBeNull();
		expect(container.querySelector('img[src*="cover.png"]')).not.toBeNull();
		expect(screen.queryByTitle("No preview available")).not.toBeInTheDocument();
	});

	it("resolves Markdown images under the signed session root", async () => {
		mocks.fetch.mockResolvedValue(new Response("# Report\n\n![Chart](assets/chart.png)"));
		const { container } = renderThumbnails([artifact("reports/notes.md", "markdown")]);

		await screen.findByText("Report");
		expect(container.querySelector("img")).toHaveAttribute("src", `${ROOT}reports/assets/chart.png`);
	});

	it("frames cloud HTML with the cloud sandbox and verifies availability through the bridge", async () => {
		renderThumbnails([artifact("index.html", "html")]);

		const frame = await screen.findByTitle("index.html");
		expect(frame).toHaveAttribute("src", `${ROOT}index.html?v=${encodeURIComponent(VERSION)}`);
		expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-forms");
		expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}index.html?v=${encodeURIComponent(VERSION)}`, { method: "HEAD" });
	});

	it("refreshes a refused cloud link and renders the replacement", async () => {
		mocks.access.mockResolvedValueOnce({ filesPrefix: "/api/cloud/v1/artifact-files/expired/", expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() });
		mocks.fetch.mockImplementation(async (url: string) => url.includes("/expired/") ? new Response(null, { status: 403 }) : new Response("# Renewed preview"));
		renderThumbnails([artifact("notes.md", "markdown")]);

		await screen.findByText("Renewed preview");
		expect(mocks.access).toHaveBeenCalledTimes(2);
		expect(mocks.fetch).toHaveBeenLastCalledWith(`${ROOT}notes.md?v=${encodeURIComponent(VERSION)}`, { method: "GET" });
	});

	it("keeps missing cloud files unavailable without retrying access", async () => {
		mocks.fetch.mockResolvedValue(new Response(null, { status: 404 }));
		const { client } = renderThumbnails([artifact("notes.md", "markdown")]);

		await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(screen.getByTitle("No preview available")).toBeInTheDocument();
		expect(mocks.access).toHaveBeenCalledTimes(1);
	});

	it.each([{ failure: "404", status: 404 }, { failure: "500", status: 500 }, { failure: "bridge failure", status: undefined }])("shows a normal-size gallery placeholder for HTML $failure", async ({ status }) => {
		mocks.fetch.mockImplementation(async () => {
			if (status === undefined) throw new Error("Bridge unavailable");
			return new Response(null, { status });
		});
		const { container, client } = renderThumbnails([artifact("index.html", "html")]);

		await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		const placeholder = screen.getByTitle("No preview available");
		expect(placeholder).toHaveClass("absolute", "inset-0");
		expect(placeholder.parentElement).not.toHaveAttribute("style");
		expect(container.querySelector("iframe")).toBeNull();
		expect(container.querySelector("button")).toBeNull();
	});

	it("keeps a card-size placeholder while HTML availability is loading", async () => {
		let finish: ((response: Response) => void) | undefined;
		mocks.fetch.mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve; }));
		const { container } = renderThumbnails([artifact("index.html", "html")]);

		await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(1));
		expect(screen.getByTitle("No preview available").parentElement).not.toHaveAttribute("style");
		expect(container.querySelector("iframe")).toBeNull();
		expect(container.querySelector("button")).toBeNull();
		await act(async () => { finish?.(new Response(null, { status: 200 })); });
		expect(await screen.findByTitle("index.html")).toBeInTheDocument();
	});

	it("shares one in-flight access refresh across simultaneously refused cards", async () => {
		let finish: ((access: { filesPrefix: string; expiresAt: string }) => void) | undefined;
		mocks.access.mockResolvedValueOnce({ filesPrefix: "/api/cloud/v1/artifact-files/expired/", expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() });
		mocks.access.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		mocks.fetch.mockImplementation(async (url: string) => url.includes("/expired/") ? new Response(null, { status: 403 }) : new Response("# Renewed preview"));
		const { client } = renderThumbnails([artifact("one.md", "markdown"), artifact("two.md", "markdown")]);

		await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(mocks.access).toHaveBeenCalledTimes(2));
		await act(async () => { finish?.({ filesPrefix: "/api/cloud/v1/artifact-files/token/", expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() }); });
		await waitFor(() => expect(screen.getAllByText("Renewed preview")).toHaveLength(2));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(mocks.access).toHaveBeenCalledTimes(2);
	});

	it.each([{ path: "notes.md", kind: "markdown" }, { path: "index.html", kind: "html" }, { path: "logo.svg", kind: "file" }] satisfies { path: string; kind: ArtifactRow["kind"] }[])("bounds persistent refusals for $path even when refreshes mint different URLs", async ({ path, kind }) => {
		let minted = 0;
		mocks.access.mockImplementation(async () => ({ filesPrefix: `/api/cloud/v1/artifact-files/token-${++minted}/`, expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() }));
		mocks.fetch.mockImplementation(async () => new Response(null, { status: 403 }));
		const { client, rerender } = renderThumbnails([artifact(path, kind)]);

		await waitFor(() => expect(mocks.fetch).toHaveBeenCalledTimes(2));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(mocks.access).toHaveBeenCalledTimes(2);
		expect(mocks.fetch.mock.calls[0][0]).not.toBe(mocks.fetch.mock.calls[1][0]);
		expect(screen.getByTitle("No preview available")).toBeInTheDocument();
		rerender([{ ...artifact(path, kind), updatedAt: "2026-10-29T10:00:00Z" }]);
		await waitFor(() => expect(mocks.access).toHaveBeenCalledTimes(3));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(mocks.access).toHaveBeenCalledTimes(3);
	});

	it("allows a new refusal episode after a successful read", async () => {
		let minted = 0;
		let refuseCurrent = true;
		mocks.access.mockImplementation(async () => ({ filesPrefix: `/api/cloud/v1/artifact-files/token-${++minted}/`, expiresAt: new Date(Date.now() + 30 * 60_000).toISOString() }));
		mocks.fetch.mockImplementation(async () => {
			if (refuseCurrent) {
				refuseCurrent = false;
				return new Response(null, { status: 403 });
			}
			return new Response("# Recovered preview");
		});
		const { client } = renderThumbnails([artifact("notes.md", "markdown")]);

		await screen.findByText("Recovered preview");
		await waitFor(() => expect(client.isFetching()).toBe(0));
		refuseCurrent = true;
		await act(async () => { await client.invalidateQueries({ queryKey: ["session-artifact-file"] }); });
		await waitFor(() => expect(mocks.access).toHaveBeenCalledTimes(3));
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(screen.getByText("Recovered preview")).toBeInTheDocument();
	});

	it.each(["not ready", "missing organization"])("shows an unavailable placeholder without fetching access when %s", async (reason) => {
		mocks.ready = reason !== "not ready";
		const row = artifact("notes.md", "markdown");
		if (reason === "missing organization") row.cloud = { orgId: "" };
		const { client } = renderThumbnails([row]);

		await act(async () => {});
		expect(screen.getByTitle("No preview available")).toBeInTheDocument();
		expect(client.isFetching()).toBe(0);
		expect(mocks.access).not.toHaveBeenCalled();
		expect(mocks.fetch).not.toHaveBeenCalled();
	});

	it("does not request access for unsupported cloud file types", async () => {
		const { client } = renderThumbnails([artifact("report.pdf"), artifact("events.jsonl"), artifact("archive.zip")]);

		await act(async () => {});
		expect(screen.getAllByTitle("No preview available")).toHaveLength(3);
		expect(client.isFetching()).toBe(0);
		expect(mocks.access).not.toHaveBeenCalled();
		expect(mocks.fetch).not.toHaveBeenCalled();
	});

});
