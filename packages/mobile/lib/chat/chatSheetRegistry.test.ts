import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatSheetRoute, parkChatSheet, readChatSheet, releaseChatSheet, resetChatSheets } from "./chatSheetRegistry";

beforeEach(resetChatSheets);

describe("chat sheet registry", () => {
	it("keeps non-serializable sheet data alive only until release", () => {
		const onSelect = vi.fn();
		const key = parkChatSheet({ kind: "account-picker", onSelect } as never);
		expect(readChatSheet(key)).toMatchObject({ kind: "account-picker", onSelect });
		releaseChatSheet(key);
		expect(readChatSheet(key)).toBeUndefined();
	});

	it.each([
		["conversation-usage", "/sheets/conversation-usage"],
	] as const)("routes %s through a native sheet route", (kind, pathname) => {
		const route = chatSheetRoute({ kind } as never);
		expect(route.pathname).toBe(pathname);
		expect(readChatSheet(route.params.sheetKey)?.kind).toBe(kind);
	});
});
