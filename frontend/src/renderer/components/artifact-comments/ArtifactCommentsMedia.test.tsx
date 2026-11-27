import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useEffect, useState, type ReactNode, type Ref } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { CloudCpClient } from "../../lib/cloud-cp";
import { TooltipProvider } from "../ui/tooltip";
import { forgetMediaPlayback, mediaPlaybackKey, mediaTime, rememberMediaSource, rememberMediaTime } from "../../lib/media-playback";
import { ArtifactCommentsWorkspace, artifactCommentsKind, type ArtifactCommentsKind } from "./ArtifactComments";
import { ArtifactVideoPlayer } from "./ArtifactCommentsMedia";
import {
	ARTIFACT_COMMENTS_PROTOCOL_VERSION,
	imageCommentAnchor,
	videoCommentAnchor,
	type ArtifactCommentAnchor,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
} from "../../../shared/artifact-comments";

const mocks = vi.hoisted(() => ({
	client: {} as Record<string, unknown>,
	// The media URL and when it was fetched; refresh() hands out a new one.
	media: { url: "https://share.test/file", fetchedAt: Date.now(), refreshes: 0, listeners: new Set<() => void>() },
}));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test", userId: "owner-1" }) }));
vi.mock("../../hooks/useSendToSessionWorker", () => ({ useSendToSessionWorker: () => vi.fn() }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => vi.fn() }));
// The published file: a plain frame, or the comment view's own media from its content URL.
vi.mock("../SharedArtifactFrame", () => ({
	SharedArtifactFrame: function Frame({ frameRef, renderMedia, title }: {
		frameRef?: Ref<HTMLIFrameElement>;
		renderMedia?: (url: string, shown: () => void, source: { fetchedAt: number; refresh: () => void }) => ReactNode;
		title: string;
	}) {
		const [, rerender] = useState(0);
		useEffect(() => {
			const listener = () => rerender((n) => n + 1);
			mocks.media.listeners.add(listener);
			return () => { mocks.media.listeners.delete(listener); };
		}, []);
		const refresh = () => {
			mocks.media.refreshes += 1;
			mocks.media.url = `https://share.test/file?v=${mocks.media.refreshes}`;
			mocks.media.fetchedAt = Date.now();
			for (const listener of mocks.media.listeners) listener();
		};
		return renderMedia
			? <>{renderMedia(mocks.media.url, () => undefined, { fetchedAt: mocks.media.fetchedAt, refresh })}</>
			: <iframe data-testid="published-frame" ref={frameRef} title={title} />;
	},
}));

const sam = { id: "sam-1", name: "Sam" };

function thread(id: string, anchor: ArtifactCommentAnchor, body = "Look here", createdAt = "2026-11-23T08:00:00Z"): ArtifactCommentThread {
	return {
		id,
		version: 3,
		status: "open",
		anchor,
		createdAt,
		updatedAt: createdAt,
		resolvedBy: null,
		resolvedAt: null,
		sentToAgentAt: null,
		sentToAgentCount: 0,
		comments: [{ id: `c-${id}`, threadId: id, clientId: `k-${id}`, author: sam, body, createdAt, editedAt: null, deleted: false }],
	};
}

type CommentMethods = Pick<CloudCpClient, "listArtifactShareComments" | "createArtifactShareThread" | "replyArtifactShareThread" | "setArtifactShareThreadStatus" | "editArtifactShareComment" | "deleteArtifactShareComment" | "markArtifactShareThreadSent">;
type FakeClient = { [K in keyof CommentMethods]: Mock<CommentMethods[K]> };

function fakeClient(threads: ArtifactCommentThread[]): FakeClient {
	const snapshot: ArtifactCommentsSnapshot = { version: 3, currentVersion: 3, revision: 41, you: { userId: "owner-1", isOwner: true }, threads };
	const client: FakeClient = {
		listArtifactShareComments: vi.fn().mockResolvedValue({ notModified: false, snapshot, etag: "\"r41-v3\"" }),
		createArtifactShareThread: vi.fn().mockImplementation(async (_slug, input) => thread("new", input.anchor, input.body, "2026-11-25T10:00:00Z")),
		replyArtifactShareThread: vi.fn(),
		setArtifactShareThreadStatus: vi.fn(),
		editArtifactShareComment: vi.fn(),
		deleteArtifactShareComment: vi.fn(),
		markArtifactShareThreadSent: vi.fn(),
	};
	mocks.client = client;
	return client;
}

async function renderWorkspace(client: FakeClient, kind: ArtifactCommentsKind, { mediaKey, outdated = false }: { mediaKey?: string; outdated?: boolean } = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ArtifactCommentsWorkspace kind={kind} mediaKey={mediaKey} onClose={vi.fn()} outdated={outdated} slug="cf-staging-x7" title="file" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalled());
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
}

/** A 200×100 picture in a 400×400 element: drawn 400×200, 100 px down. */
function stubSizes() {
	vi.spyOn(HTMLImageElement.prototype, "naturalWidth", "get").mockReturnValue(200);
	vi.spyOn(HTMLImageElement.prototype, "naturalHeight", "get").mockReturnValue(100);
	vi.spyOn(HTMLVideoElement.prototype, "videoWidth", "get").mockReturnValue(200);
	vi.spyOn(HTMLVideoElement.prototype, "videoHeight", "get").mockReturnValue(100);
	vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(400);
	vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(400);
	vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
		const rect = (left: number, top: number, width: number, height: number) => ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON() {} }) as DOMRect;
		if (this.dataset.testid === "artifact-comments-media-overlay") return rect(0, 100, 400, 200);
		return rect(0, 0, 0, 0);
	});
}

function stubVideo(duration = 250) {
	const state = { time: 0, paused: true };
	// Its metadata is in: it can seek.
	vi.spyOn(HTMLMediaElement.prototype, "readyState", "get").mockReturnValue(1);
	vi.spyOn(HTMLMediaElement.prototype, "duration", "get").mockReturnValue(duration);
	vi.spyOn(HTMLMediaElement.prototype, "currentTime", "get").mockImplementation(() => state.time);
	vi.spyOn(HTMLMediaElement.prototype, "currentTime", "set").mockImplementation((value: number) => { state.time = value; });
	vi.spyOn(HTMLMediaElement.prototype, "paused", "get").mockImplementation(() => state.paused);
	vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) { state.paused = true; this.dispatchEvent(new Event("pause")); });
	vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) { state.paused = false; this.dispatchEvent(new Event("play")); return Promise.resolve(); });
	return state;
}

beforeEach(() => {
	vi.restoreAllMocks();
	forgetMediaPlayback();
	Object.assign(mocks.media, { url: "https://share.test/file", fetchedAt: Date.now(), refreshes: 0 });
});
afterEach(() => vi.restoreAllMocks());

describe("artifactCommentsKind", () => {
	it("follows the share host: elements, lines, images and videos; no audio, PDF or Mermaid", () => {
		expect(artifactCommentsKind("docs/guide.md")).toBe("markdown");
		expect(artifactCommentsKind("site/index.HTML")).toBe("html");
		for (const path of ["server.log", "data.csv", "config.json", "main.go", "Makefile"]) expect(artifactCommentsKind(path)).toBe("text");
		for (const path of ["shot.PNG", "logo.svg", "pic.webp"]) expect(artifactCommentsKind(path)).toBe("image");
		for (const path of ["demo.mp4", "clip.webm", "take.mov"]) expect(artifactCommentsKind(path)).toBe("video");
		for (const path of ["memo.mp3", "report.pdf", "flow.mmd"]) expect(artifactCommentsKind(path)).toBeNull();
	});
});

describe("comments on an image", () => {
	it("picks a point on the picture and posts it in fractions, from a popover labelled in words", async () => {
		stubSizes();
		const client = fakeClient([]);
		await renderWorkspace(client, "image");
		// No comments: nothing pops up until something is picked.
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();
		const overlay = screen.getByTestId("artifact-comments-media-overlay");
		fireEvent.pointerDown(overlay, { button: 0, clientX: 100, clientY: 150 });
		fireEvent.pointerUp(overlay, { button: 0, clientX: 100, clientY: 150 });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		// No words for where on an image: the pin shows it.
		expect(within(popover).queryByTestId("artifact-comments-where")).toBeNull();
		expect(screen.getByTestId("artifact-comments-media-draft")).toBeInTheDocument();
		await userEvent.type(within(popover).getByRole("textbox", { name: "Add a comment…" }), "Crop this");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalled());
		expect(client.createArtifactShareThread.mock.calls[0]![1]).toMatchObject({
			anchor: { kind: "image", path: "body>img:1", tag: "img", region: { x: 0.25, y: 0.25, w: 0, h: 0 } },
			body: "Crop this",
		});
	});

	it("opens a marker's threads from its pin", async () => {
		stubSizes();
		await renderWorkspace(fakeClient([thread("t1", imageCommentAnchor({ x: 0.6, y: 0.6, w: 0.3, h: 0.3 }), "Blurry")]), "image");
		await userEvent.click(screen.getByRole("button", { name: "Region near bottom-right: 1 open" }));
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByText("Blurry")).toBeInTheDocument();
		expect(within(popover).queryByTestId("artifact-comments-where")).toBeNull();
	});

	it("adds another comment on the same point from its pin", async () => {
		stubSizes();
		const anchor = imageCommentAnchor({ x: 0.6, y: 0.6, w: 0.3, h: 0.3 });
		const client = fakeClient([thread("t1", anchor, "Blurry")]);
		await renderWorkspace(client, "image");
		await userEvent.click(screen.getByRole("button", { name: "Region near bottom-right: 1 open" }));
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		await userEvent.click(within(popover).getByTestId("artifact-comments-add"));
		await userEvent.type(await within(popover).findByRole("textbox", { name: "Add a comment…" }), "And too dark");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalled());
		expect(client.createArtifactShareThread.mock.calls[0]![1]).toMatchObject({ anchor, body: "And too dark" });
	});
});

describe("comments on a video", () => {
	const badges = () => screen.getAllByTestId("artifact-comments-timeline-point");

	it("shows numbered badges on the seek bar; a badge opens its popover at its frame with its box", async () => {
		stubSizes();
		const video = stubVideo(250);
		const client = fakeClient([
			thread("t1", videoCommentAnchor(83.5, { x: 0.05, y: 0.05, w: 0.2, h: 0.2 }), "Typo in the title card", "2026-11-23T08:00:00Z"),
			// About the same moment (within 1.2% of the length): the same badge.
			thread("t2", videoCommentAnchor(84), "Music too loud", "2026-11-23T09:00:00Z"),
			thread("t3", videoCommentAnchor(12), "Intro drags", "2026-11-23T10:00:00Z"),
		]);
		await renderWorkspace(client, "video");
		const element = document.querySelector("video")!;
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		// One player: no native controls, no side panel, no Annotate or "Comment at".
		expect(element.controls).toBe(false);
		expect(screen.getAllByRole("button", { name: "Play" })).toHaveLength(1);
		expect(screen.queryByTestId("artifact-comments-video-panel")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Annotate|^Comment at/ })).not.toBeInTheDocument();
		// Badges with their counts, like a text page's markers; nothing on the frame.
		expect(badges().map((badge) => [badge.getAttribute("aria-label"), badge.textContent])).toEqual([
			["Comments at 00:12: 1 open", "1"],
			["Comments at 01:23: 2 open", "2"],
		]);
		expect(screen.queryByTestId("artifact-comments-frame-highlight")).not.toBeInTheDocument();
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();

		// A badge: its popover, the video at its frame, paused, with its box outlined.
		await userEvent.click(badges()[1]!);
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(video.time).toBe(83.5);
		expect(video.paused).toBe(true);
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("Frame at 01:23");
		// Both moments at the badge, in one popover.
		expect(within(popover).getByText("Typo in the title card")).toBeInTheDocument();
		expect(within(popover).getByText(/Music too loud/)).toBeInTheDocument();
		expect(screen.getAllByTestId("artifact-comments-frame-highlight")).toHaveLength(1);
		expect(badges()[1]).toHaveAttribute("data-selected", "");

		// Another badge: its own popover and frame; no box (it has none).
		await userEvent.click(badges()[0]!);
		await waitFor(() => expect(within(screen.getByRole("complementary", { name: "Comments" })).getByText("Intro drags")).toBeInTheDocument());
		expect(video.time).toBe(12);
		expect(screen.queryByTestId("artifact-comments-frame-highlight")).not.toBeInTheDocument();
	});

	it("fetches a fresh URL before seeking once its view token is near its end, then seeks to the badge's frame", async () => {
		stubSizes();
		const video = stubVideo(250);
		await renderWorkspace(fakeClient([thread("t1", videoCommentAnchor(5), "Let us not highlight in yellow")]), "video");
		const element = document.querySelector("video")!;
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		// Opened 9 minutes ago: its token (10 minutes) is nearly spent.
		mocks.media.fetchedAt = Date.now() - 9 * 60_000;
		await userEvent.click(badges()[0]!);
		expect(mocks.media.refreshes).toBe(1);
		await waitFor(() => expect(element.getAttribute("src")).toBe("https://share.test/file?v=1"));
		// The new URL loads from 00:00, then goes to the badge's frame.
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		expect(video.time).toBe(5);
		expect(video.paused).toBe(true);
	});

	it("comes back to the same moment when its URL is refused and fetched again", async () => {
		stubSizes();
		const video = stubVideo(250);
		await renderWorkspace(fakeClient([]), "video");
		const element = document.querySelector("video")!;
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		video.time = 42;
		act(() => { element.dispatchEvent(new Event("timeupdate")); });
		act(() => { element.dispatchEvent(new Event("error")); });
		expect(mocks.media.refreshes).toBe(1);
		await waitFor(() => expect(element.getAttribute("src")).toBe("https://share.test/file?v=1"));
		video.time = 0;
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		expect(video.time).toBe(42);
		// Only once per URL: a second error on the same URL fetches nothing more.
		act(() => {
			element.dispatchEvent(new Event("error"));
			element.dispatchEvent(new Event("error"));
		});
		expect(mocks.media.refreshes).toBe(2);
	});

	it("a click on the frame plays and pauses; a drag draws a box and comments on it at that time", async () => {
		stubSizes();
		const video = stubVideo(250);
		const client = fakeClient([]);
		await renderWorkspace(client, "video");
		const element = document.querySelector("video")!;
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		const overlay = screen.getByTestId("artifact-comments-media-overlay");
		fireEvent.pointerDown(overlay, { button: 0, clientX: 100, clientY: 150 });
		fireEvent.pointerUp(overlay, { button: 0, clientX: 100, clientY: 150 });
		expect(video.paused).toBe(false);
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();
		video.time = 125.25;
		act(() => { element.dispatchEvent(new Event("timeupdate")); });

		fireEvent.pointerDown(overlay, { button: 0, clientX: 200, clientY: 200 });
		fireEvent.pointerMove(overlay, { button: 0, clientX: 360, clientY: 260 });
		expect(video.paused).toBe(true);
		fireEvent.pointerUp(overlay, { button: 0, clientX: 360, clientY: 260 });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("Frame at 02:05");
		await userEvent.type(within(popover).getByRole("textbox", { name: "Add a comment…" }), "Cut here");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalled());
		expect(client.createArtifactShareThread.mock.calls[0]![1].anchor).toMatchObject({ kind: "video", time: 125.25, region: { x: 0.5, y: 0.5, w: 0.4, h: 0.3 } });
	});

	it("adds a comment on the whole frame at the same moment from its popover's footer", async () => {
		stubSizes();
		stubVideo(250);
		const anchor = videoCommentAnchor(83.5, { x: 0.05, y: 0.05, w: 0.2, h: 0.2 });
		const client = fakeClient([thread("t1", anchor, "Typo in the title card")]);
		await renderWorkspace(client, "video");
		act(() => { document.querySelector("video")!.dispatchEvent(new Event("loadedmetadata")); });
		await userEvent.click(badges()[0]!);
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		await userEvent.click(within(popover).getByTestId("artifact-comments-add"));
		await userEvent.type(await within(popover).findByRole("textbox", { name: "Add a comment…" }), "Also the colour");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalled());
		expect(client.createArtifactShareThread.mock.calls[0]![1].anchor).toEqual(videoCommentAnchor(83.5));
	});
});

describe("comments on a text page", () => {
	it("puts the bridge in text mode and labels a picked range of lines", async () => {
		const lines = { path: "body>main:1>pre:1>code:1>span:12", tag: "span", textHash: "0123456789abcdef", excerpt: "retry()", kind: "lines" as const, lines: { start: 12, end: 14 } };
		const client = fakeClient([]);
		await renderWorkspace(client, "text");
		const frame = screen.getByTestId("published-frame") as HTMLIFrameElement;
		const post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
		act(() => { frame.dispatchEvent(new Event("load")); });
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		const received: unknown[] = [];
		port.onmessage = (event: MessageEvent) => { received.push(event.data); };
		port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION });
		await waitFor(() => expect(received).toContainEqual({ type: "mode", on: true, kind: "text" }));
		port.postMessage({ type: "pick", anchor: lines });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("Lines 12–14");
		await userEvent.type(within(popover).getByRole("textbox", { name: "Add a comment…" }), "Retry loop");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread.mock.calls[0]![1]).toMatchObject({ anchor: lines }));
		port.close();
	});
});

describe("one player for the viewer and comment mode", () => {
	const key = mediaPlaybackKey("sess-1", "demo.mp4");
	const daemonSrc = "http://127.0.0.1:3500/api/v1/sessions/sess-1/preview/files/demo.mp4?source=artifact&raw=true&v=7";

	it("plays the viewer's own file where it was, without the share host, while it is the published version", async () => {
		stubSizes();
		const video = stubVideo(250);
		rememberMediaSource(key, daemonSrc, "anonymous");
		rememberMediaTime(key, 42);
		await renderWorkspace(fakeClient([thread("t1", videoCommentAnchor(5), "Intro drags")]), "video", { mediaKey: key });
		const element = document.querySelector("video")!;
		expect(element.getAttribute("src")).toBe(daemonSrc);
		expect(element.getAttribute("crossorigin")).toBe("anonymous");
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		expect(video.time).toBe(42);
		// Its badges are there as on the published copy.
		expect(screen.getAllByTestId("artifact-comments-timeline-point")).toHaveLength(1);
		// Where it is now is kept for the viewer to come back to.
		video.time = 61;
		act(() => { element.dispatchEvent(new Event("timeupdate")); });
		expect(mediaTime(key)).toBe(61);
	});

	it("falls back to the published copy when the viewer's file fails to load", async () => {
		stubSizes();
		stubVideo(250);
		rememberMediaSource(key, daemonSrc, "anonymous");
		await renderWorkspace(fakeClient([]), "video", { mediaKey: key });
		act(() => { document.querySelector("video")!.dispatchEvent(new Event("error")); });
		await waitFor(() => expect(document.querySelector("video")!.getAttribute("src")).toBe("https://share.test/file"));
	});

	it("falls back to the published copy when the viewer's file hasn't loaded soon (its connections are busy)", async () => {
		stubSizes();
		stubVideo(250);
		rememberMediaSource(key, daemonSrc, "anonymous");
		await renderWorkspace(fakeClient([]), "video", { mediaKey: key });
		expect(document.querySelector("video")!.getAttribute("src")).toBe(daemonSrc);
		// No metadata ever comes.
		await waitFor(() => expect(document.querySelector("video")!.getAttribute("src")).toBe("https://share.test/file"), { timeout: 4000 });
	});

	it("keeps the viewer's file once it has loaded, however long the page stays open", async () => {
		stubSizes();
		stubVideo(250);
		rememberMediaSource(key, daemonSrc, "anonymous");
		await renderWorkspace(fakeClient([]), "video", { mediaKey: key });
		act(() => { document.querySelector("video")!.dispatchEvent(new Event("loadedmetadata")); });
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 2800)); });
		expect(document.querySelector("video")!.getAttribute("src")).toBe(daemonSrc);
	});

	it("shows the published copy when the local file changed since it was published", async () => {
		stubSizes();
		stubVideo(250);
		rememberMediaSource(key, daemonSrc, "anonymous");
		rememberMediaTime(key, 42);
		await renderWorkspace(fakeClient([]), "video", { mediaKey: key, outdated: true });
		const element = document.querySelector("video")!;
		expect(element.getAttribute("src")).toBe("https://share.test/file");
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		// Another version: not the local file's moment.
		expect(element.currentTime).toBe(0);
	});

	it("the viewer's player: a click on the frame plays and pauses and never starts a comment; mute and full screen", async () => {
		stubSizes();
		const video = stubVideo(250);
		rememberMediaSource(key, daemonSrc);
		rememberMediaTime(key, 30);
		const requestFullscreen = vi.fn().mockResolvedValue(undefined);
		Object.defineProperty(HTMLElement.prototype, "requestFullscreen", { configurable: true, value: requestFullscreen });
		try {
			render(<ArtifactVideoPlayer positionKey={key} src={daemonSrc} title="demo.mp4" />);
			const element = document.querySelector("video")!;
			expect(element.controls).toBe(false);
			act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
			// Back where comment mode (or this viewer) left it.
			expect(video.time).toBe(30);
			const overlay = screen.getByTestId("artifact-comments-media-overlay");
			fireEvent.pointerDown(overlay, { button: 0, clientX: 100, clientY: 150 });
			fireEvent.pointerUp(overlay, { button: 0, clientX: 100, clientY: 150 });
			expect(video.paused).toBe(false);
			// A drag is no box here: it plays or pauses too, and nothing is drawn.
			fireEvent.pointerDown(overlay, { button: 0, clientX: 200, clientY: 200 });
			fireEvent.pointerMove(overlay, { button: 0, clientX: 360, clientY: 260 });
			expect(screen.queryByTestId("artifact-comments-media-draft")).not.toBeInTheDocument();
			fireEvent.pointerUp(overlay, { button: 0, clientX: 360, clientY: 260 });
			expect(video.paused).toBe(true);
			expect(screen.queryByTestId("artifact-comments-timeline-point")).not.toBeInTheDocument();

			await userEvent.click(screen.getByRole("button", { name: "Mute" }));
			expect(element.muted).toBe(true);
			act(() => { element.dispatchEvent(new Event("volumechange")); });
			expect(screen.getByRole("button", { name: "Unmute" })).toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Full screen" }));
			expect(requestFullscreen).toHaveBeenCalledTimes(1);
		} finally {
			delete (HTMLElement.prototype as { requestFullscreen?: unknown }).requestFullscreen;
		}
	});
});
