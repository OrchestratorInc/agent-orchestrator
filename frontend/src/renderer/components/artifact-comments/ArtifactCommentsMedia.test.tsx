import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode, Ref } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { CloudCpClient } from "../../lib/cloud-cp";
import { TooltipProvider } from "../ui/tooltip";
import { ArtifactCommentsWorkspace, artifactCommentsKind, type ArtifactCommentsKind } from "./ArtifactComments";
import {
	ARTIFACT_COMMENTS_PROTOCOL_VERSION,
	imageCommentAnchor,
	videoCommentAnchor,
	type ArtifactCommentAnchor,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
} from "../../../shared/artifact-comments";

const mocks = vi.hoisted(() => ({ client: {} as Record<string, unknown> }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test", userId: "owner-1" }) }));
vi.mock("../../hooks/useSendToSessionWorker", () => ({ useSendToSessionWorker: () => vi.fn() }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => vi.fn() }));
// The published file: a plain frame, or the comment view's own media from its content URL.
vi.mock("../SharedArtifactFrame", () => ({
	SharedArtifactFrame: ({ frameRef, renderMedia, title }: { frameRef?: Ref<HTMLIFrameElement>; renderMedia?: (url: string, shown: () => void) => ReactNode; title: string }) =>
		renderMedia ? <>{renderMedia("https://share.test/file", () => undefined)}</> : <iframe data-testid="published-frame" ref={frameRef} title={title} />,
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

async function renderWorkspace(client: FakeClient, kind: ArtifactCommentsKind) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<ArtifactCommentsWorkspace kind={kind} onClose={vi.fn()} outdated={false} slug="cf-staging-x7" title="file" />
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
	vi.spyOn(HTMLMediaElement.prototype, "duration", "get").mockReturnValue(duration);
	vi.spyOn(HTMLMediaElement.prototype, "currentTime", "get").mockImplementation(() => state.time);
	vi.spyOn(HTMLMediaElement.prototype, "currentTime", "set").mockImplementation((value: number) => { state.time = value; });
	vi.spyOn(HTMLMediaElement.prototype, "paused", "get").mockImplementation(() => state.paused);
	vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) { state.paused = true; this.dispatchEvent(new Event("pause")); });
	vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) { state.paused = false; this.dispatchEvent(new Event("play")); return Promise.resolve(); });
	return state;
}

beforeEach(() => vi.restoreAllMocks());
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
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("Point near top-left");
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
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("Region near bottom-right");
	});
});

describe("comments on a video", () => {
	it("comments at the current moment, paused, and seeks to a group from its timeline dot", async () => {
		stubSizes();
		const video = stubVideo(250);
		const client = fakeClient([thread("t1", videoCommentAnchor(83.5, { x: 0.05, y: 0.05, w: 0, h: 0 }), "Typo in the title card")]);
		await renderWorkspace(client, "video");
		const element = document.querySelector("video")!;
		expect(element.controls).toBe(false);
		act(() => { element.dispatchEvent(new Event("loadedmetadata")); });
		await userEvent.click(screen.getAllByRole("button", { name: "At 01:23, point near top-left: 1 open" })[0]!);
		expect(video.time).toBe(83.5);
		expect(video.paused).toBe(true);
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByText("Typo in the title card")).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Play" }));
		expect(video.paused).toBe(false);
		video.time = 125.25;
		act(() => { element.dispatchEvent(new Event("timeupdate")); });
		await userEvent.click(screen.getByRole("button", { name: "Comment at 02:05" }));
		expect(video.paused).toBe(true);
		const box = await within(popover).findByRole("textbox", { name: "Add a comment…" });
		expect(within(popover).getByTestId("artifact-comments-where")).toHaveTextContent("At 02:05");
		await userEvent.type(box, "Cut here");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalled());
		const input = client.createArtifactShareThread.mock.calls[0]![1];
		expect(input.anchor).toMatchObject({ kind: "video", time: 125.25 });
		expect(input.anchor).not.toHaveProperty("region");
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
