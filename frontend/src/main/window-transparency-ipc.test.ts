import { describe, expect, it, vi } from "vitest";
import { registerWindowTransparencyIpc } from "./window-transparency-ipc";

describe("window transparency IPC", () => {
	function setup() {
		const handlers = new Map<string, (...args: any[]) => unknown>();
		const shell = {};
		const state = { supported: true, enabled: false, reason: null };
		const read = vi.fn(() => state);
		const set = vi.fn((enabled: boolean) => ({ ...state, enabled }));
		registerWindowTransparencyIpc({ ipcMain: { handle: (name, handler) => { handlers.set(name, handler); } }, isShell: (sender) => sender === shell, read, set });
		return { handlers, shell, read, set };
	}
	it("returns capability and effective state to the shell", () => {
		const { handlers, shell, set } = setup();
		expect(handlers.get("theme:get-window-transparency")!({ sender: shell })).toEqual({ supported: true, enabled: false, reason: null });
		expect(handlers.get("theme:set-window-translucent")!({ sender: shell }, true)).toEqual({ supported: true, enabled: true, reason: null });
		expect(set).toHaveBeenCalledWith(true);
	});
	it.each(["theme:get-window-transparency", "theme:set-window-translucent"])("rejects untrusted senders for %s", (channel) => {
		const { handlers, read, set } = setup();
		expect(() => handlers.get(channel)!({ sender: {} }, true)).toThrow("Untrusted");
		expect(read).not.toHaveBeenCalled();
		expect(set).not.toHaveBeenCalled();
	});
	it.each([null, undefined, 1, "true", {}])("rejects invalid preference %s", (value) => {
		const { handlers, shell, set } = setup();
		expect(() => handlers.get("theme:set-window-translucent")!({ sender: shell }, value)).toThrow("boolean");
		expect(set).not.toHaveBeenCalled();
	});
});
