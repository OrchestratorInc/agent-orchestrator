import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FileCodeReference } from "../../shared/file-annotations";
import { sendReferenceToChat, subscribeChatComposerReferences, subscribeChatReveal, useAskInChat } from "./chat-context-bus";
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
