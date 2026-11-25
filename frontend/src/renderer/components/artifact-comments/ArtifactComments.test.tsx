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
	port.onmessage = (event: MessageEvent) => { bridgeReceived.push(event.data); };
	port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION });
	return port;
}

/** What the app has sent the bridge since the test began. */
const bridgeReceived: unknown[] = [];

const newAnchor = { path: "body>main:1>p:2", tag: "p", textHash: "0123456789abcdef", excerpt: "Staging first." };

/** Opens an element's comments as clicking its count would; returns the popover. */
async function openElement(port: MessagePort, path = thread().anchor.path): Promise<HTMLElement> {
	port.postMessage({ type: "marker", path });
	return screen.findByRole("complementary", { name: "Comments" });
}

/** Renders the workspace, waits for its comments and connects the bridge. */
async function ready(client: FakeClient, options?: Parameters<typeof renderWorkspace>[1]) {
	const rendered = renderWorkspace(client, options);
	await waitFor(() => expect(client.listArtifactShareComments).toHaveBeenCalled());
	const port = await connectBridge();
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
	return { ...rendered, port };
}

const twoThreads = () => [
	thread(),
	thread({ id: "t2", createdAt: "2026-11-23T09:30:00Z", comments: [{ id: "c3", threadId: "t2", clientId: "k3", author: sam, body: "Title is too long for the sidebar.", createdAt: "2026-11-23T09:30:00Z", editedAt: null, deleted: false }] }),
];

beforeEach(() => {
	mocks.send.mockReset();
	mocks.navigate.mockReset();
	useUiStore.getState().clearGlobalToast();
	bridgeReceived.length = 0;
});

afterEach(() => vi.useRealTimers());

describe("ArtifactCommentsWorkspace", () => {
	it("frames the published link in comment mode: counts on the elements and no popover", async () => {
		const client = fakeClient(snapshot({ threads: twoThreads() }));
		const { port } = await ready(client);
		expect(screen.getByTestId("published-frame")).toHaveAttribute("data-link-id", "cf-staging-x7");
		expect(client.listArtifactShareComments).toHaveBeenCalledWith("cf-staging-x7", expect.not.objectContaining({ etag: expect.anything() }));
		await waitFor(() => expect(bridgeReceived).toContainEqual({ type: "anchors", anchors: [{ path: thread().anchor.path, textHash: thread().anchor.textHash, count: 2 }] }));
		expect(bridgeReceived).toContainEqual({ type: "mode", on: true, kind: "html" });
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();
		port.close();
	});

	it("says it shows the published version when the local file changed", async () => {
		renderWorkspace(fakeClient(), { outdated: true });
		expect(await screen.findByText("Showing the published version (v3)")).toBeInTheDocument();
	});

	it("opens only the clicked element's comments, beside it, and selects it on the page", async () => {
		const other = thread({ id: "t5", anchor: newAnchor, comments: [{ id: "c5", threadId: "t5", clientId: "k5", author: sam, body: "Which account?", createdAt: "2026-11-23T08:30:00Z", editedAt: null, deleted: false }] });
		const { port } = await ready(fakeClient(snapshot({ threads: [thread(), other] })));
		const popover = await openElement(port);
		expect(await within(popover).findByText(/staging only/)).toBeInTheDocument();
		expect(within(popover).getByText(/1 open/)).toBeInTheDocument();
		expect(within(popover).queryByText("Which account?")).not.toBeInTheDocument();
		await waitFor(() => expect(bridgeReceived).toContainEqual({ type: "select", path: thread().anchor.path }));
		port.close();
	});

	it("closes the popover with ✕ or Esc and stays in comment mode; a second Esc leaves it", async () => {
		const { onClose, port } = await ready(fakeClient());
		const popover = await openElement(port);
		await userEvent.click(within(popover).getByRole("button", { name: "Close comments" }));
		await waitFor(() => expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument());
		await waitFor(() => expect(bridgeReceived).toContainEqual({ type: "clear" }));
		expect(onClose).not.toHaveBeenCalled();
		await openElement(port);
		port.postMessage({ type: "escape" });
		await waitFor(() => expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument());
		expect(onClose).not.toHaveBeenCalled();
		port.postMessage({ type: "escape" });
		await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
		port.close();
	});

	it("offers Send to agent to the owner when the file's session is present", async () => {
		const { port } = await ready(fakeClient());
		const popover = await openElement(port);
		expect(await within(popover).findByRole("button", { name: "Send to agent" })).toBeInTheDocument();
		expect(within(popover).getByRole("button", { name: "Resolve" })).toBeInTheDocument();
		port.close();
	});

	it("shows no Send to agent without the file's session, not even disabled", async () => {
		const { port } = await ready(fakeClient(), { withSession: false });
		const popover = await openElement(port);
		expect(await within(popover).findByRole("button", { name: "Resolve" })).toBeInTheDocument();
		expect(within(popover).queryByRole("button", { name: /Send to agent|Send again/ })).not.toBeInTheDocument();
		port.close();
	});

	it("shows no Send to agent to someone who isn't the owner", async () => {
		const { port } = await ready(fakeClient(snapshot({ you: { userId: "sam-1", isOwner: false } })));
		const popover = await openElement(port);
		expect(await within(popover).findByRole("button", { name: "Resolve" })).toBeInTheDocument();
		expect(within(popover).queryByRole("button", { name: /Send to agent/ })).not.toBeInTheDocument();
		port.close();
	});

	it("sends the thread with the note, then marks it sent and confirms", async () => {
		const client = fakeClient();
		mocks.send.mockResolvedValue(undefined);
		const { port } = await ready(client);
		const popover = await openElement(port);
		await userEvent.click(await within(popover).findByRole("button", { name: "Send to agent" }));
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
		port.close();
	});

	it("keeps the dialog open with the error and does not mark the thread when sending fails", async () => {
		const client = fakeClient();
		mocks.send.mockRejectedValue(new Error("The session isn't running."));
		const { port } = await ready(client);
		const popover = await openElement(port);
		await userEvent.click(await within(popover).findByRole("button", { name: "Send to agent" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Send" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("The session isn't running.");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(client.markArtifactShareThreadSent).not.toHaveBeenCalled();
		port.close();
	});

	it("labels a thread already sent and offers Send again", async () => {
		const { port } = await ready(fakeClient(snapshot({ threads: [thread({ sentToAgentAt: "2026-11-23T10:42:00Z" })] })));
		const popover = await openElement(port);
		expect(await within(popover).findByText(/^Sent to agent · /)).toBeInTheDocument();
		expect(within(popover).getByRole("button", { name: "Send again" })).toBeInTheDocument();
		port.close();
	});

	it("names a deleted account Former user and a nameless one Orchestrator user", async () => {
		const base = thread();
		const { port } = await ready(fakeClient(snapshot({ threads: [thread({
			status: "resolved",
			resolvedBy: null,
			resolvedAt: "2026-11-23T10:00:00Z",
			comments: [{ ...base.comments[0], author: { id: null, name: "" } }, { ...base.comments[1], author: { id: "u-9", name: "" } }],
		})] })));
		const popover = await openElement(port);
		await userEvent.click(await within(popover).findByRole("button", { name: /1 resolved on this element · Show/ }));
		expect(within(popover).getByText("Resolved by Former user")).toBeInTheDocument();
		expect(within(popover).getByText("Former user")).toBeInTheDocument();
		expect(within(popover).getByText("Orchestrator user")).toBeInTheDocument();
		port.close();
	});

	it("hides the frame and comments when access is lost", async () => {
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
		const { port } = await ready(client);
		const popover = await openElement(port);
		const box = await within(popover).findByRole("textbox", { name: "Reply…" });
		await userEvent.type(box, "On it{Enter}");
		expect(await within(popover).findByText("Couldn't post.")).toBeInTheDocument();
		expect(box).toHaveValue("On it");
		expect(client.replyArtifactShareThread).toHaveBeenCalledWith("cf-staging-x7", "t1", { body: "On it", clientId: expect.any(String) });
		port.close();
	});
});

describe("ArtifactCommentsPanel: one element's comments", () => {
	it("pops up nothing on an artifact with no comments until an element is picked; Cancel closes it", async () => {
		const { port } = await ready(fakeClient(snapshot({ threads: [] })));
		expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument();
		port.postMessage({ type: "pick", anchor: newAnchor });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByRole("textbox", { name: "Add a comment…" })).toHaveFocus();
		await userEvent.click(within(popover).getByRole("button", { name: "Cancel" }));
		await waitFor(() => expect(screen.queryByRole("complementary", { name: "Comments" })).not.toBeInTheDocument());
		port.close();
	});

	it("keeps a failed post's text with Retry under the same client id, then shows the new thread", async () => {
		const posted = thread({ id: "t9", anchor: newAnchor, createdAt: "2026-11-23T11:00:00Z", comments: [{ id: "c9", threadId: "t9", clientId: "k9", author: aditi, body: "Which account?", createdAt: "2026-11-23T11:00:00Z", editedAt: null, deleted: false }] });
		const client = fakeClient();
		client.createArtifactShareThread
			.mockRejectedValueOnce(new Error("offline"))
			.mockImplementationOnce(async () => {
				client.listArtifactShareComments.mockResolvedValue({ notModified: false, snapshot: snapshot({ threads: [thread(), posted] }), etag: "\"r42-v3\"" });
				return posted;
			});
		const { port } = await ready(client);
		port.postMessage({ type: "pick", anchor: newAnchor });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		const box = within(popover).getByRole("textbox", { name: "Add a comment…" });
		// Never a copy of the element's text.
		expect(within(popover).queryByText("Staging first.")).not.toBeInTheDocument();
		await userEvent.type(box, "Which account?");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		expect(await within(popover).findByText("Couldn't post.")).toBeInTheDocument();
		expect(within(popover).getByRole("textbox", { name: "Add a comment…" })).toHaveValue("Which account?");
		await userEvent.click(within(popover).getByRole("button", { name: "Retry" }));
		await waitFor(() => expect(client.createArtifactShareThread).toHaveBeenCalledTimes(2));
		const [first, second] = client.createArtifactShareThread.mock.calls.map(([, input]) => input);
		expect(second).toEqual(first);
		expect(first).toMatchObject({ version: 3, anchor: newAnchor, body: "Which account?" });
		await waitFor(() => expect(within(popover).getByText("Which account?")).toBeInTheDocument());
		expect(within(popover).getByRole("textbox", { name: "Add a comment…" })).toHaveValue("");
		port.close();
	});

	it("shows an element's threads with a one-line box under them when it is clicked", async () => {
		const { port } = await ready(fakeClient());
		port.postMessage({ type: "pick", anchor: thread().anchor });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		expect(within(popover).getByText("Yes, will add.")).toBeInTheDocument();
		const box = within(popover).getByRole("textbox", { name: "Add a comment…" });
		expect(box).not.toHaveFocus();
		expect(within(popover).queryByRole("button", { name: "Comment" })).not.toBeInTheDocument();
		await userEvent.type(box, "Another point");
		expect(within(popover).getByRole("button", { name: "Comment" })).toBeEnabled();
		port.close();
	});

	it("keeps one thread open: the newest, until another is clicked", async () => {
		const { port } = await ready(fakeClient(snapshot({ threads: twoThreads() })));
		const popover = await openElement(port);
		const threadIds = () => [...popover.querySelectorAll("[data-thread-id]")].map((node) => [node.getAttribute("data-thread-id"), node.hasAttribute("data-thread-folded")]);
		await waitFor(() => expect(threadIds()).toEqual([["t2", false], ["t1", true]]));
		expect(within(popover).getAllByRole("textbox", { name: "Reply…" })).toHaveLength(1);
		const folded = within(popover).getByRole("button", { expanded: false, name: /staging only/ });
		expect(within(folded).getByText("1 reply")).toBeInTheDocument();
		await userEvent.click(folded);
		expect(threadIds()).toEqual([["t2", true], ["t1", false]]);
		expect(within(within(popover).getByRole("button", { expanded: false, name: /too long/ })).getByText("No replies")).toBeInTheDocument();
		port.close();
	});

	it("opens a thread you just posted on an element, as its newest", async () => {
		const posted = thread({ id: "t9", createdAt: "2026-11-23T11:00:00Z", comments: [{ id: "c9", threadId: "t9", clientId: "k9", author: aditi, body: "Which account?", createdAt: "2026-11-23T11:00:00Z", editedAt: null, deleted: false }] });
		const client = fakeClient();
		client.createArtifactShareThread.mockImplementationOnce(async () => {
			client.listArtifactShareComments.mockResolvedValue({ notModified: false, snapshot: snapshot({ threads: [thread(), posted] }), etag: "\"r42-v3\"" });
			return posted;
		});
		const { port } = await ready(client);
		port.postMessage({ type: "pick", anchor: thread().anchor });
		const popover = await screen.findByRole("complementary", { name: "Comments" });
		await userEvent.type(within(popover).getByRole("textbox", { name: "Add a comment…" }), "Which account?");
		await userEvent.click(within(popover).getByRole("button", { name: "Comment" }));
		await waitFor(() => expect(popover.querySelector('[data-thread-id="t9"]')).not.toBeNull());
		expect(popover.querySelector('[data-thread-id="t9"]')).not.toHaveAttribute("data-thread-folded");
		expect(popover.querySelector('[data-thread-id="t1"]')).toHaveAttribute("data-thread-folded", "true");
		port.close();
	});

	it("keeps resolved threads behind Show, with who resolved them and Reopen, never Send to agent", async () => {
		const { port } = await ready(fakeClient(snapshot({ threads: [thread({ status: "resolved", resolvedBy: aditi, resolvedAt: "2026-11-23T10:00:00Z" })] })));
		const popover = await openElement(port);
		expect(await within(popover).findByRole("button", { name: /1 resolved on this element · Show/ })).toBeInTheDocument();
		expect(within(popover).queryByText("Resolved by Aditi")).not.toBeInTheDocument();
		await userEvent.click(within(popover).getByRole("button", { name: /1 resolved on this element · Show/ }));
		expect(within(popover).getByText("Resolved by Aditi")).toBeInTheDocument();
		expect(within(popover).getByRole("button", { name: "Reopen" })).toBeInTheDocument();
		expect(within(popover).queryByRole("button", { name: /Send to agent|Send again/ })).not.toBeInTheDocument();
		expect(within(popover).queryByRole("textbox", { name: "Reply…" })).not.toBeInTheDocument();
		port.close();
	});

	it("drops deleted replies, leaves Comment deleted for a deleted first comment, and marks edits", async () => {
		const base = thread();
		const { port } = await ready(fakeClient(snapshot({ threads: [thread({
			comments: [
				{ ...base.comments[0], body: "", deleted: true },
				{ ...base.comments[1], editedAt: "2026-11-23T09:30:00Z" },
				{ id: "c3", threadId: "t1", clientId: "k3", author: sam, body: "", createdAt: "2026-11-23T09:40:00Z", editedAt: null, deleted: true },
			],
		})] })));
		const popover = await openElement(port);
		expect(await within(popover).findAllByText("Comment deleted")).toHaveLength(1);
		expect(popover.querySelector('[data-comment-id="c3"]')).toBeNull();
		expect(within(popover).getByText("edited")).toBeInTheDocument();
		port.close();
	});

	it("offers the owner Delete, in red, on someone else's comment and Edit only on their own", async () => {
		const { port } = await ready(fakeClient());
		const popover = await openElement(port);
		await within(popover).findByText("Yes, will add.");
		const [samMenu, ownMenu] = within(popover).getAllByRole("button", { name: "Comment actions" });
		await userEvent.click(samMenu);
		expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Delete" })).toHaveClass("text-destructive");
		await userEvent.keyboard("{Escape}");
		await userEvent.click(ownMenu);
		expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
		port.close();
	});

	it("floats over the artifact instead of beside it", async () => {
		const { port } = await ready(fakeClient());
		const popover = await openElement(port);
		expect(popover).toHaveClass("absolute");
		expect(popover.parentElement).toContainElement(screen.getByTestId("published-frame"));
		port.close();
	});

	describe("placement", () => {
		const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientHeight");
		beforeEach(() => {
			// A 700px tall frame area; the popover itself measures 0 tall in jsdom, so it always fits.
			Object.defineProperty(HTMLElement.prototype, "clientHeight", {
				configurable: true,
				get(this: HTMLElement) { return this.dataset.testid === "artifact-comments-stage" ? 700 : 0; },
			});
			vi.stubGlobal("ResizeObserver", class { constructor(private readonly callback: () => void) {} observe() { this.callback(); } disconnect() {} });
		});
		afterEach(() => {
			if (original) Object.defineProperty(HTMLElement.prototype, "clientHeight", original);
			vi.unstubAllGlobals();
		});

		it("sits under the selected element and follows it as the page scrolls", async () => {
			const { port } = await ready(fakeClient());
			const popover = await openElement(port);
			expect(popover.style.top).toBe("8px");
			port.postMessage({ type: "selection", path: thread().anchor.path, top: 100, bottom: 180 });
			await waitFor(() => expect(popover.style.top).toBe("188px"));
			expect(popover.style.maxHeight).toBe("504px");
			port.postMessage({ type: "selection", path: thread().anchor.path, top: 40, bottom: 120 });
			await waitFor(() => expect(popover.style.top).toBe("128px"));
			port.postMessage({ type: "selection", path: null, top: 0, bottom: 0 });
			await waitFor(() => expect(popover.style.top).toBe("8px"));
			port.close();
		});

		it("ignores where an element is when it isn't the selected one", async () => {
			const { port } = await ready(fakeClient());
			const popover = await openElement(port);
			port.postMessage({ type: "selection", path: "body>p:9", top: 100, bottom: 180 });
			await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
			expect(popover.style.top).toBe("8px");
			port.close();
		});
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
		renderHook(() => useCommentBridge(frame, { onPick }));
		act(() => { frame.dispatchEvent(new Event("load")); });
		expect(post).toHaveBeenCalledWith({ type: ARTIFACT_COMMENTS_CONNECT, protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION }, "*", [expect.any(MessagePort)]);
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		const bridgeSide = vi.fn();
		port.onmessage = bridgeSide;
		// A window message never counts, even one shaped like the bridge's.
		act(() => { window.dispatchEvent(new MessageEvent("message", { data: { type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" } } })); });
		expect(onPick).not.toHaveBeenCalled();
		port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION });
		port.postMessage({ type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" } });
		await waitFor(() => expect(onPick).toHaveBeenCalledWith({ path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" }));
		// Theme goes over right after ready.
		await waitFor(() => expect(bridgeSide.mock.calls.some(([event]) => (event as MessageEvent).data.type === "theme")).toBe(true));
		port.close();
		frame.remove();
	});

	it("ignores a ready for another protocol version: nothing from the page counts", async () => {
		const frame = document.createElement("iframe");
		document.body.append(frame);
		const post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => undefined);
		const onPick = vi.fn();
		renderHook(() => useCommentBridge(frame, { onPick }));
		act(() => { frame.dispatchEvent(new Event("load")); });
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		port.postMessage({ type: "ready", protocol: ARTIFACT_COMMENTS_PROTOCOL_VERSION + 1 });
		port.postMessage({ type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "0123456789abcdef", excerpt: "Intro" } });
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(onPick).not.toHaveBeenCalled();
		port.close();
		frame.remove();
	});
});
