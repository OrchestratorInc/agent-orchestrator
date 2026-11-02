import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { getDocument } from "pdfjs-dist";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { FileMediaPreview } from "./FileMediaPreview";

vi.mock("../lib/api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:3001" }));
vi.mock("../hooks/useHostConnection", () => ({
	useHostConnection: (hostId?: string) => ({ baseUrl: hostId === "remote" ? "http://127.0.0.1:4000/token" : undefined }),
}));

vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "pdf.worker.js" }));
vi.mock("pdfjs-dist", () => ({
	GlobalWorkerOptions: {},
	getDocument: vi.fn(),
	// What PdfPreview calls on a text layer: it draws one, and cancels it when the page unmounts.
	TextLayer: class {
		render() {
			return Promise.resolve();
		}
		cancel() {}
	},
}));

describe("FileMediaPreview", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it.each(["image", "video", "audio"] as const)("uses renderer-origin CORS for protected %s", (kind) => {
		render(<FileMediaPreview kind={kind} path="media" sessionId="s1" source={{kind: "workspace"}} version={1} />);
		expect(screen.getByLabelText("media")).toHaveAttribute("crossorigin", "anonymous");
	});

	it("encodes paths and streams from the selected remote host", () => {
		render(<FileMediaPreview hostId="remote" kind="video" path="clips/a #1.mp4" sessionId="session 1" source={{ kind: "workspace" }} version={7} />);
		expect(screen.getByLabelText("clips/a #1.mp4")).toHaveAttribute("src", "http://127.0.0.1:4000/token/api/v1/sessions/session%201/preview/files/clips/a%20%231.mp4?raw=true&v=7");
	});

	it("does not fall back to local media when the selected host is offline", () => {
		render(<FileMediaPreview hostId="offline" kind="audio" path="voice.mp3" sessionId="session-1" source={{ kind: "workspace" }} version={1} />);
		expect(screen.getByText("Unable to load this file.")).toBeInTheDocument();
		expect(screen.queryByLabelText("voice.mp3")).not.toBeInTheDocument();
	});

	it("retries a changed artifact URL after a failed load", () => {
		const props = { kind: "image" as const, path: "logo.png", sessionId: "session-1", source: { kind: "artifact" as const, rawUrl: "https://artifacts.example.test/logo.png?raw=true" } };
		const view = render(<FileMediaPreview {...props} version={1} />);
		fireEvent.error(screen.getByLabelText("logo.png"));
		expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
		view.rerender(<FileMediaPreview {...props} version={2} />);
		expect(screen.getByLabelText("logo.png")).toHaveAttribute("src", `${props.source.rawUrl}&v=2`);
	});

	it("plays a cloud video from its control-plane link and asks for a fresh link when it fails", () => {
		const onAccessRefused = vi.fn();
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/demo.mp4?v=1", onAccessRefused };
		render(<FileMediaPreview kind="video" path="demo.mp4" sessionId="session-1" source={source} version={1} />);
		const video = screen.getByLabelText("demo.mp4");
		expect(video.getAttribute("src")).toMatch(/^https:\/\/cp\.test\/api\/cloud\/v1\/artifact-files\/t\/demo\.mp4\?v=1/);
		fireEvent.error(video);
		expect(onAccessRefused).toHaveBeenCalledTimes(1);
	});

	it("loads cloud media without CORS, which a localhost control plane does not send", () => {
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "http://127.0.0.1:8081/api/cloud/v1/artifact-files/t/demo.mp4?v=1", onAccessRefused: vi.fn() };
		render(<FileMediaPreview kind="video" path="demo.mp4" sessionId="session-1" source={source} version={1} />);
		expect(screen.getByLabelText("demo.mp4")).not.toHaveAttribute("crossorigin");
	});

	it("takes one fresh link per file version, then shows a cloud file that still fails", () => {
		const onAccessRefused = vi.fn();
		const props = { kind: "video" as const, path: "broken.mp4", sessionId: "session-1", version: 1 };
		const source = (token: string) => ({ kind: "artifact" as const, cloud: true, rawUrl: `https://cp.test/api/cloud/v1/artifact-files/${token}/broken.mp4?v=1`, onAccessRefused });
		const view = render(<FileMediaPreview {...props} source={source("t1")} />);
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		view.rerender(<FileMediaPreview {...props} source={source("t2")} />);
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		view.rerender(<FileMediaPreview {...props} source={source("t3")} />);
		expect(onAccessRefused).toHaveBeenCalledTimes(1);

		// The fresh link failed too: the error shows, and Retry may take one more link.
		view.rerender(<FileMediaPreview {...props} source={source("t2")} />);
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		fireEvent.error(screen.getByLabelText("broken.mp4"));
		expect(onAccessRefused).toHaveBeenCalledTimes(2);
	});

	it("does not add a second version to a cloud file's URL", () => {
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/chart.png?v=1" };
		render(<FileMediaPreview kind="image" path="chart.png" sessionId="session-1" source={source} version="2026-10-22T00:00:00Z" />);
		expect(screen.getByLabelText("chart.png")).toHaveAttribute("src", source.rawUrl);
	});

	it("shows a cloud image from its control-plane link", () => {
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/chart.png?v=1" };
		render(<FileMediaPreview kind="image" path="chart.png" sessionId="session-1" source={source} version={1} />);
		expect(screen.getByLabelText("chart.png").getAttribute("src")).toContain("https://cp.test/api/cloud/v1/artifact-files/t/chart.png");
	});

	it("streams local artifacts through the explicit loopback artifact route", () => {
		render(<FileMediaPreview kind="video" path="clips/a #1.mp4" sessionId="session 1" source={{ kind: "artifact", rawUrl: "http://ao-preview-artifact.abc.localhost/clip.mp4?raw=true" }} version={7} />);
		expect(screen.getByLabelText("clips/a #1.mp4")).toHaveAttribute("src", "http://127.0.0.1:3001/api/v1/sessions/session%201/preview/files/clips/a%20%231.mp4?source=artifact&raw=true&v=7");
	});

	it("shows Retry when a PDF fails to load", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => new Response("missing", { status: 404 })));
		render(<FileMediaPreview kind="pdf" path="report.pdf" sessionId="session-1" source={{ kind: "artifact", rawUrl: "http://ao-preview-artifact.abc.localhost/report.pdf?raw=true" }} version={1} />);
		expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
	});

	it("renders a cloud PDF's pages from bytes read through the main process", async () => {
		const bytes = new Uint8Array([37, 80, 68, 70]);
		const fetchFile = vi.spyOn(aoBridge.cloudArtifact, "fetch").mockResolvedValue(bytes);
		const browserFetch = vi.fn();
		vi.stubGlobal("fetch", browserFetch);
		vi.stubGlobal("ResizeObserver", class {
			constructor(private readonly callback: ResizeObserverCallback) {}
			observe() {
				this.callback([{ contentRect: { width: 600 } } as ResizeObserverEntry], this as unknown as ResizeObserver);
			}
			disconnect() {}
		});
		const page = {
			getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
			render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
			streamTextContent: () => null,
			cleanup: () => true,
		};
		vi.mocked(getDocument).mockReturnValueOnce({ promise: Promise.resolve({ numPages: 2, getPage: async () => page }), destroy: async () => {} } as unknown as ReturnType<typeof getDocument>);
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/report.pdf?v=1" };
		render(<FileMediaPreview kind="pdf" path="report.pdf" sessionId="session-1" source={source} version={1} />);

		await waitFor(() => expect(document.querySelectorAll("[data-page-number]")).toHaveLength(2));
		// Both pages finish their text layers, so unmounting cancels them every run
		// rather than only when the test happens to end after they were built.
		await waitFor(() => expect(document.querySelectorAll(".endOfContent")).toHaveLength(2));
		expect(fetchFile).toHaveBeenCalledWith(source.rawUrl);
		expect(getDocument).toHaveBeenCalledWith({ data: bytes });
		expect(browserFetch).not.toHaveBeenCalled();
		fetchFile.mockRestore();
	});

	it("reads a cloud PDF through the main process and asks for a fresh link when it fails", async () => {
		const fetchFile = vi.spyOn(aoBridge.cloudArtifact, "fetch").mockRejectedValue(new Error("Artifact request failed (403)"));
		const browserFetch = vi.fn();
		vi.stubGlobal("fetch", browserFetch);
		const onAccessRefused = vi.fn();
		const source = { kind: "artifact" as const, cloud: true, rawUrl: "https://cp.test/api/cloud/v1/artifact-files/t/report.pdf?v=1", onAccessRefused };
		render(<FileMediaPreview kind="pdf" path="report.pdf" sessionId="session-1" source={source} version={1} />);
		expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
		expect(fetchFile).toHaveBeenCalledWith(source.rawUrl);
		expect(browserFetch).not.toHaveBeenCalled();
		expect(onAccessRefused).toHaveBeenCalledTimes(1);
		fetchFile.mockRestore();
	});

	it("does not read a PDF over the preview size cap", async () => {
		const cancel = vi.fn();
		vi.stubGlobal("fetch", vi.fn(async () => new Response(new ReadableStream({ cancel }), { headers: { "Content-Length": String(200 * 1024 * 1024) } })));
		render(<FileMediaPreview kind="pdf" path="huge.pdf" sessionId="session-1" source={{ kind: "artifact", rawUrl: "http://ao-preview-artifact.abc.localhost/huge.pdf?raw=true" }} version={1} />);
		expect(await screen.findByText("File is too large to preview (200.0 MB).")).toBeInTheDocument();
		expect(cancel).toHaveBeenCalled();
		expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
	});
});
