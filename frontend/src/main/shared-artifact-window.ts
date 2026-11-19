import { BrowserWindow, shell } from "electron";
import { artifactViewerOrigins, sharedArtifactAppURL } from "../../../packages/shared/artifacts/shared-artifact-link";

/**
 * The artifact pages the app opens: production's, plus a staging or local
 * viewer's (AO_ARTIFACT_VIEWER_ORIGIN). A link to any other site opens nothing,
 * so no web page can put its own content in a window titled as Orchestrator's.
 */
export const SHARED_ARTIFACT_VIEWER_ORIGINS = artifactViewerOrigins(process.env.AO_ARTIFACT_VIEWER_ORIGIN);

const TITLE = "Shared artifact · Orchestrator";

/**
 * Where the window's page may go next. On the viewer, it may follow its own
 * links; it leaves the viewer only through a server redirect, which is how
 * sign-in reaches the identity provider, and the provider may then take its
 * own steps until it sends the person back. Anything a viewer page starts
 * toward another site opens in the browser instead.
 */
export function sharedArtifactNavigation(
	current: string,
	target: string,
	redirect: boolean,
	origins: readonly string[] = SHARED_ARTIFACT_VIEWER_ORIGINS,
): "allow" | "external" | "deny" {
	let next: URL;
	try {
		next = new URL(target);
	} catch {
		return "deny";
	}
	if (next.protocol !== "https:") return "deny";
	if (origins.includes(next.origin) || redirect) return "allow";
	let from: string | null = null;
	try {
		from = new URL(current).origin;
	} catch {
		// No page yet: only the viewer itself may load.
	}
	return from && !origins.includes(from) ? "allow" : "external";
}

/** The window's title: the host it shows, unless that is a viewer. */
export function sharedArtifactTitle(current: string, origins: readonly string[] = SHARED_ARTIFACT_VIEWER_ORIGINS): string {
	try {
		const url = new URL(current);
		return origins.includes(url.origin) ? TITLE : `${url.host} · ${TITLE}`;
	} catch {
		return TITLE;
	}
}

// Keep remote documents completely separate from the supervisor's preload,
// credentials and IPC. The viewer itself enforces access, including sign-in.
const windows = new Set<BrowserWindow>();

export function openSharedArtifact(raw: string): void {
	const url = sharedArtifactAppURL(raw, SHARED_ARTIFACT_VIEWER_ORIGINS);
	if (!url) return;
	const window = new BrowserWindow({
		title: TITLE,
		width: 1000,
		height: 800,
		webPreferences: {
			partition: "persist:shared-artifacts",
			nodeIntegration: false,
			contextIsolation: true,
			sandbox: true,
			webviewTag: false,
		},
	});
	windows.add(window);
	window.on("closed", () => windows.delete(window));
	// The page never names the window: it shows which site it is on.
	window.on("page-title-updated", (event) => event.preventDefault());
	const contents = window.webContents;
	contents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
	contents.session.setPermissionCheckHandler(() => false);
	const guard = (redirect: boolean) => (event: Electron.Event, target: string, _isInPlace: boolean, isMainFrame: boolean) => {
		// A framed document is the viewer's sandboxed file host, not the window's page.
		if (!isMainFrame) return;
		const verdict = sharedArtifactNavigation(contents.getURL(), target, redirect);
		if (verdict === "allow") return;
		event.preventDefault();
		if (verdict === "external") void shell.openExternal(target).catch(() => {});
	};
	contents.on("will-navigate", guard(false));
	contents.on("will-redirect", guard(true));
	const retitle = () => {
		if (!window.isDestroyed()) window.setTitle(sharedArtifactTitle(contents.getURL()));
	};
	contents.on("did-navigate", retitle);
	contents.on("did-navigate-in-page", retitle);
	contents.setWindowOpenHandler(({ url: target }) => {
		try {
			if (new URL(target).protocol === "https:") void shell.openExternal(target).catch(() => {});
		} catch { /* refuse invalid URLs */ }
		return { action: "deny" };
	});
	void window.loadURL(url).catch(() => {
		if (!window.isDestroyed()) window.setTitle("Could not load shared artifact · Orchestrator");
	});
}
