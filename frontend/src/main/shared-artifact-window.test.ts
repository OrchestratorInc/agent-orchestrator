import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	options: [] as Record<string, unknown>[],
	events: new Map<string, (...args: any[]) => void>(),
	popup: null as null | ((details: { url: string }) => { action: string }),
	loadURL: vi.fn(() => Promise.resolve()),
	openExternal: vi.fn(() => Promise.resolve()),
	permission: vi.fn(),
	checkPermission: vi.fn(),
}));
vi.mock("electron", () => ({
	BrowserWindow: class {
		constructor(options: Record<string, unknown>) { state.options.push(options); }
		webContents = {
			session: { setPermissionRequestHandler: state.permission, setPermissionCheckHandler: state.checkPermission },
			on: (name: string, callback: (...args: any[]) => void) => state.events.set(name, callback),
			setWindowOpenHandler: (callback: typeof state.popup) => { state.popup = callback; },
		};
		on() {}
		loadURL = state.loadURL;
		isDestroyed() { return false; }
		setTitle() {}
	},
	shell: { openExternal: state.openExternal },
}));
import { openSharedArtifact } from "./shared-artifact-window";
const viewer = "https://orchestrator.inc/artifact/my-report";

beforeEach(() => {
	vi.clearAllMocks();
	state.options = [];
	state.events.clear();
});
describe("isolated shared artifact window", () => {
	it("opens the published page with no preload or app credentials", () => {
		openSharedArtifact(viewer);
		expect(state.loadURL).toHaveBeenCalledWith(`${viewer}?inApp=1`);
		expect(state.options[0].webPreferences).toEqual({
			partition: "persist:shared-artifacts", nodeIntegration: false, contextIsolation: true,
			sandbox: true, webviewTag: false,
		});
		const callback = vi.fn();
		state.permission.mock.calls[0][0](null, "camera", callback);
		expect(callback).toHaveBeenCalledWith(false);
		expect(state.checkPermission.mock.calls[0][0]()).toBe(false);
	});
	it("never opens malformed or credential-bearing URLs", () => {
		openSharedArtifact(`${viewer}?token=secret`);
		openSharedArtifact("file:///artifact/my-report");
		expect(state.options).toHaveLength(0);
	});
	it("blocks privileged navigation and only opens HTTPS popups externally", () => {
		openSharedArtifact(viewer);
		for (const name of ["will-navigate", "will-redirect"]) {
			const preventDefault = vi.fn();
			state.events.get(name)!({ preventDefault }, "file:///etc/passwd");
			expect(preventDefault).toHaveBeenCalledOnce();
			state.events.get(name)!({ preventDefault }, "https://auth.example.com/sign-in");
			expect(preventDefault).toHaveBeenCalledOnce();
		}
		expect(state.popup!({ url: "ao-app://callback" })).toEqual({ action: "deny" });
		expect(state.openExternal).not.toHaveBeenCalled();
		expect(state.popup!({ url: "https://example.com" })).toEqual({ action: "deny" });
		expect(state.openExternal).toHaveBeenCalledWith("https://example.com");
	});
});
