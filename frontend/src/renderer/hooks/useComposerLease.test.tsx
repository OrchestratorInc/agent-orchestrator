import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpComposerAnswer, CloudCpComposerLease, CloudCpSessionLive, CloudCpSessionViewer } from "../lib/cloud-cp";
import { COMPOSER_DRAFT_PUBLISH_MS, COMPOSER_RENEW_MS, useComposerLease } from "./useComposerLease";

const ME = "user-me";
const ALICE = { userId: "user-alice", name: "Alice" };
const BOB = { userId: "user-bob", name: "Bob" };

function live(viewers: CloudCpSessionViewer[], composer: Partial<CloudCpComposerLease> = {}, canTakeOver = false): CloudCpSessionLive {
	return { type: "session.live", viewers, composer: { holder: null, epoch: 0, waitlist: [], ...composer }, canTakeOver, you: ME, chatSettings: {}, canConfigure: canTakeOver };
}

const me = { userId: ME, name: "Me", role: "member" as const };
const aliceEditor = { ...ALICE, role: "editor" as const };

function answer(granted: boolean, epoch: number, holder = granted ? { userId: ME, name: "Me" } : ALICE): CloudCpComposerAnswer {
	return { composer: { holder, epoch, waitlist: [], expiresInMs: granted ? 15_000 : 0 }, granted };
}

function fakeClient() {
	return {
		acquireComposer: vi.fn(async () => answer(true, 7)),
		renewComposer: vi.fn(async () => answer(true, 7)),
		releaseComposer: vi.fn(async () => ({ composer: { holder: null, epoch: 7, waitlist: [], expiresInMs: 0 }, granted: false })),
		requestComposerTurn: vi.fn(async () => answer(false, 7)),
		leaveComposerQueue: vi.fn(async () => answer(false, 7)),
		takeOverComposer: vi.fn(async () => answer(true, 8)),
		publishComposerDraft: vi.fn(async () => answer(true, 7)),
	};
}

function render(client: ReturnType<typeof fakeClient>, initial: CloudCpSessionLive | undefined, canWrite = true) {
	return renderHook(
		({ state }: { state: CloudCpSessionLive | undefined }) =>
			useComposerLease({ client, orgId: "org-1", sessionId: "session-1", userId: ME, live: state, canWrite }),
		{ initialProps: { state: initial } },
	);
}

describe("useComposerLease", () => {
	beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
	afterEach(() => vi.useRealTimers());

	it("does nothing while nobody else can write in the session", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me]));
		act(() => result.current.noteDraft("hello"));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_RENEW_MS * 3); });
		expect(result.current.shared).toBe(false);
		expect(result.current.locked).toBe(false);
		expect(result.current.epoch).toBeUndefined();
		expect(client.acquireComposer).not.toHaveBeenCalled();
		expect(client.renewComposer).not.toHaveBeenCalled();
	});

	it("takes the lease on the first keystroke and renews while typing", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("h"));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		expect(client.acquireComposer).toHaveBeenCalledTimes(1);
		expect(result.current.holding).toBe(true);

		act(() => result.current.noteDraft("hi"));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_RENEW_MS); });
		expect(client.renewComposer).toHaveBeenLastCalledWith("org-1", "session-1", 7, true);
		// No keystroke since: the next renewal reports it, so the server can
		// release an idle holder.
		await act(async () => { vi.advanceTimersByTime(COMPOSER_RENEW_MS); });
		expect(client.renewComposer).toHaveBeenLastCalledWith("org-1", "session-1", 7, false);
	});

	it("releases the lease when the draft is cleared", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("h"));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		act(() => result.current.noteDraft(""));
		await waitFor(() => expect(client.releaseComposer).toHaveBeenCalledWith("org-1", "session-1", 7));
		await waitFor(() => expect(result.current.holding).toBe(false));
	});

	it("is locked while someone else holds the lease, and never asks for it", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor], { holder: ALICE, epoch: 3 }, true));
		expect(result.current.locked).toBe(true);
		expect(result.current.holder).toEqual(ALICE);
		expect(result.current.canTakeOver).toBe(true);
		act(() => result.current.noteDraft("hello"));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_RENEW_MS); });
		expect(client.acquireComposer).not.toHaveBeenCalled();

		act(() => result.current.requestTurn());
		await waitFor(() => expect(client.requestComposerTurn).toHaveBeenCalledWith("org-1", "session-1"));
		act(() => result.current.takeOver());
		await waitFor(() => expect(client.takeOverComposer).toHaveBeenCalledWith("org-1", "session-1"));
	});

	it("follows the live state: a lease handed over from the line is held, and a take-over ends the grant", async () => {
		const client = fakeClient();
		const { result, rerender } = render(client, live([me, aliceEditor], { holder: ALICE, epoch: 3, waitlist: [{ userId: ME, name: "Me" }] }));
		expect(result.current.position).toBe(1);
		expect(result.current.locked).toBe(true);

		rerender({ state: live([me, aliceEditor], { holder: { userId: ME, name: "Me" }, epoch: 4 }) });
		await waitFor(() => expect(result.current.holding).toBe(true));
		expect(result.current.epoch).toBe(4);

		rerender({ state: live([me, aliceEditor], { holder: BOB, epoch: 5 }) });
		await waitFor(() => expect(result.current.holding).toBe(false));
		expect(result.current.epoch).toBeUndefined();
		expect(result.current.locked).toBe(true);
	});

	it("takes the lease for a draft already written when someone joins", async () => {
		const client = fakeClient();
		const { result, rerender } = render(client, live([me]));
		act(() => result.current.noteDraft("half a thought"));
		expect(client.acquireComposer).not.toHaveBeenCalled();
		rerender({ state: live([me, aliceEditor]) });
		await waitFor(() => expect(client.acquireComposer).toHaveBeenCalledTimes(1));
	});

	it("forgets the grant once a send used it", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("h"));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		act(() => result.current.sent());
		expect(result.current.epoch).toBeUndefined();
	});

	it("never takes part for a read-only participant", async () => {
		const client = fakeClient();
		const { result } = render(client, live([{ ...me, role: "viewer" }, aliceEditor]), false);
		expect(result.current.shared).toBe(false);
		act(() => result.current.noteDraft("hello"));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_RENEW_MS); });
		expect(client.acquireComposer).not.toHaveBeenCalled();
	});

	it("reports a failed request", async () => {
		const client = fakeClient();
		client.acquireComposer.mockRejectedValueOnce(new Error("offline"));
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("h"));
		await waitFor(() => expect(result.current.error).toBe("offline"));
	});

	it("shares the holder's draft and caret, gathering quick keystrokes into one update", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("h", 1));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		act(() => result.current.noteDraft("he", 2));
		act(() => result.current.noteDraft("hel", 3));
		act(() => result.current.noteDraft("help", 2));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS + 10); });
		await waitFor(() => expect(client.publishComposerDraft).toHaveBeenLastCalledWith("org-1", "session-1", 7, "help", 2));
		expect(client.publishComposerDraft.mock.calls.length).toBeLessThanOrEqual(2);
	});

	it("shares a caret move without a text change", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("hello", 5));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS + 10); });
		act(() => result.current.noteDraft("hello", 1));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS + 10); });
		await waitFor(() => expect(client.publishComposerDraft).toHaveBeenLastCalledWith("org-1", "session-1", 7, "hello", 1));
	});

	it("shares the draft with someone who can only watch", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, { ...ALICE, role: "viewer" }]));
		act(() => result.current.noteDraft("h", 1));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		act(() => result.current.noteDraft("hello", 5));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS + 10); });
		await waitFor(() => expect(client.publishComposerDraft).toHaveBeenLastCalledWith("org-1", "session-1", 7, "hello", 5));
	});

	it("keeps sharing the draft while someone types without pausing", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me, aliceEditor]));
		act(() => result.current.noteDraft("x", 1));
		await waitFor(() => expect(result.current.epoch).toBe(7));
		let text = "x";
		for (let keystroke = 0; keystroke < 20; keystroke++) {
			text += "y";
			act(() => result.current.noteDraft(text, text.length));
			await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS / 3); });
		}
		// 20 keystrokes over about six publish windows, with no pause.
		expect(client.publishComposerDraft.mock.calls.length).toBeGreaterThanOrEqual(4);
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS + 10); });
		await waitFor(() => expect(client.publishComposerDraft).toHaveBeenLastCalledWith("org-1", "session-1", 7, text, text.length));
	});

	it("never shares a draft while nobody else is in the session", async () => {
		const client = fakeClient();
		const { result } = render(client, live([me]));
		act(() => result.current.noteDraft("private", 7));
		await act(async () => { vi.advanceTimersByTime(COMPOSER_DRAFT_PUBLISH_MS * 5); });
		expect(client.publishComposerDraft).not.toHaveBeenCalled();
	});

	it("shows someone else's draft, but never the person's own", () => {
		const client = fakeClient();
		const theirs = render(client, live([me, aliceEditor], { holder: ALICE, epoch: 3, draft: { text: "run the tests", cursor: 4 } }));
		expect(theirs.result.current.remoteDraft).toEqual({ holder: ALICE, text: "run the tests", cursor: 4 });
		const mine = render(client, live([me, aliceEditor], { holder: { userId: ME, name: "Me" }, epoch: 3, draft: { text: "mine", cursor: 4 } }));
		expect(mine.result.current.remoteDraft).toBeUndefined();
	});
});
