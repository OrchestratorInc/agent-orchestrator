import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArtifactRow } from "../lib/artifacts";
import { ArtifactThumbnail } from "./ArtifactThumbnail";

const mocks = vi.hoisted(() => ({
	access: vi.fn(),
	fetch: vi.fn(),
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { createSessionArtifactAccess: mocks.access },
		ready: true,
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
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			{rows.map((row) => <ArtifactThumbnail key={row.path} row={row} />)}
		</QueryClientProvider>,
	);
}

describe("cloud artifact thumbnails", () => {
	beforeEach(() => {
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
		const { container } = renderThumbnails([artifact("notes.md", "markdown"), artifact("logo.svg"), artifact("cover.png")]);

		await screen.findByText("Cloud preview");
		await waitFor(() => expect(container.querySelectorAll("img")).toHaveLength(2));
		expect(mocks.access).toHaveBeenCalledTimes(1);
		expect(mocks.access).toHaveBeenCalledWith("org-1", "session-1", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}notes.md?v=${encodeURIComponent(VERSION)}`, { method: "GET" });
		expect(mocks.fetch).toHaveBeenCalledWith(`${ROOT}cover.png?v=${encodeURIComponent(VERSION)}`, { method: "HEAD" });
		expect(container.querySelector('img[src*="logo.svg"]')).not.toBeNull();
		expect(container.querySelector('img[src*="cover.png"]')).not.toBeNull();
		expect(screen.queryByTitle("No preview available")).not.toBeInTheDocument();
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
		renderThumbnails([artifact("notes.md", "markdown")]);

		await screen.findByTitle("No preview available");
		expect(mocks.access).toHaveBeenCalledTimes(1);
	});
});
