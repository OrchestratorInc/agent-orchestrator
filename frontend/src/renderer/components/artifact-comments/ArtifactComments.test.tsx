import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode, Ref } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { useUiStore } from "../../stores/ui-store";
import type { WorkspaceSession } from "../../types/workspace";
import { TooltipProvider } from "../ui/tooltip";
import { ArtifactCommentsWorkspace } from "./ArtifactComments";
import { ArtifactCommentsClientContext, type ArtifactCommentsClient } from "./client";
import {
	ARTIFACT_COMMENTS_BRIDGE_CONNECT,
	formatArtifactCommentMessage,
	groupArtifactCommentThreads,
	parseArtifactCommentsBridgeMessage,
	type ArtifactCommentsSnapshot,
	type ArtifactCommentThread,
} from "./contract";
import { useArtifactComments } from "./useArtifactComments";
import { useCommentBridge } from "./useCommentBridge";

const mocks = vi.hoisted(() => ({ send: vi.fn(), navigate: vi.fn() }));
vi.mock("../../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: {}, ready: true, baseUrl: "https://cp.test", userId: "owner-1" }) }));
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
		anchor: { path: "body>main:1>h1:1", tag: "h1", textHash: "9f2c", excerpt: "Cloudflare staging setup for shared video streaming" },
		createdAt: "2026-11-23T08:00:00Z",
		resolvedBy: null,
		resolvedAt: null,
		sentToAgentAt: null,
		comments: [
			{ id: "c1", author: sam, body: "Can we say \"staging only\" in the title? People will skim this and assume it's production.", createdAt: "2026-11-23T08:00:00Z", editedAt: null, deleted: false },
			{ id: "c2", author: aditi, body: "Yes, will add.", createdAt: "2026-11-23T09:00:00Z", editedAt: null, deleted: false },
		],
		...over,
	};
}

function snapshot(over: Partial<ArtifactCommentsSnapshot> = {}): ArtifactCommentsSnapshot {
	return { version: 3, currentVersion: 3, revision: 41, you: { userId: "owner-1", isOwner: true }, threads: [thread()], ...over };
}

type FakeClient = { [K in keyof ArtifactCommentsClient]: Mock<ArtifactCommentsClient[K]> };

function fakeClient(snap: ArtifactCommentsSnapshot = snapshot()): FakeClient {
	return {
		list: vi.fn().mockResolvedValue({ kind: "snapshot", snapshot: snap, etag: "\"r41-v3\"" }),
		createThread: vi.fn(),
		reply: vi.fn(),
		setStatus: vi.fn(),
		edit: vi.fn(),
		remove: vi.fn(),
		markSent: vi.fn().mockResolvedValue({ ...snap.threads[0], sentToAgentAt: "2026-11-23T10:42:00Z" }),
	};
}

const localSession = { id: "sess-1", title: "cloudflare-video-setup", workspaceId: "proj-1", mode: "chat" } as WorkspaceSession;

function wrap(client: ArtifactCommentsClient) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>
			<ArtifactCommentsClientContext.Provider value={client}>
				<TooltipProvider>{children}</TooltipProvider>
			</ArtifactCommentsClientContext.Provider>
		</QueryClientProvider>
	);
}

function renderWorkspace(client: ArtifactCommentsClient, { outdated = false, withSession = true } = {}) {
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
		expect(client.list).toHaveBeenCalledWith("cf-staging-x7", expect.objectContaining({ etag: null }));
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
		await userEvent.click(within(dialog).getByRole("button", { name: "Send to agent" }));

		await waitFor(() => expect(client.markSent).toHaveBeenCalledWith("cf-staging-x7", "t1"));
		const [message] = mocks.send.mock.calls[0];
		expect(message).toContain("File: docs/cloudflare-staging-setup.html  (shared link version 3)");
		expect(message).toContain("Owner's note: Rename the title.");
		expect(mocks.send.mock.invocationCallOrder[0]).toBeLessThan(client.markSent.mock.invocationCallOrder[0]);
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
		await userEvent.click(within(dialog).getByRole("button", { name: "Send to agent" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("The session isn't running.");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(client.markSent).not.toHaveBeenCalled();
	});

	it("labels a thread already sent and offers Send again", async () => {
		renderWorkspace(fakeClient(snapshot({ threads: [thread({ sentToAgentAt: "2026-11-23T10:42:00Z" })] })));
		await expandFirstGroup();
		expect(screen.getByText(/^Sent to agent · /)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Send again" })).toBeInTheDocument();
	});

	it("hides the frame and panel when access is lost", async () => {
		const { CloudCpError } = await import("../../lib/cloud-cp");
		const client = fakeClient();
		client.list.mockRejectedValue(new CloudCpError("not found", { status: 404, code: "not_found" }));
		renderWorkspace(client);
		expect(await screen.findByText("You no longer have access to this link")).toBeInTheDocument();
		expect(screen.queryByTestId("published-frame")).not.toBeInTheDocument();
	});

	it("shows a reply at once and keeps the text with Retry when it fails", async () => {
		const client = fakeClient();
		client.reply.mockRejectedValue(new Error("offline"));
		renderWorkspace(client);
		await expandFirstGroup();
		const box = screen.getByRole("textbox", { name: "Reply…" });
		await userEvent.type(box, "On it{Enter}");
		expect(await screen.findByText("Couldn't post.")).toBeInTheDocument();
		expect(box).toHaveValue("On it");
		expect(client.reply).toHaveBeenCalledWith("cf-staging-x7", "t1", { body: "On it", clientId: expect.any(String) });
	});
});

describe("useArtifactComments", () => {
	it("keeps the same snapshot when the control plane answers no change", async () => {
		const client = fakeClient();
		const { result } = renderHook(() => useArtifactComments("cf-staging-x7"), { wrapper: wrap(client) });
		await waitFor(() => expect(result.current.snapshot).toBeDefined());
		const first = result.current.snapshot;
		client.list.mockResolvedValue({ kind: "unchanged" });
		await act(async () => { await result.current.refetch(); });
		expect(client.list).toHaveBeenLastCalledWith("cf-staging-x7", expect.objectContaining({ etag: "\"r41-v3\"" }));
		expect(result.current.snapshot).toBe(first);
		expect(result.current.openCount).toBe(1);
	});

	it("polls every 30 seconds only while the panel is open", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const client = fakeClient();
		const { rerender } = renderHook(({ open }) => useArtifactComments("cf-staging-x7", { panelOpen: open }), { wrapper: wrap(client), initialProps: { open: false } });
		await waitFor(() => expect(client.list).toHaveBeenCalledTimes(1));
		await act(async () => { await vi.advanceTimersByTimeAsync(61_000); });
		expect(client.list).toHaveBeenCalledTimes(1);
		rerender({ open: true });
		await waitFor(() => expect(client.list).toHaveBeenCalledTimes(2));
		await act(async () => { await vi.advanceTimersByTimeAsync(30_500); });
		await waitFor(() => expect(client.list).toHaveBeenCalledTimes(3));
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
		expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: ARTIFACT_COMMENTS_BRIDGE_CONNECT }), "*", [expect.any(MessagePort)]);
		const port = (post.mock.calls[0] as unknown as [unknown, string, MessagePort[]])[2][0];
		const bridgeSide = vi.fn();
		port.onmessage = bridgeSide;
		// A window message never counts, even one shaped like the bridge's.
		act(() => { window.dispatchEvent(new MessageEvent("message", { data: { type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "x" } } })); });
		expect(onPick).not.toHaveBeenCalled();
		port.postMessage({ type: "ready" });
		await waitFor(() => expect(result.current.status).toBe("ready"));
		port.postMessage({ type: "pick", anchor: { path: "body>p:1", tag: "p", textHash: "x" } });
		await waitFor(() => expect(onPick).toHaveBeenCalledWith({ path: "body>p:1", tag: "p", textHash: "x" }));
		// Theme goes over right after ready.
		await waitFor(() => expect(bridgeSide.mock.calls.some(([event]) => (event as MessageEvent).data.type === "theme")).toBe(true));
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

describe("contract (provisional)", () => {
	it("groups threads by element in document order, resolved and not-found apart", () => {
		const a = thread({ id: "a", anchor: { path: "body>p:2", tag: "p", textHash: "a" }, createdAt: "2026-11-23T01:00:00Z" });
		const b = thread({ id: "b", anchor: { path: "body>h1:1", tag: "h1", textHash: "b" }, createdAt: "2026-11-23T02:00:00Z" });
		const b2 = thread({ id: "b2", anchor: b.anchor, createdAt: "2026-11-23T00:30:00Z" });
		const r = thread({ id: "r", status: "resolved", anchor: b.anchor });
		const gone = thread({ id: "gone", anchor: { path: "body>div:9", tag: "div", textHash: "g" } });
		const groups = groupArtifactCommentThreads([a, b, b2, r, gone], [
			{ path: "body>h1:1", top: 10, found: true },
			{ path: "body>p:2", top: 200, found: true },
			{ path: "body>div:9", top: 0, found: false },
		]);
		expect(groups.open.map((group) => [group.path, group.threads.map((item) => item.id)])).toEqual([["body>h1:1", ["b2", "b"]], ["body>p:2", ["a"]]]);
		expect(groups.resolved.map((group) => group.path)).toEqual(["body>h1:1"]);
		expect(groups.unanchored.map((item) => item.id)).toEqual(["gone"]);
	});

	it("drops malformed bridge messages", () => {
		expect(parseArtifactCommentsBridgeMessage({ type: "pick", anchor: { path: 1 } })).toBeNull();
		expect(parseArtifactCommentsBridgeMessage({ type: "steal" })).toBeNull();
		expect(parseArtifactCommentsBridgeMessage("ready")).toBeNull();
		expect(parseArtifactCommentsBridgeMessage({ type: "positions", positions: [{ path: "x", top: 1, found: true }, { path: 2 }] }))
			.toEqual({ type: "positions", positions: [{ path: "x", top: 1, found: true }] });
	});

	it("formats the agent message", () => {
		expect(formatArtifactCommentMessage(thread({ comments: [
			{ id: "c1", author: sam, body: "Can we say \"staging only\" in the title? People will skim this and assume it's production.", createdAt: "", editedAt: null, deleted: false },
			{ id: "c2", author: aditi, body: "Yes, will add.", createdAt: "", editedAt: null, deleted: false },
		] }), { path: "docs/cloudflare-staging-setup.html", version: 3 }, "Rename the title and add a one-line staging warning under it.")).toMatchInlineSnapshot(`
			"The owner forwarded a review comment on a shared artifact they published from this session.

			File: docs/cloudflare-staging-setup.html  (shared link version 3)
			Where: <h1> "Cloudflare staging setup for shared video streaming" [selector: body>main:1>h1:1]
			Thread:
			- Sam: Can we say "staging only" in the title? People will skim this and assume it's production.
			- Aditi: Yes, will add.
			Owner's note: Rename the title and add a one-line staging warning under it.

			Update the file to address this, then tell the owner what changed. They will republish it."
		`);
	});

	it("caps the agent message at the file-annotation limit", () => {
		const long = thread({ comments: Array.from({ length: 20 }, (_, index) => ({ id: `c${index}`, author: sam, body: "x".repeat(1500), createdAt: "", editedAt: null, deleted: false })) });
		const message = formatArtifactCommentMessage(long, { path: "a.html", version: 1 }, "keep this note");
		expect(message.length).toBeLessThanOrEqual(4096);
		expect(message).toContain("Owner's note: keep this note");
		expect(message).toMatch(/more not included/);
	});
});
