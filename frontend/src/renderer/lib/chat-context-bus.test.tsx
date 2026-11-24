import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FileCodeReference } from "../../shared/file-annotations";
import { sendImageToChat, sendReferenceToChat, subscribeChatComposerImages, subscribeChatComposerReferences, subscribeChatReveal, useAskInChat, useHasChatImageComposer } from "./chat-context-bus";
import { captureFileReference } from "../../shared/chat-file-reference";
import { sessionUiKey } from "./hosts";

const range: FileCodeReference = {
	path: "src/retry.ts",
	side: "new",
	line: 3,
	endLine: 4,
	lines: [
		{ kind: "context", oldNo: 3, newNo: 3, text: "for (let i = 0; i < retries; i++) {" },
		{ kind: "add", oldNo: null, newNo: 4, text: "  await attempt();" },
	],
};

describe("useAskInChat", () => {
	it("rejects unsupported captures before notifying or revealing the composer", () => {
		const receive=vi.fn(), reveal=vi.fn();
		const stop=subscribeChatComposerReferences("invalid-capture",receive);
		const stopReveal=subscribeChatReveal("invalid-capture",reveal);
		const view=renderHook(()=>useAskInChat("invalid-capture"));
		for(const selection of [{...range,path:"notes:v2.md"},{...range,previousPath:"a\\b"},{...range,lines:[{...range.lines[0],text:"bad\0row"}]}]) {
			const saved=JSON.stringify(selection);
			expect(view.result.current?.(selection)).toBe(false);
			expect(JSON.stringify(selection)).toBe(saved);
		}
		expect(receive).not.toHaveBeenCalled();
		expect(reveal).not.toHaveBeenCalled();
		view.unmount();stop();stopReveal();
	});
	it("does not acknowledge or reveal a refused reference", () => {
		const reveal = vi.fn();
		const unreveal = subscribeChatReveal("refused-session", reveal);
		const unsubscribe = subscribeChatComposerReferences("refused-session", () => false);
		expect(sendReferenceToChat("refused-session", captureFileReference(range, "refused-session"))).toBe(false);
		expect(reveal).not.toHaveBeenCalled();
		unsubscribe();
		unreveal();
	});
	it("is offered only while the session has a Chat composer, and hands it the reference", () => {
		const { result } = renderHook(() => useAskInChat("sess-chat"));
		expect(result.current).toBeUndefined();

		const received = vi.fn();
		const revealed = vi.fn();
		let unsubscribe = () => {};
		const unreveal = subscribeChatReveal("sess-chat", revealed);
		act(() => {
			unsubscribe = subscribeChatComposerReferences("sess-chat", received);
		});
		expect(result.current).toBeDefined();

		act(() => result.current?.(range));
		expect(revealed).toHaveBeenCalledTimes(1);
		expect(received).toHaveBeenCalledWith(expect.objectContaining({
			path: "src/retry.ts",
			sessionId: "sess-chat", lines: range.lines, truncated: false,
		}));

		act(() => unsubscribe());
		unreveal();
		expect(result.current).toBeUndefined();
	});

	it("finds a remote session's Chat composer under its host-scoped key", () => {
		const { result } = renderHook(() => useAskInChat("sess-remote", "box-2"));
		const received = vi.fn();
		let unsubscribe = () => {};
		act(() => {
			unsubscribe = subscribeChatComposerReferences(sessionUiKey("sess-remote", "box-2"), received);
		});
		expect(result.current).toBeDefined();
		act(() => result.current?.(range));
		expect(received).toHaveBeenCalledWith(expect.objectContaining({ path: "src/retry.ts", sessionId: "sess-remote", hostId: "box-2" }));
		act(() => unsubscribe());
	});
});

describe("sendImageToChat", () => {
	const image = () => new File([new Uint8Array([1])], "browser-selection.png", { type: "image/png" });

	it("is unavailable until a composer that can attach registers", () => {
		const { result } = renderHook(() => useHasChatImageComposer("image-availability"));
		expect(result.current).toBe(false);
		expect(sendImageToChat("image-availability", image())).toBe(false);
		let stop = () => {};
		act(() => {
			stop = subscribeChatComposerImages("image-availability", () => true);
		});
		expect(result.current).toBe(true);
		act(() => stop());
		expect(result.current).toBe(false);
	});

	it("hands the image to one composer and reveals the chat", () => {
		const first = vi.fn(() => true);
		const second = vi.fn(() => true);
		const reveal = vi.fn();
		const stops = [
			subscribeChatComposerImages("image-once", first),
			subscribeChatComposerImages("image-once", second),
			subscribeChatReveal("image-once", reveal),
		];
		const file = image();
		expect(sendImageToChat("image-once", file)).toBe(true);
		expect(first).toHaveBeenCalledWith(file);
		expect(second).not.toHaveBeenCalled();
		expect(reveal).toHaveBeenCalledTimes(1);
		for (const stop of stops) stop();
	});

	it("does not reveal the chat when the composer refuses the image", () => {
		const reveal = vi.fn();
		const stops = [subscribeChatComposerImages("image-busy", () => false), subscribeChatReveal("image-busy", reveal)];
		expect(sendImageToChat("image-busy", image())).toBe(false);
		expect(reveal).not.toHaveBeenCalled();
		for (const stop of stops) stop();
	});
});
