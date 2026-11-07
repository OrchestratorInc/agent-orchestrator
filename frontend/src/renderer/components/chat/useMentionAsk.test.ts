import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ComposerMention } from "../../lib/cloud-team-collab";
import { useMentionAsk } from "./useMentionAsk";

const cara: ComposerMention = { kind: "person", id: "u-cara", name: "Cara Diaz", canOpen: false };
const chen: ComposerMention = { kind: "person", id: "u-chen", name: "Chen Wei", canOpen: true };
const platform: ComposerMention = { kind: "team", id: "t-platform", name: "Platform", memberCount: 6, membersWithoutAccess: 2 };

function setup(share = vi.fn(async () => undefined)) {
	const hook = renderHook(({ resetKey }) => useMentionAsk({ share, resetKey }), { initialProps: { resetKey: "s1" } });
	return { hook, share };
}

describe("useMentionAsk", () => {
	it("asks nothing when nobody needs asking about", () => {
		const { hook } = setup();
		expect(hook.result.current.confirm([chen])).toBeUndefined();
		expect(hook.result.current.step).toBeUndefined();
	});

	it("asks about the big team first, then the people without access, one at a time", async () => {
		const { hook } = setup();
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = hook.result.current.confirm([platform, cara, chen]); });
		expect(hook.result.current.step).toEqual({ kind: "team", team: platform });
		act(() => hook.result.current.notifyTeam());
		expect(hook.result.current.step).toEqual({ kind: "noAccess", people: [cara] });
		act(() => hook.result.current.sendWithout());
		expect(hook.result.current.step).toBeUndefined();
		await expect(answer).resolves.toEqual([platform, chen]);
	});

	it("keeps the draft when a team mention is cancelled", async () => {
		const { hook } = setup();
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = hook.result.current.confirm([platform]); });
		act(() => hook.result.current.cancel());
		await expect(answer).resolves.toBeNull();
		expect(hook.result.current.step).toBeUndefined();
	});

	it("shares with the people first on Share and send, then sends to everyone", async () => {
		const { hook, share } = setup();
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = hook.result.current.confirm([cara, chen]); });
		await act(async () => { await hook.result.current.shareAndSend(); });
		expect(share).toHaveBeenCalledWith([cara]);
		await expect(answer).resolves.toEqual([cara, chen]);
	});

	it("keeps asking when sharing failed, and says so", async () => {
		const share = vi.fn(async () => { throw new Error("forbidden"); });
		const { hook } = setup(share);
		act(() => { void hook.result.current.confirm([cara]); });
		await act(async () => { await hook.result.current.shareAndSend(); });
		expect(hook.result.current.step).toEqual({ kind: "noAccess", people: [cara] });
		expect(hook.result.current.shareFailed).toBe(true);
		expect(hook.result.current.sharing).toBe(false);
	});

	it("drops a question still open for another session", async () => {
		const { hook } = setup();
		let answer: Promise<ComposerMention[] | null> | undefined;
		act(() => { answer = hook.result.current.confirm([cara]); });
		hook.rerender({ resetKey: "s2" });
		await expect(answer).resolves.toBeNull();
		expect(hook.result.current.step).toBeUndefined();
	});

	it("answers no to an earlier question when a new one starts", async () => {
		const { hook } = setup();
		let first: Promise<ComposerMention[] | null> | undefined;
		act(() => { first = hook.result.current.confirm([cara]); });
		act(() => { void hook.result.current.confirm([platform]); });
		await expect(first).resolves.toBeNull();
		expect(hook.result.current.step).toEqual({ kind: "team", team: platform });
	});
});
