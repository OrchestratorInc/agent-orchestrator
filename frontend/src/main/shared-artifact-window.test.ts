import { beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	options: [] as Record<string, unknown>[],
	events: new Map<string, (...args: any[]) => void>(),
	windowEvents: new Map<string, (...args: any[]) => void>(),
	current: "",
	titles: [] as string[],
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
			getURL: () => state.current,
			setWindowOpenHandler: (callback: typeof state.popup) => { state.popup = callback; },
		};
		on(name: string, callback: (...args: any[]) => void) { state.windowEvents.set(name, callback); }
		loadURL = state.loadURL;
		isDestroyed() { return false; }
		setTitle(title: string) { state.titles.push(title); }
	},
	shell: { openExternal: state.openExternal },
}));
import { openSharedArtifact, sharedArtifactNavigation } from "./shared-artifact-window";
const viewer = "https://orchestrator.inc/artifact/my-report";

beforeEach(() => {
	vi.clearAllMocks();
	state.options = [];
	state.events.clear();
	state.windowEvents.clear();
	state.current = "";
	state.titles = [];
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
	it("never opens malformed or credential-bearing URLs, or another site's page", () => {
		openSharedArtifact(`${viewer}?token=secret`);
		openSharedArtifact("file:///artifact/my-report");
		openSharedArtifact("https://evil.example/artifact/x");
		expect(state.options).toHaveLength(0);
	});
	it("keeps the page on the viewer, leaving it only by a sign-in redirect", () => {
		openSharedArtifact(viewer);
		const navigate = (name: string, target: string, isMainFrame = true) => {
			const preventDefault = vi.fn();
			state.events.get(name)!({ preventDefault }, target, false, isMainFrame);
			return preventDefault.mock.calls.length === 0;
		};
		state.current = `${viewer}?inApp=1`;
		for (const name of ["will-navigate", "will-redirect"]) expect(navigate(name, "file:///etc/passwd")).toBe(false);
		expect(navigate("will-navigate", "https://orchestrator.inc/api/cloud/auth/login?return_to=%2Fartifact%2Fmy-report")).toBe(true);
		// A page the viewer starts toward another site opens in the browser.
		expect(navigate("will-navigate", "https://evil.example/sign-in")).toBe(false);
		expect(state.openExternal).toHaveBeenCalledWith("https://evil.example/sign-in");
		// Sign-in: the viewer's server sends the window to the identity provider...
		state.current = "https://orchestrator.inc/api/cloud/auth/login";
		expect(navigate("will-redirect", "https://auth.orchestrator.inc/authorize")).toBe(true);
		// ...whose own steps it follows until it sends the person back.
		state.current = "https://auth.orchestrator.inc/authorize";
		expect(navigate("will-navigate", "https://accounts.google.com/o/oauth2")).toBe(true);
		// The framed file host is not the window's page.
		state.current = `${viewer}?inApp=1`;
		expect(navigate("will-navigate", "https://elsewhere.example/", false)).toBe(true);
	});
	it("keeps its own title, and names any host that is not the viewer", () => {
		openSharedArtifact(viewer);
		const preventDefault = vi.fn();
		state.windowEvents.get("page-title-updated")!({ preventDefault }, "Sign in to Orchestrator");
		expect(preventDefault).toHaveBeenCalledOnce();
		state.current = "https://auth.orchestrator.inc/authorize";
		state.events.get("did-navigate")!();
		state.current = `${viewer}?inApp=1`;
		state.events.get("did-navigate")!();
		expect(state.titles).toEqual(["auth.orchestrator.inc · Shared artifact · Orchestrator", "Shared artifact · Orchestrator"]);
	});
	it("opens only HTTPS popups, externally", () => {
		openSharedArtifact(viewer);
		expect(state.popup!({ url: "ao-app://callback" })).toEqual({ action: "deny" });
		expect(state.openExternal).not.toHaveBeenCalled();
		expect(state.popup!({ url: "https://example.com" })).toEqual({ action: "deny" });
		expect(state.openExternal).toHaveBeenCalledWith("https://example.com");
	});
	it("accepts a configured staging viewer next to production's", () => {
		const staging = ["https://orchestrator.inc", "https://staging.orchestrator.inc"];
		expect(sharedArtifactNavigation("https://staging.orchestrator.inc/artifact/x", "https://staging.orchestrator.inc/artifact/y", false, staging)).toBe("allow");
		expect(sharedArtifactNavigation("https://orchestrator.inc/artifact/x", "https://staging.orchestrator.inc/artifact/y", false)).toBe("external");
	});
});
