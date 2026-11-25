import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode, Ref } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import type { CloudCpClient } from "../../lib/cloud-cp";
import type { WorkspaceSession } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { ArtifactCommentsWorkspace } from "./ArtifactComments";
import {
	ARTIFACT_COMMENTS_CONNECT,
	ARTIFACT_COMMENTS_PROTOCOL_VERSION,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
} from "../../../shared/artifact-comments";
import { useArtifactComments } from "./useArtifactComments";
import { useCommentBridge } from "./useCommentBridge";

const mocks = vi.hoisted(() => ({ send: vi.fn(), navigate: vi.fn(), client: {} as Record<string, unknown> }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cp.test", userId: "owner-1" }) }));
vi.mock("../../hooks/useSendToSessionWorker", () => ({ useSendToSessionWorker: () => mocks.send }));
vi.mock("../../lib/navigate-to-session", () => ({ useNavigateToSession: () => mocks.navigate }));
// The published page, framed from the share host: a plain frame here.
vi.mock("../SharedArtifactFrame", () => ({
	SharedArtifactFrame: ({ frameRef, linkId, title }: { frameRef?: Ref<HTMLIFrameElement>; linkId: string; title: string }) => (
		<iframe data-link-id={linkId} data-testid="published-frame" ref={frameRef} title={title} />
	),
}));

const sam = { id: "sam-1", name: "Sam" };
const aditi = { id: "owner-1", name: "Aditi" };

function thread(over: Partial<ArtifactCommentThread> = {}): ArtifactCommentThread {
	return {
		id: "t1",
		version: 3,
		status: "open",
		anchor: { path: "body>main:1>h1:1", tag: "h1", textHash: "9f2c00000000abcd", excerpt: "Cloudflare staging setup for shared video streaming" },
		createdAt: "2026-11-23T08:00:00Z",
		updatedAt: "2026-11-23T09:00:00Z",
		resolvedBy: null,
		resolvedAt: null,
		sentToAgentAt: null,
		sentToAgentCount: 0,
		comments: [
			{ id: "c1", threadId: "t1", clientId: "k1", author: sam, body: "Can we say \"staging only\" in the title? People will skim this and assume it's production.", createdAt: "2026-11-23T08:00:00Z", editedAt: null, deleted: false },
			{ id: "c2", threadId: "t1", clientId: "k2", author: aditi, body: "Yes, will add.", createdAt: "2026-11-23T09:00:00Z", editedAt: null, deleted: false },
		],
		...over,
	};
}

function snapshot(over: Partial<ArtifactCommentsSnapshot> = {}): ArtifactCommentsSnapshot {
	return { version: 3, currentVersion: 3, revision: 41, you: { userId: "owner-1", isOwner: true }, threads: [thread()], ...over };
}

type CommentMethods = Pick<CloudCpClient, "listArtifactShareComments" | "createArtifactShareThread" | "replyArtifactShareThread" | "setArtifactShareThreadStatus" | "editArtifactShareComment" | "deleteArtifactShareComment" | "markArtifactShareThreadSent">;
type FakeClient = { [K in keyof CommentMethods]: Mock<CommentMethods[K]> };

function fakeClient(snap: ArtifactCommentsSnapshot = snapshot()): FakeClient {
	const client: FakeClient = {
		listArtifactShareComments: vi.fn().mockResolvedValue({ notModified: false, snapshot: snap, etag: "\"r41-v3\"" }),
		createArtifactShareThread: vi.fn(),
		replyArtifactShareThread: vi.fn(),
		setArtifactShareThreadStatus: vi.fn(),
		editArtifactShareComment: vi.fn(),
		deleteArtifactShareComment: vi.fn(),
		markArtifactShareThreadSent: vi.fn().mockResolvedValue({ ...snap.threads[0], sentToAgentAt: "2026-11-23T10:42:00Z", sentToAgentCount: 1 }),
	};
	mocks.client = client;
	return client;
}

const localSession = { id: "sess-1", title: "cloudflare-video-setup", workspaceId: "proj-1", mode: "chat" } as WorkspaceSession;

function wrap(_client: FakeClient) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>{children}</TooltipProvider>
		</QueryClientProvider>
	);
}

function renderWorkspace(client: FakeClient, { outdated = false, withSession = true } = {}) {
	const onClose = vi.fn();
	const Wrapper = wrap(client);
	render(
		<Wrapper>
			<ArtifactCommentsWorkspace
				kind="html"
				onClose={onClose}
				outdated={outdated}
				sendTarget={withSession ? { session: localSession, path: "docs/cloudflare-staging-setup.html", name: "cloudflare-staging-setup.html" } : undefined}
				slug="cf-staging-x7"
				title="cloudflare-staging-setup.html"
			/>
		</Wrapper>,
	);
	return { onClose };
}

/** Connects the frame's bridge, as the share host's script would, and returns its end of the port. */
async function connectBridge(): Promise<MessagePort> {
	const frame = screen.getByTestId("published-frame") as HTMLIFrameElement;
	const post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
	act(() => { frame.dispatchEvent(new Event("load")); });
	const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
	port.onmessage = () => undefined;
	port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION });
	return port;
}

const newAnchor = { path: "body>main:1>p:2", tag: "p", textHash: "0123456789abcdef", excerpt: "Staging first." };

async function expandFirstGroup() {
	const card = await screen.findByRole("button", { expanded: false, name: /staging only/ });
	await userEvent.click(card);
}

beforeEach(() => {
	mocks.send.mockReset();
	mocks.navigate.mockReset();
	useUiStore.getState().clearGlobalToast();
});

afterEach(() => vi.useRealTimers());

describe("ArtifactCommentsWorkspace", () => {
	it("frames the published link and lists its threads grouped by element", async () => {
		const client = fakeClient();
		renderWorkspace(client);
		expect(screen.getByTestId("published-frame")).toHaveAttribute("data-link-id", "cf-staging-x7");
		const panel = await screen.findByRole("complementary", { name: "Comments" });
		expect(await within(panel).findByText(/v3 · 1 open/)).toBeInTheDocument();
		// Collapsed: the first comment and its reply count, no thread count.
		expect(within(panel).getByText("1 reply")).toBeInTheDocument();
		expect(client.listArtifactShareComments).toHaveBeenCalledWith("cf-staging-x7", expect.not.objectContaining({ etag: expect.anything() }));
	});

	it("says it shows the published version when the local file changed", async () => {
		renderWorkspace(fakeClient(), { outdated: true });
		expect(await screen.findByText("Showing the published version (v3)")).toBeInTheDocument();
	});

	it("offers Send to agent to the owner when the file's session is present", async () => {
		renderWorkspace(fakeClient());
		await expandFirstGroup();
		expect(screen.getByRole("button", { name: "Send to agent" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Resolve" })).toBeInTheDocument();
	});

	it("shows no Send to agent without the file's session, not even disabled", async () => {
		renderWorkspace(fakeClient(), { withSession: false });
		await expandFirstGroup();
		expect(screen.getByRole("button", { name: "Resolve" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Send to agent|Send again/ })).not.toBeInTheDocument();
	});

	it("shows no Send to agent to someone who isn't the owner", async () => {
		renderWorkspace(fakeClient(snapshot({ you: { userId: "sam-1", isOwner: false } })));
		await expandFirstGroup();
		expect(screen.queryByRole("button", { name: /Send to agent/ })).not.toBeInTheDocument();
	});

	it("sends the thread with the note, then marks it sent and confirms", async () => {
		const client = fakeClient();
		mocks.send.mockResolvedValue(undefined);
		renderWorkspace(client);
		await expandFirstGroup();
		await userEvent.click(screen.getByRole("button", { name: "Send to agent" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByTestId("send-to-agent-session")).toHaveTextContent("cloudflare-video-setup");
		expect(within(dialog).getByText("cloudflare-staging-setup.html · v3")).toBeInTheDocument();
		expect(within(dialog).getByText("Yes, will add.")).toBeInTheDocument();
		const note = within(dialog).getByLabelText("Note (optional)");
		expect(note).toHaveAttribute("maxLength", "2000");
		await userEvent.type(note, "Rename the title.");
		await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));

		await waitFor(() => expect(client.markArtifactShareThreadSent).toHaveBeenCalledWith("cf-staging-x7", "t1"));
		const [message] = mocks.send.mock.calls[0];
		expect(message).toContain("File: docs/cloudflare-staging-setup.html  (shared link version 3)");
		expect(message).toContain("Owner's note: Rename the title.");
		expect(mocks.send.mock.invocationCallOrder[0]).toBeLessThan(client.markArtifactShareThreadSent.mock.invocationCallOrder[0]);
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		const toast = useUiStore.getState().globalToast;
		expect(toast).toMatchObject({ title: "Sent to cloudflare-video-setup", action: { label: "Open chat" } });
		toast?.action?.run();
		expect(mocks.navigate).toHaveBeenCalledWith("proj-1", "sess-1", undefined);
	});

	it("keeps the dialog open with the error and does not mark the thread when sending fails", async () => {
		const client = fakeClient();
		mocks.send.mockRejectedValue(new Error("The session isn't running."));
		renderWorkspace(client);
		await expandFirstGroup();
		await userEvent.click(screen.getByRole("button", { name: "Send to agent" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("The session isn't running.");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(client.markArtifactShareThreadSent).not.toHaveBeenCalled();
	});

	it("labels a thread already sent and offers Send again", async () => {
		renderWorkspace(fakeClient(snapshot({ threads: [thread({ sentToAgentAt: "2026-11-23T10:42:00Z" })] })));
		await expandFirstGroup();
		expect(screen.getByText(/^Sent to agent · /)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Send again" })).toBeInTheDocument();
	});

	it("names a deleted account Former user and a nameless one Orchestrator user", async () => {
		const base = thread();
		renderWorkspace(fakeClient(snapshot({ threads: [thread({
			status: "resolved",
			resolvedBy: null,
			resolvedAt: "2026-11-23T10:00:00Z",
			comments: [{ ...base.comments[0], author: { id: null, name: "" } }, { ...base.comments[1], author: { id: "u-9", name: "" } }],
		})] })));
		await userEvent.click(await screen.findByRole("button", { name: /Resolved \(1\)/ }));
		expect(screen.getByText("Resolved by Former user")).toBeInTheDocument();
		expect(screen.getByText("Former user")).toBeInTheDocument();
		expect(screen.getByText("Orchestrator user")).toBeInTheDocument();
	});

	it("hides the frame and panel when access is lost", async () => {
		const { CloudCpError } = await import("../../lib/cloud-cp");
		const client = fakeClient();
		client.listArtifactShareComments.mockRejectedValue(new CloudCpError("not found", { status: 404, code: "not_found" }));
		renderWorkspace(client);
		expect(await screen.findByText("You no longer have access to this link")).toBeInTheDocument();
		expect(screen.queryByTestId("published-frame")).not.toBeInTheDocument();
	});

	it("shows a reply at once and keeps the text with Retry when it fails", async () => {
		const client = fakeClient();
		client.replyArtifactShareThread.mockRejectedValue(new Error("offline"));
		renderWorkspace(client);
		await expandFirstGroup();
		const box = screen.getByRole("textbox", { name: "Reply…" });
		await userEvent.type(box, "On it{Enter}");
		expect(await screen.findByText("Couldn't post.")).toBeInTheDocument();
		expect(box).toHaveValue("On it");
		expect(client.replyArtifactShareThread).toHaveBeenCalledWith("cf-staging-x7", "t1", { body: "On it", clientId: expect.any(String) });
	});
});

describe("ArtifactCommentsPanel cards and threads", () => {
	it("opens a box at a new element's place and keeps a failed post's text with Retry under the same client id", async () => {
		const client = fakeClient();
		client.createArtifactShareThread
			.mockRejectedValueOnce(new Error("offline"))
			.mockResolvedValueOnce(thread({ id: "t9", anchor: newAnchor, comments: [{ id: "c9", threadId: "t9", clientId: "k9", author: aditi, body: "Which account?", createdAt: "2026-11-23T11:00:00Z", editedAt: null, deleted: false }] }));
		renderWorkspace(client);
		await screen.findByRole("button", { expanded: false, name: /staging only/ });
		const port = await connectBridge();
		port.postMessage({ type: "pick", anchor: newAnchor });
		const box = await screen.findByRole("textbox", { name: "Add a comment…" });
		// Never a copy of the element's text.
		expect(screen.queryByText("Staging first.")).not.toBeInTheDocument();
		await userEvent.type(box, "Which account?");
		await userEvent.click(screen.getByRole("button", { name: "Comment" }));
		expect(await screen.findByText("Couldn't post.")).toBeInTheDocument();
		expect(screen.getByRole("textbox", { name: "Add a comment…" })).toHaveValue("Which account?");
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalledTimes(2));
		const [first, second] = client.createArtifactShareThread.mock.calls.map(([, input]) => input);
		expect(second).toEqual(first);
		expect(first).toMatchObject({ version: 3, anchor: newAnchor, body: "Which account?" });
		await waitFor(() => expect(screen.queryByRole("textbox", { name: "Add a comment…" })).not.toBeInTheDocument());
		port.close();
	});

	it("pops up nothing on an artifact with no comments until an element is picked", async () => {
		const client = fakeClient(snapshot({ threads: [] }));
		renderWorkspace(client);
		await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalled());
		const port = await connectBridge();
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
		// Comment mode is on (the frame is framed for picking), but no popover and no empty state.
		expect(screen.getByTestId("published-frame")).toBeInTheDocument();
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();
		expect(screen.queryByTestId("artifact-comments-empty")).not.toBeInTheDocument();
		port.postMessage({ type: "pick", anchor: newAnchor });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByRole("textbox", { name: "Add a comment…" })).toBeInTheDocument();
		await userEvent.click(within(popover).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument());
		port.close();
	});

	it("floats the comments over the artifact instead of beside it", async () => {
		renderWorkspace(fakeClient());
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(popover).toHaveClass("absolute");
		expect(popover.parentElement).toContainElement(screen.getByTestId("published-frame"));
	});

	it("shows an element's threads folded below the box when writing on it", async () => {
		renderWorkspace(fakeClient());
		await screen.findByRole("button", { expanded: false, name: /staging only/ });
		const port = await connectBridge();
		port.postMessage({ type: "pick", anchor: thread().anchor });
		expect(await screen.findByRole("textbox", { name: "Add a comment…" })).toBeInTheDocument();
		expect(screen.getByText("1 reply")).toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Reply…" })).not.toBeInTheDocument();
		expect(screen.queryByText("Yes, will add.")).not.toBeInTheDocument();
		port.close();
	});

	it("collapses an element with several threads to its newest thread and +N more, with no thread count", async () => {
		const other = thread({ id: "t2", createdAt: "2026-11-23T09:30:00Z", comments: [{ id: "c3", threadId: "t2", clientId: "k3", author: sam, body: "Title is too long for the sidebar.", createdAt: "2026-11-23T09:30:00Z", editedAt: null, deleted: false }] });
		renderWorkspace(fakeClient(snapshot({ threads: [thread(), other] })));
		const card = await screen.findByRole("button", { expanded: false, name: /too long/ });
		expect(within(card).getByText("+1 more")).toBeInTheDocument();
		expect(within(card).queryByText(/thread/i)).not.toBeInTheDocument();
		expect(within(card).queryByText(/staging only/)).not.toBeInTheDocument();
	});

	it("opens the newest thread of an expanded element and folds the others to one line", async () => {
		const other = thread({ id: "t2", createdAt: "2026-11-23T09:30:00Z", comments: [{ id: "c3", threadId: "t2", clientId: "k3", author: sam, body: "Title is too long for the sidebar.", createdAt: "2026-11-23T09:30:00Z", editedAt: null, deleted: false }] });
		renderWorkspace(fakeClient(snapshot({ threads: [thread(), other] })));
		await userEvent.click(await screen.findByRole("button", { expanded: false, name: /too long/ }));
		const threadIds = () => [...document.querySelectorAll("[data-thread-id]")].map((node) => [node.getAttribute("data-thread-id"), node.hasAttribute("data-thread-folded")]);
		// Newest first and open; the older thread is one line.
		expect(threadIds()).toEqual([["t2", false], ["t1", true]]);
		expect(screen.getAllByRole("textbox", { name: "Reply…" })).toHaveLength(1);
		await userEvent.click(screen.getByRole("button", { expanded: false, name: /staging only/ }));
		expect(threadIds()).toEqual([["t2", false], ["t1", false]]);
		await userEvent.click(screen.getAllByRole("button", { name: "Collapse thread" })[0]);
		expect(threadIds()).toEqual([["t2", true], ["t1", false]]);
	});

	it("labels resolved threads with who resolved them and offers Reopen, never Send to agent", async () => {
		renderWorkspace(fakeClient(snapshot({ threads: [thread({ status: "resolved", resolvedBy: aditi, resolvedAt: "2026-11-23T10:00:00Z" })] })));
		await userEvent.click(await screen.findByRole("button", { name: /Resolved \(1\)/ }));
		expect(screen.getByText("Resolved by Aditi")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Reopen" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Send to agent|Send again/ })).not.toBeInTheDocument();
		expect(screen.queryByRole("textbox", { name: "Reply…" })).not.toBeInTheDocument();
	});

	it("drops deleted replies, leaves Comment deleted for a deleted first comment, and marks edits", async () => {
		const base = thread();
		renderWorkspace(fakeClient(snapshot({ threads: [thread({
			comments: [
				{ ...base.comments[0], body: "", deleted: true },
				{ ...base.comments[1], editedAt: "2026-11-23T09:30:00Z" },
				{ id: "c3", threadId: "t1", clientId: "k3", author: sam, body: "", createdAt: "2026-11-23T09:40:00Z", editedAt: null, deleted: true },
			],
		})] })));
		const card = await screen.findByRole("button", { expanded: false, name: /Comment deleted/ });
		expect(within(card).getByText("1 reply")).toBeInTheDocument();
		await userEvent.click(card);
		expect(screen.getAllByText("Comment deleted")).toHaveLength(1);
		expect(document.querySelector('[data-comment-id="c3"]')).toBeNull();
		expect(screen.getByText("edited")).toBeInTheDocument();
	});

	it("offers the owner Delete, in red, on someone else's comment and Edit only on their own", async () => {
		renderWorkspace(fakeClient());
		await expandFirstGroup();
		const [samMenu, ownMenu] = screen.getAllByRole("button", { name: "Comment actions" });
		await userEvent.click(samMenu);
		expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Delete" })).toHaveClass("text-destructive");
		await userEvent.keyboard("{Escape}");
		await userEvent.click(ownMenu);
		expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
	});
});

describe("useArtifactComments", () => {
	it("keeps the same snapshot when the control plane answers no change", async () => {
		const client = fakeClient();
		const { result } = renderHook(() => useArtifactComments("cf-staging-x7"), { wrapper: wrap(client) });
		await waitFor(() => expect(result.current.snapshot).toBeDefined());
		const first = result.current.snapshot;
		client.listArtifactShareComments.mockResolvedValue({ notModified: true, etag: "\"r41-v3\"" });
		await act(async () => { await result.current.refetch(); });
		expect(client.listArtifactShareComments).toHaveBeenLastCalledWith("cf-staging-x7", expect.objectContaining({ etag: "\"r41-v3\"" }));
		expect(result.current.snapshot).toBe(first);
		expect(result.current.openCount).toBe(1);
	});

	it("polls every 30 seconds only while the panel is open", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const client = fakeClient();
		const { rerender } = renderHook(({ open }) => useArtifactComments("cf-staging-x7", { panelOpen: open }), { wrapper: wrap(client), initialProps: { open: false } });
		await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalledTimes(1));
		await act(async () => { await vi.advanceTimersByTimeAsync(61_000); });
		expect(client.listArtifactShareComments).toHaveBeenCalledTimes(1);
		rerender({ open: true });
		await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalledTimes(2));
		await act(async () => { await vi.advanceTimersByTimeAsync(30_500); });
		await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalledTimes(3));
	});
});

describe("useCommentBridge", () => {
	it("hands the frame a private port on load and listens only on it", async () => {
		const frame = document.createElement("iframe");
		document.body.append(frame);
		const post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
		const onPick = vi.fn();
		const { result } = renderHook(() => useCommentBridge(frame, { onPick }));
		act(() => { frame.dispatchEvent(new Event("load")); });
		expect(post).toHaveBeenCalledWith({ type: ARTIFACT_COMMENTS_CONNECT, protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION }, "*", [expect.any(MessagePort)]);
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		const bridgeSide = vi.fn();
		port.onmessage = bridgeSide;
		// A window message never counts, even one shaped like the bridge's.
		act(() => { window.dispatchEvent(new MessageEvent("message", { data: { type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" } } })); });
		expect(onPick).not.toHaveBeenCalled();
		port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION });
		await waitFor(() => expect(result.current.status).toBe("ready"));
		port.postMessage({ type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" } });
		await waitFor(() => expect(onPick).toHaveBeenCalledWith({ path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" }));
		// Theme goes over right after ready.
		await waitFor(() => expect(bridgeSide.mock.calls.some(([event]) => (event as MessageEvent).data.type === "theme")).toBe(true));
		port.close();
		frame.remove();
	});

	it("ignores a ready for another protocol version", async () => {
		const frame = document.createElement("iframe");
		document.body.append(frame);
		const post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
		const { result } = renderHook(() => useCommentBridge(frame, {}));
		act(() => { frame.dispatchEvent(new Event("load")); });
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION + 1 });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(result.current.status).toBe("connecting");
		port.close();
		frame.remove();
	});

	it("says the page can't be pointed at when no ready arrives in 3 seconds", async () => {
		vi.useFakeTimers();
		const frame = document.createElement("iframe");
		document.body.append(frame);
		vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
		const { result } = renderHook(() => useCommentBridge(frame, {}));
		act(() => { frame.dispatchEvent(new Event("load")); });
		expect(result.current.status).toBe("connecting");
		act(() => { vi.advanceTimersByTime(3_001); });
		expect(result.current.status).toBe("unsupported");
		frame.remove();
	});
});
