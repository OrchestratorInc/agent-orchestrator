import { describe, expect, it, vi } from "vitest";
import { checkRender } from "./render-check";

const url = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/check-id-001";

function fakes(options: { loadError?: Error } = {}) {
	const listeners = new Map<string, (...args: unknown[]) => void>();
	const permissionRequest = vi.fn();
	const contents = {
		session: {
			setPermissionRequestHandler: (handler: (...args: unknown[]) => void) => permissionRequest.mockImplementation(handler),
			setPermissionCheckHandler: vi.fn(),
		},
		setWindowOpenHandler: vi.fn(),
		on: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		loadURL: vi.fn(async () => {
			if (options.loadError) throw options.loadError;
			listeners.get("console-message")?.({}, 3, "Uncaught ReferenceError: d3 is not defined", 1, url);
		}),
		executeJavaScript: vi.fn(async () => 412),
		debugger: {
			attach: vi.fn(),
			sendCommand: vi.fn(async () => ({ data: "iVBORw0KGgo=" })),
			detach: vi.fn(),
		},
		close: vi.fn(),
	};
	const view = { webContents: contents, setBounds: vi.fn() };
	// A function, not an arrow: checkRender calls it with `new` (vitest 4 rejects arrow constructors).
	const WebContentsView = vi.fn(function (_options: { webPreferences: Record<string, unknown> }) {
		return view;
	});
	const window = { contentView: { addChildView: vi.fn(), removeChildView: vi.fn() } };
	return { contents, view, WebContentsView, window, permissionRequest };
}

describe("checkRender", () => {
	it("loads only daemon render-check URLs", async () => {
		const f = fakes();
		await expect(checkRender(f as never, { url: "https://example.com/", width: 720 })).rejects.toThrow(/render-check URL/);
		await expect(checkRender(f as never, { url: url.replace("check-", ""), width: 720 })).rejects.toThrow(/render-check URL/);
		expect(f.WebContentsView).not.toHaveBeenCalled();
	});

	it("returns the screenshot, height, and console, in a sandboxed throwaway view", async () => {
		const f = fakes();
		const result = await checkRender(f as never, { url, width: 390 });
		expect(result).toEqual({
			data: "iVBORw0KGgo=",
			width: 390,
			height: 412,
			contentHeight: 412,
			consoleMessages: [{ level: "error", text: "Uncaught ReferenceError: d3 is not defined" }],
		});
		const prefs = f.WebContentsView.mock.calls[0][0].webPreferences;
		expect(prefs).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false });
		expect(prefs.partition).toMatch(/^ao-render-check-/);
		const decide = vi.fn();
		f.permissionRequest({}, "media", decide);
		expect(decide).toHaveBeenCalledWith(false);
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});

	it("removes the view when the page fails to load", async () => {
		const f = fakes({ loadError: new Error("ERR_CONNECTION_REFUSED") });
		await expect(checkRender(f as never, { url, width: 720 })).rejects.toThrow(/ERR_CONNECTION_REFUSED/);
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});
});
