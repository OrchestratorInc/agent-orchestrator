import type { BrowserCommand, BrowserDialog, BrowserDownload, BrowserPickedFile } from "../browserTransport";

/**
 * What the phone answers when the page asks something, and how it picks and
 * saves files. The sheets themselves are per platform (BrowserAsk).
 */

type AuthDialog = Extract<BrowserDialog, { kind: "auth" }>;
type SelectDialog = Extract<BrowserDialog, { kind: "select" }>;
type FileChooserDialog = Extract<BrowserDialog, { kind: "filechooser" }>;

export function answerDialog(accept: boolean, promptText?: string): BrowserCommand {
	return { type: "dialog", payload: promptText === undefined ? { accept } : { accept, promptText } };
}

/** Sent straight to the host: neither this nor its caller keeps or logs it. */
export function answerAuth(dialog: AuthDialog, credentials: { username: string; password: string } | null): BrowserCommand {
	return credentials
		? { type: "auth", payload: { origin: dialog.origin, username: credentials.username, password: credentials.password } }
		: { type: "auth", payload: { origin: dialog.origin, cancel: true } };
}

/** Null for an option the page doesn't offer, or has disabled. */
export function answerSelect(dialog: SelectDialog, index: number): BrowserCommand | null {
	const option = dialog.options[index];
	if (!Number.isInteger(index) || !option || option.disabled) return null;
	return { type: "select", payload: { id: dialog.id, index, ...(dialog.ctx !== undefined ? { ctx: dialog.ctx } : {}) } };
}

function hostOf(origin: string): string {
	try {
		return new URL(origin).host || origin;
	} catch {
		return origin;
	}
}

export function askTitle(dialog: BrowserDialog): string {
	switch (dialog.kind) {
		case "alert": return "The page says";
		case "confirm": return "The page asks";
		case "prompt": return "The page asks";
		case "beforeunload": return "Leave this page?";
		case "auth": return `Sign in to ${hostOf(dialog.origin)}`;
		case "select": return "Choose an option";
		case "filechooser": return dialog.multiple ? "The page asks for files" : "The page asks for a file";
	}
}

export function authMessage(dialog: AuthDialog): string {
	const lines = [dialog.failed ? "That username or password didn't work." : "", dialog.realm ? `The site says: ${dialog.realm}` : ""];
	return lines.filter(Boolean).join("\n");
}

export function downloadLabel(download: BrowserDownload): string {
	if (!download.size) return download.name;
	const units = ["B", "KB", "MB", "GB"];
	let size = download.size;
	let unit = 0;
	while (size >= 1024 && unit < units.length - 1) {
		size /= 1024;
		unit++;
	}
	return `${download.name} · ${unit === 0 ? size : size.toFixed(1)} ${units[unit]}`;
}

const EXTENSION_TYPES: Record<string, string> = {
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp", ".heic": "image/heic",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".pdf": "application/pdf", ".csv": "text/csv", ".txt": "text/plain",
	".json": "application/json", ".zip": "application/zip", ".doc": "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
};

export type PickerOptions = { photos: boolean; mediaTypes: Array<"images" | "videos">; documentTypes: string | string[] };

/** The phone's pickers for the page's `accept`: Photos only when it takes images or video. */
export function pickerFor(accept: string): PickerOptions {
	const parts = accept.split(",").map((part) => part.trim().toLowerCase()).filter(Boolean);
	if (!parts.length) return { photos: true, mediaTypes: ["images", "videos"], documentTypes: "*/*" };
	const types = parts.map((part) => (part.startsWith(".") ? EXTENSION_TYPES[part] : part)).filter((type): type is string => Boolean(type));
	if (!types.length) return { photos: true, mediaTypes: ["images", "videos"], documentTypes: "*/*" };
	const mediaTypes: Array<"images" | "videos"> = [];
	if (types.some((type) => type.startsWith("image/"))) mediaTypes.push("images");
	if (types.some((type) => type.startsWith("video/"))) mediaTypes.push("videos");
	return { photos: mediaTypes.length > 0, mediaTypes, documentTypes: [...new Set(types)] };
}

type PickedAsset = { uri: string; fileName?: string | null; name?: string; mimeType?: string | null };
export type Pickers = {
	launchImageLibraryAsync(options: Record<string, unknown>): Promise<{ canceled: boolean; assets: PickedAsset[] | null }>;
	getDocumentAsync(options: Record<string, unknown>): Promise<{ canceled: boolean; assets: PickedAsset[] | null }>;
};

async function loadPickers(): Promise<Pickers> {
	const [ImagePicker, DocumentPicker] = await Promise.all([import("expo-image-picker"), import("expo-document-picker")]);
	return {
		launchImageLibraryAsync: (options) => ImagePicker.launchImageLibraryAsync(options) as ReturnType<Pickers["launchImageLibraryAsync"]>,
		getDocumentAsync: (options) => DocumentPicker.getDocumentAsync(options) as ReturnType<Pickers["getDocumentAsync"]>,
	};
}

/** The files the person picked, or none if they cancelled. */
export async function pickFiles(source: "photos" | "files", dialog: FileChooserDialog, pickers?: Pickers): Promise<BrowserPickedFile[]> {
	const use = pickers ?? await loadPickers();
	const options = pickerFor(dialog.accept);
	const result = source === "photos"
		? await use.launchImageLibraryAsync({ mediaTypes: options.mediaTypes, allowsMultipleSelection: dialog.multiple, quality: 1 })
		: await use.getDocumentAsync({ multiple: dialog.multiple, copyToCacheDirectory: true, type: options.documentTypes });
	if (result.canceled || !result.assets) return [];
	return result.assets.map((asset) => ({
		uri: asset.uri,
		name: safeName(asset.fileName ?? asset.name ?? asset.uri.split("/").pop() ?? "file"),
		...(asset.mimeType ? { type: asset.mimeType } : {}),
	}));
}

/** The page names the file: only its last part, with no way out of the folder. */
function safeName(name: string): string {
	const last = name.split(/[\\/]/).pop() ?? "";
	const clean = Array.from(last).filter((char) => char.charCodeAt(0) >= 0x20 && char !== "\u007f").join("").replace(/^\.+/, "").trim();
	return clean.slice(0, 200) || "download";
}

export type DownloadSaver = {
	/** Fetches the file into the app's own cache. */
	download(url: string, headers: Record<string, string>, name: string): Promise<{ uri: string }>;
	/** Hands it to the person: the Share sheet on iOS, a folder they pick on Android. */
	keep(uri: string, name: string): Promise<void>;
};

async function loadSaver(): Promise<DownloadSaver> {
	const [{ Directory, File, Paths }, { Platform, Share }] = await Promise.all([import("expo-file-system"), import("react-native")]);
	return {
		async download(url, headers, name) {
			const directory = new Directory(Paths.cache, "browser-downloads");
			if (!directory.exists) directory.create({ intermediates: true, idempotent: true });
			const target = new File(directory, name);
			if (target.exists) target.delete();
			return File.downloadFileAsync(url, target, { headers, idempotent: true });
		},
		async keep(uri, name) {
			if (Platform.OS === "ios") {
				await Share.share({ url: uri, title: name });
				return;
			}
			const folder = await Directory.pickDirectoryAsync();
			const source = new File(uri);
			const target = folder.createFile(name, source.type || "application/octet-stream");
			target.write(await source.bytes());
		},
	};
}

export async function saveDownload(
	download: BrowserDownload,
	request: (id: string) => Promise<{ url: string; headers: Record<string, string> }>,
	saver?: DownloadSaver,
): Promise<void> {
	const use = saver ?? await loadSaver();
	const name = safeName(download.name);
	const { url, headers } = await request(download.id);
	const file = await use.download(url, headers, name);
	await use.keep(file.uri, name);
}
