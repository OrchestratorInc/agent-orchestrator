import type { DownloadItem, Session, WebContents } from "electron";
import { randomUUID } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import path from "node:path";
import type {
	BrowserDownload,
	BrowserDownloadActionInput,
	BrowserDownloadsState,
} from "../shared/browser-downloads";

type DownloadItemLike = Pick<
	DownloadItem,
	| "cancel"
	| "getFilename"
	| "getURL"
	| "getURLChain"
	| "getReceivedBytes"
	| "getTotalBytes"
	| "isPaused"
	| "on"
	| "once"
	| "removeListener"
	| "pause"
	| "canResume"
	| "resume"
	| "setSavePath"
>;

type DownloadSessionLike = Pick<Session, "on" | "removeListener" | "downloadURL">;
type DownloadWebContentsLike = Pick<WebContents, "downloadURL" | "isDestroyed">;
type DownloadEventLike = { preventDefault: () => void };

// A blocked download keeps only what is needed to request it again after the
// user allows it. Nothing has been written to disk for it.
type BlockedRequest = {
	url: string;
	session: DownloadSessionLike;
	webContents?: DownloadWebContentsLike;
};

type DownloadShell = {
	openPath: (filePath: string) => Promise<string>;
	showItemInFolder: (filePath: string) => void;
	trashItem: (filePath: string) => Promise<void>;
};

type StoredDownload = BrowserDownload & { savePath: string };

type BrowserDownloadManagerOptions = {
	downloadsDirectory: string;
	historyPath: string;
	shell: DownloadShell;
	notify: (state: BrowserDownloadsState) => void;
	now?: () => number;
	createId?: () => string;
};

const MAX_DOWNLOAD_HISTORY = 200;
// Blocked requests are capped on their own so a page or agent loop that keeps
// requesting files can never push real downloads out of the history.
const MAX_BLOCKED_DOWNLOADS = 20;
const DOWNLOAD_DESTINATION_ERROR = "Could not prepare the Downloads folder.";
const DOWNLOAD_DELETE_ERROR = "Could not delete the downloaded file.";
const DOWNLOAD_UNAVAILABLE_ERROR = "This download is no longer available. Open the link again.";
// How long an explicit approval waits for Chromium to request the file again.
const DOWNLOAD_APPROVAL_TTL_MS = 60_000;

function requestedURL(item: DownloadItemLike): string {
	return item.getURLChain()[0] || item.getURL();
}

function downloadSource(url: string): string {
	try {
		const parsed = new URL(url);
		if (parsed.protocol === "blob:") return new URL(parsed.pathname).host;
		return parsed.host;
	} catch {
		return "";
	}
}

function publicDownload(download: StoredDownload): BrowserDownload {
	const { savePath: _savePath, ...safe } = download;
	return safe;
}

function safeFilename(value: string): string {
	const fileName = path.basename(value.trim()).replace(/[. ]+$/u, "");
	return fileName && fileName !== "." && fileName !== ".." ? fileName : "download";
}

function collisionSafePath(directory: string, fileName: string, unavailable: Set<string>): string {
	const parsed = path.parse(fileName);
	let candidate = path.join(directory, fileName);
	let suffix = 1;
	while (existsSync(candidate) || unavailable.has(candidate.toLowerCase())) {
		candidate = path.join(directory, `${parsed.name} (${suffix})${parsed.ext}`);
		suffix += 1;
	}
	return candidate;
}

// Expects newest first, and keeps the newest of each kind.
function withinLimits(downloads: StoredDownload[]): StoredDownload[] {
	let blocked = 0;
	let history = 0;
	return downloads.filter((download) =>
		download.status === "blocked" ? ++blocked <= MAX_BLOCKED_DOWNLOADS : ++history <= MAX_DOWNLOAD_HISTORY);
}

function isInsideDirectory(directory: string, candidate: string): boolean {
	const relative = path.relative(path.resolve(directory), path.resolve(candidate));
	return relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function isStoredDownload(value: unknown): value is StoredDownload {
	if (!value || typeof value !== "object" || Array.isArray(value)) return false;
	const item = value as Partial<StoredDownload>;
	return (
		typeof item.id === "string" &&
		typeof item.fileName === "string" &&
		typeof item.savePath === "string" &&
		typeof item.receivedBytes === "number" &&
		typeof item.totalBytes === "number" &&
		typeof item.startedAt === "number" &&
		typeof item.updatedAt === "number" &&
		["progressing", "paused", "completed", "cancelled", "interrupted"].includes(item.status ?? "")
	);
}

export type BrowserDownloadManager = ReturnType<typeof createBrowserDownloadManager>;

export function createBrowserDownloadManager(options: BrowserDownloadManagerOptions) {
	const attachedSessions = new Map<
		DownloadSessionLike,
		(event: DownloadEventLike, item: DownloadItem, webContents?: DownloadWebContentsLike | null) => void
	>();
	const blockedRequests = new Map<string, BlockedRequest>();
	// An approval belongs to the browser context that will request the file
	// again: the session, and the tab when the request is replayed through one.
	const approvals = new Map<string, {
		url: string;
		session: DownloadSessionLike;
		webContents?: DownloadWebContentsLike;
		expiresAt: number;
	}>();
	const activeItems = new Map<string, DownloadItemLike>();
	const activeItemListeners = new Map<string, {
		item: DownloadItemLike;
		updated: (event: unknown, state: "progressing" | "interrupted") => void;
		done: (event: unknown, state: "completed" | "cancelled" | "interrupted") => void;
	}>();
	const reservedPaths = new Set<string>();
	let disposed = false;
	let downloads: StoredDownload[] = [];
	let error = "";

	try {
		const parsed = JSON.parse(readFileSync(options.historyPath, "utf8")) as unknown;
		if (Array.isArray(parsed)) {
			downloads = parsed
				.filter((download): download is StoredDownload =>
					isStoredDownload(download) && isInsideDirectory(options.downloadsDirectory, download.savePath))
				.map((download) => ({
					...download,
					fileName: path.basename(download.savePath),
					status: download.status === "progressing" || download.status === "paused" ? "interrupted" : download.status,
					active: false,
					resumable: false,
				}));
		}
	} catch {
		// A missing or malformed optional history file starts with an empty list.
	}

	const state = (): BrowserDownloadsState => ({
		downloads: downloads.map(publicDownload),
		...(error ? { error } : {}),
	});
	const persist = (): void => {
		mkdirSync(path.dirname(options.historyPath), { recursive: true });
		const temporaryPath = `${options.historyPath}.tmp`;
		// Blocked requests cannot be replayed after a restart, so they are not history.
		const history = downloads.filter((download) => download.status !== "blocked");
		writeFileSync(temporaryPath, JSON.stringify(history), { encoding: "utf8", mode: 0o600 });
		renameSync(temporaryPath, options.historyPath);
	};
	const publish = (persistState = false): BrowserDownloadsState => {
		if (persistState) {
			try {
				persist();
			} catch {
				// A history write failure must not abort Chromium's file transfer.
			}
		}
		const next = state();
		if (!disposed) {
			try {
				options.notify(next);
			} catch {
				// A window can disappear while an Electron download event is being
				// dispatched. Notification failure must not block other listeners.
			}
		}
		return next;
	};
	const updateItem = (id: string, item: DownloadItemLike, status?: StoredDownload["status"]): void => {
		downloads = downloads.map<StoredDownload>((download) =>
			download.id === id
				? {
						...download,
						receivedBytes: Math.max(0, item.getReceivedBytes()),
						totalBytes: Math.max(0, item.getTotalBytes()),
						status: status ?? (item.isPaused() ? "paused" : "progressing"),
						active: activeItems.has(id),
						resumable: status === "interrupted" && activeItems.has(id) && item.canResume(),
						updatedAt: (options.now ?? Date.now)(),
					}
				: download,
		);
	};

	const takeApproval = (
		item: DownloadItemLike,
		session: DownloadSessionLike,
		webContents?: DownloadWebContentsLike,
	): string | undefined => {
		const now = (options.now ?? Date.now)();
		// Only the URL the request started from counts. A request that merely
		// redirects through an approved URL must not use up that approval.
		const url = requestedURL(item);
		let approvedId: string | undefined;
		for (const [id, approval] of approvals) {
			if (approval.expiresAt < now) approvals.delete(id);
			else if (
				!approvedId &&
				approval.url === url &&
				approval.session === session &&
				// Strict: an approval replayed through the session has no tab, and
				// must not be used by a live tab asking for the same URL.
				approval.webContents === webContents
			) approvedId = id;
		}
		if (approvedId) approvals.delete(approvedId);
		return approvedId;
	};

	// Pages, redirects, and agent-driven navigation can all start a download
	// without the user asking for one. Nothing is saved until the user allows it.
	const block = (
		event: DownloadEventLike,
		item: DownloadItemLike,
		session: DownloadSessionLike,
		webContents?: DownloadWebContentsLike,
	): void => {
		event.preventDefault();
		const url = requestedURL(item);
		const now = (options.now ?? Date.now)();
		// Only a repeat from the same tab is the same request. Another profile or
		// tab asking for this URL gets its own row, so the file a row shows is
		// always the one that row's Download fetches.
		const existing = [...blockedRequests].find(([, request]) =>
			request.url === url && request.session === session && request.webContents === webContents)?.[0];
		if (existing) {
			downloads = downloads.map((download) => download.id === existing ? { ...download, updatedAt: now } : download);
			publish();
			return;
		}
		const id = (options.createId ?? randomUUID)();
		blockedRequests.set(id, { url, session, webContents });
		const download: StoredDownload = {
			id,
			fileName: safeFilename(item.getFilename()),
			savePath: "",
			source: downloadSource(url),
			receivedBytes: 0,
			totalBytes: Math.max(0, item.getTotalBytes()),
			status: "blocked",
			active: false,
			resumable: false,
			startedAt: now,
			updatedAt: now,
		};
		downloads = withinLimits([download, ...downloads]);
		for (const blockedId of blockedRequests.keys()) {
			if (!downloads.some((candidate) => candidate.id === blockedId)) {
				blockedRequests.delete(blockedId);
				approvals.delete(blockedId);
			}
		}
		publish();
	};

	const begin = (item: DownloadItemLike, id: string): void => {
		// The replayed request can report a different name than the one the user
		// approved (a blob link loses its download attribute), so the approved
		// row's name is the one that is saved.
		const approved = downloads.find((download) => download.id === id);
		const source = approved?.source;
		const fileName = approved?.fileName ?? safeFilename(item.getFilename());
		blockedRequests.delete(id);
		downloads = downloads.filter((download) => download.id !== id);
		let savePath: string;
		try {
			mkdirSync(options.downloadsDirectory, { recursive: true });
			savePath = collisionSafePath(options.downloadsDirectory, fileName, reservedPaths);
			item.setSavePath(savePath);
		} catch {
			try {
				item.cancel();
			} catch {
				// The destination failure remains the useful error for the user.
			}
			error = DOWNLOAD_DESTINATION_ERROR;
			publish();
			return;
		}
		error = "";
		const now = (options.now ?? Date.now)();
		reservedPaths.add(savePath.toLowerCase());
		activeItems.set(id, item);
		const download: StoredDownload = {
			id,
			fileName: path.basename(savePath),
			savePath,
			...(source ? { source } : {}),
			receivedBytes: Math.max(0, item.getReceivedBytes()),
			totalBytes: Math.max(0, item.getTotalBytes()),
			status: "progressing",
			active: true,
			resumable: false,
			startedAt: now,
			updatedAt: now,
		};
		downloads = withinLimits([download, ...downloads]);
		publish(true);
		const updated = (_event: unknown, updateState: "progressing" | "interrupted") => {
			updateItem(id, item, updateState === "interrupted" ? "interrupted" : undefined);
			publish();
		};
		const done = (_event: unknown, doneState: "completed" | "cancelled" | "interrupted") => {
			activeItems.delete(id);
			activeItemListeners.delete(id);
			reservedPaths.delete(savePath.toLowerCase());
			updateItem(id, item, doneState);
			publish(true);
		};
		activeItemListeners.set(id, { item, updated, done });
		item.on("updated", updated);
		item.once("done", done);
	};

	return {
		attach(session: DownloadSessionLike | undefined): void {
			if (!session || disposed || attachedSessions.has(session)) return;
			const listener = (event: DownloadEventLike, item: DownloadItem, webContents?: DownloadWebContentsLike | null) => {
				// Electron reports a session-level request with a null WebContents.
				const tab = webContents ?? undefined;
				const approvedId = takeApproval(item, session, tab);
				if (approvedId) begin(item, approvedId);
				else block(event, item, session, tab);
			};
			attachedSessions.set(session, listener);
			session.on("will-download", listener);
		},
		dispose(): void {
			if (disposed) return;
			disposed = true;
			for (const [session, listener] of attachedSessions) {
				session.removeListener("will-download", listener);
			}
			attachedSessions.clear();
			for (const { item, updated, done } of activeItemListeners.values()) {
				item.removeListener("updated", updated);
				item.removeListener("done", done);
			}
			activeItemListeners.clear();
			activeItems.clear();
			reservedPaths.clear();
			blockedRequests.clear();
			approvals.clear();
		},
		list: state,
		async action(input: BrowserDownloadActionInput): Promise<BrowserDownloadsState> {
			if (!input || typeof input.id !== "string") throw new Error("Invalid download action");
			const download = downloads.find((candidate) => candidate.id === input.id);
			if (!download) throw new Error("Download not found");
			const item = activeItems.get(input.id);
			switch (input.action) {
				case "allow": {
					const request = blockedRequests.get(input.id);
					if (download.status !== "blocked" || !request) throw new Error(DOWNLOAD_UNAVAILABLE_ERROR);
					// The tab that asked may be gone; the session then requests the file.
					const tab = request.webContents && !request.webContents.isDestroyed() ? request.webContents : undefined;
					approvals.set(input.id, {
						url: request.url,
						session: request.session,
						webContents: tab,
						expiresAt: (options.now ?? Date.now)() + DOWNLOAD_APPROVAL_TTL_MS,
					});
					try {
						if (tab) tab.downloadURL(request.url);
						else request.session.downloadURL(request.url);
					} catch {
						approvals.delete(input.id);
						throw new Error(DOWNLOAD_UNAVAILABLE_ERROR);
					}
					return state();
				}
				case "pause":
					if (!item) throw new Error("Download is no longer active");
					item.pause();
					updateItem(input.id, item, "paused");
					return publish(true);
				case "resume":
					if (!item) throw new Error("Download is no longer active");
					item.resume();
					updateItem(input.id, item, "progressing");
					return publish(true);
				case "cancel":
					if (!item) throw new Error("Download is no longer active");
					item.cancel();
					return state();
				case "open": {
					if (download.status !== "completed" || !existsSync(download.savePath)) throw new Error("Downloaded file is unavailable");
					const error = await options.shell.openPath(download.savePath);
					if (error) throw new Error(error);
					return state();
				}
				case "show":
					if (download.status !== "completed" || !existsSync(download.savePath)) throw new Error("Downloaded file is unavailable");
					options.shell.showItemInFolder(download.savePath);
					return state();
				case "remove": {
					if (item) throw new Error("Active downloads cannot be removed");
					try {
						if (existsSync(download.savePath)) {
							if (!isInsideDirectory(options.downloadsDirectory, download.savePath)) {
								throw new Error(DOWNLOAD_DELETE_ERROR);
							}
							const file = lstatSync(download.savePath);
							if (!file.isFile() && !file.isSymbolicLink()) throw new Error(DOWNLOAD_DELETE_ERROR);
							await options.shell.trashItem(download.savePath);
						}
					} catch {
						throw new Error(DOWNLOAD_DELETE_ERROR);
					}
					blockedRequests.delete(input.id);
					approvals.delete(input.id);
					downloads = downloads.filter((candidate) => candidate.id !== input.id);
					return publish(true);
				}
				default:
					throw new Error("Unsupported download action");
			}
		},
		clear(): BrowserDownloadsState {
			downloads = downloads.filter((download) => activeItems.has(download.id));
			blockedRequests.clear();
			approvals.clear();
			return publish(true);
		},
	};
}
