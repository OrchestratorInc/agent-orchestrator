import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { ArtifactFileView } from "./ArtifactFileView";
import { TooltipProvider } from "./ui/tooltip";

const { postMock, putMock, remotePostMock } = vi.hoisted(() => ({ postMock: vi.fn(), putMock: vi.fn(), remotePostMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (error: unknown, fallback?: string) => error instanceof Error ? error.message : fallback ?? "error",
	getApiBaseUrl: () => "http://127.0.0.1:3001",
}));
vi.mock("../lib/host-clients", async (importOriginal) => ({
	...(await importOriginal<typeof import("../lib/host-clients")>()),
	clientForSessionHost: (hostId?: string) => ({ POST: hostId ? remotePostMock : postMock, PUT: putMock }),
}));
vi.mock("./MarkdownEditor", () => ({
	MarkdownEditor: ({ onChange, value }: { onChange: (value: string) => void; value: string }) => (
		<textarea aria-label="Markdown source" onChange={(event) => onChange(event.target.value)} value={value} />
	),
}));
vi.mock("../hooks/usePierreFileHighlight", () => ({ usePierreFileHighlightReady: () => true }));
vi.mock("./ReadOnlyFileView", () => ({
	ReadOnlyFileView: ({ detail }: { detail: { binary: boolean; content: string; contentTruncated: boolean; size: number } }) => (
		<div>
			<code>{detail.content}</code>
			<span data-testid="artifact-binary">{String(detail.binary)}</span>
			<span data-testid="artifact-truncated">{String(detail.contentTruncated)}</span>
			<span data-testid="artifact-size">{detail.size}</span>
		</div>
	),
}));

vi.mock("./chat/MermaidBlock", () => ({ MermaidBlock: ({ code }: { code: string }) => <figure aria-label="Mermaid diagram">{code}</figure> }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "pdf.worker.js" }));
vi.mock("pdfjs-dist", () => ({
	GlobalWorkerOptions: {},
	getDocument: () => {
		const page = {
			getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
			render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
			streamTextContent: () => null,
			cleanup: () => true,
		};
		return { promise: Promise.resolve({ numPages: 2, getPage: async () => page }), destroy: async () => {} };
	},
	TextLayer: class {
		constructor(private readonly options: { container: HTMLElement }) {}
		async render() {
			const span = document.createElement("span");
			span.textContent = "Revenue grew 12% quarter over quarter.";
			this.options.container.append(span);
		}
		cancel() {}
	},
}));

const fetchMock = vi.fn();

function renderWithQuery(ui: ReactNode, queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>{ui}</TooltipProvider>
		</QueryClientProvider>,
	);
}

function scrollContainer(): HTMLElement {
	const element = document.querySelector(".board-scrollbar");
	if (!(element instanceof HTMLElement)) throw new Error("expected artifact scroll container");
	return element;
}

describe("ArtifactFileView", () => {
	beforeEach(() => {
		vi.stubGlobal("fetch", fetchMock);
	});

	afterEach(() => {
		fetchMock.mockReset();
		postMock.mockReset();
		putMock.mockReset();
		remotePostMock.mockReset();
		vi.unstubAllGlobals();
	});

	const RAW_URL = "http://ao-preview-artifact.abc123.localhost:3001/notes.txt?raw=true";

	it.each([
		["screenshot.png", "img"],
		["animation.GIF", "img"],
		["recording.mp4", "video"],
		["recording.webm", "video"],
		["voice.mp3", "audio"],
		["voice.wav", "audio"],
	])("previews %s as media without reading it as text", async (path, tag) => {
		const rawUrl = `http://ao-preview-artifact.abc123.localhost:3001/${path}?raw=true`;
		const cancel = vi.fn();
		const response = new Response(new ReadableStream({ cancel }), { headers: { "Content-Length": "104857600" } });
		// The daemon's preview/files route only registers GET, including for media.
		fetchMock.mockImplementation((_url, init?: RequestInit) => Promise.resolve(
			init?.method && init.method !== "GET" ? new Response(null, { status: 405 }) : response,
		));

		renderWithQuery(<ArtifactFileView artifactName={path} path={path} rawUrl={rawUrl} sessionId="sess-1" />);

		const media = await screen.findByLabelText(path);
		expect(media.tagName.toLowerCase()).toBe(tag);
		const contentUrl = `http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/${path}?source=artifact&raw=true`;
		expect(media).toHaveAttribute("src", expect.stringContaining(`${contentUrl}&v=`));
		expect(fetchMock).toHaveBeenCalledExactlyOnceWith(contentUrl);
		expect(cancel).toHaveBeenCalledOnce();
		expect(screen.queryByRole("button", { name: "Edit file" })).not.toBeInTheDocument();
		if (tag !== "img") {
			expect(media).toHaveAttribute("controls");
			expect(media).toHaveAttribute("preload", "metadata");
			expect(media).not.toHaveAttribute("autoplay");
		}
	});

	it.each(["recording.mp4", "voice.wav"])("preserves %s playback until its ETag changes within the same Last-Modified second", async (path) => {
		let etag = '"1000-1791979200123456789"';
		fetchMock.mockImplementation(() => Promise.resolve(new Response(null, {
			headers: { "Content-Length": "1000", "Last-Modified": "Wed, 14 Oct 2026 12:00:00 GMT", ETag: etag },
		})));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderWithQuery(<ArtifactFileView artifactName={path} path={path} rawUrl={RAW_URL} sessionId="sess-1" />, client);
		const player = await screen.findByLabelText<HTMLMediaElement>(path);
		const src = player.src;
		expect(new URL(src).searchParams.get("v")).toBe(etag);
		player.currentTime = 12;

		await act(() => client.invalidateQueries({ queryKey: ["session-artifact-file"] }));
		expect(fetchMock).toHaveBeenCalledTimes(2);
		expect(screen.getByLabelText(path)).toBe(player);
		expect(player.src).toBe(src);
		expect(player.currentTime).toBe(12);

		etag = '"1000-1791979200123456790"';
		await act(() => client.invalidateQueries({ queryKey: ["session-artifact-file"] }));
		await waitFor(() => expect(screen.getByLabelText(path)).not.toBe(player));
		const replacement = screen.getByLabelText<HTMLMediaElement>(path);
		expect(fetchMock).toHaveBeenCalledTimes(3);
		expect(replacement.src).not.toBe(src);
		expect(new URL(replacement.src).searchParams.get("v")).toBe(etag);
		expect(replacement.currentTime).toBe(0);
	});

	it("previews SVGs as images while keeping their text source editable", async () => {
		const content = '<svg xmlns="http://www.w3.org/2000/svg"></svg>';
		fetchMock.mockResolvedValue(new Response(content));
		renderWithQuery(<ArtifactFileView artifactName="diagram.svg" path="diagram.svg" rawUrl={RAW_URL} sessionId="sess-1" />);
		expect(await screen.findByRole("img", { name: "diagram.svg" })).toHaveAttribute("src", expect.stringContaining("data:image/svg+xml"));
		await userEvent.click(screen.getByRole("button", { name: "Edit file" }));
		expect(screen.queryByRole("img", { name: "diagram.svg" })).not.toBeInTheDocument();
		expect(document.querySelector("code")).toHaveTextContent(content);
		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/diagram.svg?source=artifact&raw=true");
	});

	it("shows a retry action when media fails to load or decode", async () => {
		fetchMock.mockResolvedValue(new Response(null));
		renderWithQuery(<ArtifactFileView artifactName="clip.mp4" path="clip.mp4" rawUrl={RAW_URL} sessionId="sess-1" />);
		fireEvent.error(await screen.findByLabelText("clip.mp4"));
		expect(screen.getByText("Unable to load this file.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(screen.getByLabelText("clip.mp4")).toBeInTheDocument();
	});

	it("fetches a local artifact through the explicit loopback artifact API and renders its content", async () => {
		fetchMock.mockResolvedValue(new Response("hello world", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="notes.txt" path="notes.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByText("hello world")).toBeInTheDocument());
		expect(scrollContainer()).toHaveClass("overflow-y-auto", "min-h-0", "select-text");

		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/notes.txt?source=artifact&raw=true");
	});

	it("opens a .md artifact already rendered, not as raw markdown source", async () => {
		fetchMock.mockResolvedValue(new Response("# Notes\n\nhello", { status: 200 }));
		const rawUrl = "http://ao-preview-artifact.abc123.localhost:3001/notes.md?raw=true";

		renderWithQuery(<ArtifactFileView artifactName="notes.md" path="notes.md" rawUrl={rawUrl} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByRole("heading", { name: "Notes" })).toBeInTheDocument());
		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/notes.md?source=artifact&raw=true");
		expect(screen.getByText("hello")).toBeInTheDocument();
		expect(screen.queryByText(/^# Notes/)).not.toBeInTheDocument();
	});

	it("encodes nested artifact paths without treating their characters as URL delimiters", async () => {
		fetchMock.mockResolvedValue(new Response("content", { status: 200 }));
		const rawUrl = "http://ao-preview-artifact.abc123.localhost:3001/sub%20dir/report.txt?raw=true";

		renderWithQuery(<ArtifactFileView artifactName="report #1?.txt" path="sub dir/report #1?.txt" rawUrl={rawUrl} sessionId="sess-1" />);

		await waitFor(() => expect(fetchMock).toHaveBeenCalled());
		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/sub%20dir/report%20%231%3F.txt?source=artifact&raw=true");
	});

	it.each([
		"http://127.0.0.1:4200/remote/capability/notes.txt?raw=true",
		"https://artifacts.example.test/signed/notes.txt?signature=example",
	])("preserves a remote or cloud content URL: %s", async (rawUrl) => {
		fetchMock.mockResolvedValue(new Response("remote content", { status: 200 }));
		renderWithQuery(<ArtifactFileView artifactName="notes.txt" path="notes.txt" rawUrl={rawUrl} sessionId="sess-1" />);
		await screen.findByText("remote content");
		expect(fetchMock).toHaveBeenCalledWith(rawUrl);
	});

	it.each(["../secret.txt", "sub/../../secret.txt", "/secret.txt", "sub\\secret.txt"])("rejects an invalid local artifact path before making a request: %s", async (path) => {
		renderWithQuery(<ArtifactFileView artifactName="secret.txt" path={path} rawUrl={RAW_URL} sessionId="sess-1" />);
		await screen.findByRole("button", { name: "Retry" });
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("marks binary artifacts as binary instead of decoding them as text", async () => {
		fetchMock.mockResolvedValue(new Response("prefix\0suffix", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="data.bin" path="data.bin" rawUrl={RAW_URL} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByTestId("artifact-binary")).toHaveTextContent("true"));
		expect(screen.getByTestId("artifact-truncated")).toHaveTextContent("false");
		expect(document.querySelector("code")).toHaveTextContent("");
	});

	it("bounds oversized artifact reads and marks the content truncated", async () => {
		fetchMock.mockResolvedValue(new Response("a".repeat(256 * 1024 + 1), { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="large.txt" path="large.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByTestId("artifact-truncated")).toHaveTextContent("true"));
		expect(screen.getByTestId("artifact-binary")).toHaveTextContent("false");
		expect(screen.getByTestId("artifact-size")).toHaveTextContent(String(256 * 1024 + 1));
	});

	it("shows one view of a markdown artifact, with no file/rendered switch", async () => {
		fetchMock.mockResolvedValue(new Response("# Notes\n\nhello", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="notes.md" path="notes.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		await screen.findByRole("heading", { name: "Notes" });
		expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
		expect(screen.queryByRole("tab", { name: "Rendered" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Edit file" })).toBeInTheDocument();
	});

	it("edits a markdown artifact in place and saves it against the loaded content's fingerprint", async () => {
		const original = "# Notes\n\nhello";
		fetchMock.mockResolvedValue(new Response(original, { status: 200 }));
		putMock.mockResolvedValue({ data: { fileFingerprint: "next", path: "notes.md", size: 17 } });

		renderWithQuery(<ArtifactFileView artifactName="notes.md" path="notes.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		await userEvent.click(await screen.findByRole("button", { name: "Edit file" }));
		const source = screen.getByRole("textbox", { name: "Markdown source" });
		fireEvent.change(source, { target: { value: "# Notes\n\nhello there" } });
		await userEvent.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/artifact-files", {
				params: { path: { sessionId: "sess-1" } },
				body: {
					content: "# Notes\n\nhello there",
					// SHA-256 of the loaded content.
					expectedFileFingerprint: "8308cc372b77cd665969724964a425bea33928d861f3b23eb66b76b834442eac",
					path: "notes.md",
				},
			}),
		);
		// Back to the rendered preview, showing the saved text.
		expect(await screen.findByText("hello there")).toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Markdown source" })).not.toBeInTheDocument();
	});

	it("does not offer editing for a read-only artifact", async () => {
		fetchMock.mockResolvedValue(new Response("content", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="notes.txt" path="notes.txt" rawUrl={RAW_URL} readOnly sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByText("content")).toBeInTheDocument());
		expect(screen.queryByRole("button", { name: "Edit file" })).not.toBeInTheDocument();
	});

	it("does not offer editing for a truncated artifact", async () => {
		fetchMock.mockResolvedValue(new Response("a".repeat(256 * 1024 + 1), { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="large.txt" path="large.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByTestId("artifact-truncated")).toHaveTextContent("true"));
		expect(screen.queryByRole("button", { name: "Edit file" })).not.toBeInTheDocument();
	});

	it("opens the feedback composer on text selected in the rendered artifact and sends it with the selection", async () => {
		fetchMock.mockResolvedValue(new Response("# Plan\n\nCache the session list.", { status: 200 }));
		postMock.mockResolvedValue({ data: { status: "ok" } });
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 200, 120, 16));

		renderWithQuery(<ArtifactFileView artifactName="plan.md" path="plan.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		const paragraph = await screen.findByText("Cache the session list.");
		const range = document.createRange();
		range.selectNodeContents(paragraph);
		window.getSelection()?.removeAllRanges();
		window.getSelection()?.addRange(range);
		fireEvent.pointerUp(paragraph);

		const textbox = await screen.findByRole("textbox", { name: "Feedback for plan.md · selected text" });
		await userEvent.type(textbox, "Use a TTL");
		await userEvent.click(screen.getByRole("button", { name: "Send feedback" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		const message = postMock.mock.calls[0][1].body.message as string;
		expect(message).toContain("Use a TTL");
		expect(message).toContain("- Location: Selected text");
		expect(message).toContain("- Selected text: Cache the session list.");
		expect(message).toContain("- Path: plan.md");
	});

	it("copies text selected in the rendered artifact with the copy shortcut while the composer has focus", async () => {
		fetchMock.mockResolvedValue(new Response("# Plan\n\nCache the session list.", { status: 200 }));
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 200, 120, 16));
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);

		renderWithQuery(<ArtifactFileView artifactName="plan.md" path="plan.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		const paragraph = await screen.findByText("Cache the session list.");
		const range = document.createRange();
		range.selectNodeContents(paragraph);
		window.getSelection()?.removeAllRanges();
		window.getSelection()?.addRange(range);
		fireEvent.pointerUp(paragraph);
		const textbox = await screen.findByRole("textbox", { name: "Feedback for plan.md · selected text" });

		const setData = vi.fn();
		fireEvent.copy(textbox, { clipboardData: { setData } });
		expect(setData).toHaveBeenCalledWith("text/plain", "Cache the session list.");

		expect(screen.queryByRole("button", { name: /copy/i })).not.toBeInTheDocument();
		fireEvent.keyDown(textbox, { key: "c", metaKey: true });
		expect(writeText).toHaveBeenCalledWith("Cache the session list.");
		writeText.mockClear();
		fireEvent.keyDown(textbox, { key: "c", ctrlKey: true });
		expect(writeText).toHaveBeenCalledWith("Cache the session list.");
	});

	it("copies a fenced code block in a rendered markdown artifact", async () => {
		fetchMock.mockResolvedValue(new Response("# Setup\n\n```sh\nnpm install\n```", { status: 200 }));
		const writeText = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);

		renderWithQuery(<ArtifactFileView artifactName="setup.md" path="setup.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		fireEvent.click(await screen.findByRole("button", { name: "Copy code" }));
		expect(writeText).toHaveBeenCalledWith("npm install");
	});

	it("closes an empty selection composer on a click elsewhere", async () => {
		fetchMock.mockResolvedValue(new Response("# Plan\n\nCache the session list.", { status: 200 }));
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 200, 120, 16));

		renderWithQuery(<ArtifactFileView artifactName="plan.md" path="plan.md" rawUrl={RAW_URL} sessionId="sess-1" />);

		const paragraph = await screen.findByText("Cache the session list.");
		const range = document.createRange();
		range.selectNodeContents(paragraph);
		window.getSelection()?.removeAllRanges();
		window.getSelection()?.addRange(range);
		fireEvent.pointerUp(paragraph);
		await screen.findByRole("textbox", { name: "Feedback for plan.md · selected text" });

		window.getSelection()?.removeAllRanges();
		fireEvent.pointerDown(document.body);

		await waitFor(() => expect(screen.queryByRole("textbox", { name: /selected text/ })).not.toBeInTheDocument());
	});

	it("shows a retry option when the fetch fails", async () => {
		fetchMock.mockResolvedValue(new Response("nope", { status: 404 }));

		renderWithQuery(<ArtifactFileView artifactName="notes.txt" path="notes.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument());
		expect(screen.getByText("Unable to load artifact file")).toBeInTheDocument();
	});

	it("shows a retry option when no rawUrl is available", async () => {
		renderWithQuery(<ArtifactFileView artifactName="notes.txt" path="notes.txt" sessionId="sess-1" />);

		await waitFor(() => expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument());
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("opens whole-artifact feedback from a reveal request and sends it to the worker", async () => {
		fetchMock.mockResolvedValue(new Response("content", { status: 200 }));
		postMock.mockResolvedValue({ data: { status: "ok" } });

		renderWithQuery(<ArtifactFileView artifactName="notes.txt" feedbackRequestKey={1} path="notes.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		const textbox = await screen.findByRole("textbox", { name: /Feedback for notes\.txt/ });
		await userEvent.type(textbox, "Please tighten this artifact.");
		await userEvent.click(screen.getByRole("button", { name: "Send feedback" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
				params: { path: { sessionId: "sess-1" } },
				body: { message: expect.stringContaining("Please tighten this artifact."), userAuthored: true },
			}),
		);
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: "sess-1" } },
			body: { message: expect.stringContaining("notes.txt"), userAuthored: true },
		});
	});

	it("sends artifact feedback for a remote session to that host's daemon, not the local one", async () => {
		fetchMock.mockResolvedValue(new Response("content", { status: 200 }));
		remotePostMock.mockResolvedValue({ data: { status: "ok" } });

		renderWithQuery(<ArtifactFileView artifactName="notes.txt" feedbackRequestKey={1} hostId="host-2" path="notes.txt" rawUrl={RAW_URL} sessionId="sess-1" />);

		const textbox = await screen.findByRole("textbox", { name: /Feedback for notes\.txt/ });
		await userEvent.type(textbox, "Remote feedback.");
		await userEvent.click(screen.getByRole("button", { name: "Send feedback" }));

		await waitFor(() => expect(remotePostMock).toHaveBeenCalled());
		expect(postMock).not.toHaveBeenCalled();
	});

	it("resolves a markdown artifact's sibling image against the artifact origin, not the workspace", async () => {
		fetchMock.mockResolvedValue(new Response("# Report\n\n![chart](chart.png)", { status: 200 }));
		const rawUrl = "http://ao-preview-artifact.abc123.localhost:3001/sub/report.md?raw=true";

		renderWithQuery(<ArtifactFileView artifactName="report.md" path="sub/report.md" rawUrl={rawUrl} sessionId="sess-1" />);

		const image = await screen.findByRole("img", { name: "chart" });
		expect(image).toHaveAttribute("src", "http://ao-preview-artifact.abc123.localhost:3001/sub/chart.png");
	});

	it("previews a PDF's pages and sends feedback on text selected in a page", async () => {
		const rawUrl = "http://ao-preview-artifact.abc123.localhost:3001/report.pdf?raw=true";
		fetchMock.mockImplementation(async () => new Response(new Uint8Array([37, 80, 68, 70]), { headers: { ETag: "\"r1\"" }, status: 200 }));
		postMock.mockResolvedValue({ data: { status: "ok" } });
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 200, 120, 16));
		vi.stubGlobal("ResizeObserver", class {
			constructor(private readonly callback: ResizeObserverCallback) {}
			observe() {
				this.callback([{ contentRect: { width: 600 } } as ResizeObserverEntry], this as unknown as ResizeObserver);
			}
			disconnect() {}
		});

		renderWithQuery(<ArtifactFileView artifactName="report.pdf" path="report.pdf" rawUrl={rawUrl} sessionId="sess-1" />);

		await waitFor(() => expect(document.querySelectorAll("[data-page-number]")).toHaveLength(2));
		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/report.pdf?source=artifact&raw=true&v=%22r1%22");
		expect(screen.queryByTestId("artifact-binary")).not.toBeInTheDocument();

		const [text] = await screen.findAllByText("Revenue grew 12% quarter over quarter.");
		const range = document.createRange();
		range.selectNodeContents(text);
		window.getSelection()?.removeAllRanges();
		window.getSelection()?.addRange(range);
		fireEvent.pointerUp(text);

		const textbox = await screen.findByRole("textbox", { name: "Feedback for report.pdf · selected text" });
		await userEvent.type(textbox, "Cite the source");
		await userEvent.click(screen.getByRole("button", { name: "Send feedback" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		const message = postMock.mock.calls[0][1].body.message as string;
		expect(message).toContain("Cite the source");
		expect(message).toContain("- Selected text: Revenue grew 12% quarter over quarter.");
		expect(message).toContain("- Path: report.pdf");
	});

	it("opens a CSV artifact as a table that sorts by a clicked column", async () => {
		fetchMock.mockResolvedValue(new Response("name,count\nbeta,10\nalpha,9\n", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="usage.csv" path="usage.csv" rawUrl={RAW_URL} sessionId="sess-1" />);

		const table = await screen.findByRole("table");
		const firstColumn = () => Array.from(table.querySelectorAll("tbody tr")).map((row) => row.firstElementChild?.textContent);
		expect(firstColumn()).toEqual(["beta", "alpha"]);
		const count = screen.getByRole("columnheader", { name: "count" });
		expect(count).toHaveClass("text-right");

		await userEvent.click(screen.getByRole("button", { name: "count" }));
		expect(count).toHaveAttribute("aria-sort", "ascending");
		expect(firstColumn()).toEqual(["alpha", "beta"]);
		await userEvent.click(screen.getByRole("button", { name: "count" }));
		expect(firstColumn()).toEqual(["beta", "alpha"]);
		await userEvent.click(screen.getByRole("button", { name: "count" }));
		expect(count).toHaveAttribute("aria-sort", "none");
	});

	it("sends feedback on a table cell selected in a CSV artifact", async () => {
		fetchMock.mockResolvedValue(new Response("name,count\nalpha,9\n", { status: 200 }));
		postMock.mockResolvedValue({ data: { status: "ok" } });
		vi.spyOn(Range.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(40, 200, 120, 16));

		renderWithQuery(<ArtifactFileView artifactName="usage.csv" path="usage.csv" rawUrl={RAW_URL} sessionId="sess-1" />);

		const cell = await screen.findByRole("cell", { name: "alpha" });
		const range = document.createRange();
		range.selectNodeContents(cell);
		window.getSelection()?.removeAllRanges();
		window.getSelection()?.addRange(range);
		fireEvent.pointerUp(cell);

		const textbox = await screen.findByRole("textbox", { name: "Feedback for usage.csv · selected text" });
		await userEvent.type(textbox, "Rename this");
		await userEvent.click(screen.getByRole("button", { name: "Send feedback" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock.mock.calls[0][1].body.message as string).toContain("- Selected text: alpha");
	});

	it("opens a Mermaid artifact as its diagram", async () => {
		fetchMock.mockResolvedValue(new Response("graph TD\n  A-->B\n", { status: 200 }));

		renderWithQuery(<ArtifactFileView artifactName="flow.mmd" path="flow.mmd" rawUrl={RAW_URL} sessionId="sess-1" />);

		expect(await screen.findByRole("figure", { name: "Mermaid diagram" })).toHaveTextContent("graph TD A-->B");
	});
});
