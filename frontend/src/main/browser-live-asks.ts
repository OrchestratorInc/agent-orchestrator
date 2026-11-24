import { randomBytes } from "node:crypto";
import { mkdir, rm, writeFile, appendFile } from "node:fs/promises";
import path from "node:path";
import { AoError } from "../../../packages/shared/telemetry/ao-error";
import type { BrowserLiveAsk, BrowserRemoteAuth, BrowserRemoteFiles } from "./browser-live-types";

/** A phone's files for one chooser, all together: more is refused. */
export const MAX_UPLOAD_BYTES = 25 << 20;
const MAX_UPLOAD_FILES = 20;
/** The page's sign-in waits this long for the phone, then is cancelled. */
const AUTH_WAIT_MS = 60_000;
/** A second request for an origin this soon after an answer means it failed. */
const AUTH_RETRY_MS = 15_000;

type AskSink = { ask?(ask: BrowserLiveAsk): void; askClosed?(): void; notice?(message: string): void };
type Chooser = { setFiles(paths: string[]): Promise<void> };
type PendingUpload = { id: string; chooser: Chooser; dir: string; files: Array<{ name: string; path: string; bytes: number }>; bytes: number };
type PendingAuth = { origin: string; answer(username?: string, password?: string): void; timer: ReturnType<typeof setTimeout> };

/** The name a phone gave a file: only its last part, and never one that leaves the folder. */
function safeName(name: string): string {
	const last = String(name).split(/[\\/]/).pop() ?? "";
	const clean = Array.from(last).filter((char) => char.charCodeAt(0) >= 0x20 && char !== "\u007f").join("").replace(/^\.+/, "").trim();
	return clean.slice(0, 200) || "file";
}

/**
 * What a watched page asks the person on the phone, while one watches: its file
 * inputs and HTTP sign-ins. The phone's files arrive in chunks (each command is
 * capped at 64 KiB), are kept under AO's own data directory, and go when the
 * stream does.
 */
export class LiveAsks {
	private upload: PendingUpload | null = null;
	private auth: PendingAuth | null = null;
	private lastAuth: { origin: string; at: number } | null = null;
	private readonly dirs = new Set<string>();

	constructor(private readonly options: { uploadDir: string }, private readonly sink: AskSink) {}

	chooserOpened(chooser: Chooser, ask: { multiple: boolean; accept: string; folder: boolean }): void {
		const id = randomBytes(8).toString("hex");
		this.upload = { id, chooser, dir: path.join(this.options.uploadDir, randomBytes(12).toString("hex")), files: [], bytes: 0 };
		this.sink.ask?.({ kind: "filechooser", id, ...ask });
	}

	async receiveFiles(payload: BrowserRemoteFiles): Promise<void> {
		// Files picked for a chooser the page has since replaced never reach the newer one.
		const current = this.upload && payload.chooser === this.upload.id ? this.upload : null;
		if (payload.action === "cancel") {
			if (current) this.upload = null;
			return;
		}
		if (!current && this.upload) return;
		const upload = current;
		if (payload.action === "done") {
			if (!upload || !upload.files.length || upload.files.length !== payload.count) return;
			this.upload = null;
			await upload.chooser.setFiles(upload.files.map((file) => file.path));
			return;
		}
		if (!upload) throw new AoError("INVALID_ARGUMENT", "No file chooser is waiting for files");
		const data = Buffer.from(String(payload.data ?? ""), "base64");
		let file = upload.files[payload.file];
		if (!file) {
			if (payload.file !== upload.files.length || payload.offset !== 0 || upload.files.length >= MAX_UPLOAD_FILES) {
				throw new AoError("INVALID_ARGUMENT", "Files arrived out of order");
			}
			// Each file in its own folder: names may repeat, and each keeps its own.
			const fileDir = path.join(upload.dir, String(upload.files.length));
			await mkdir(fileDir, { recursive: true, mode: 0o700 });
			this.dirs.add(upload.dir);
			const name = safeName(payload.name);
			file = { name, path: path.join(fileDir, name), bytes: 0 };
			upload.files.push(file);
		} else if (payload.offset !== file.bytes) {
			throw new AoError("INVALID_ARGUMENT", "File data arrived out of order");
		}
		if (upload.bytes + data.byteLength > MAX_UPLOAD_BYTES) {
			this.upload = null;
			this.sink.notice?.(`Those files are too large to send to the page (${MAX_UPLOAD_BYTES >> 20} MB at most).`);
			throw new AoError("UPLOAD_TOO_LARGE", "The files are too large");
		}
		if (this.upload !== upload) return;
		await (file.bytes === 0 ? writeFile(file.path, data, { mode: 0o600 }) : appendFile(file.path, data));
		file.bytes += data.byteLength;
		upload.bytes += data.byteLength;
	}

	authRequired(request: { origin: string; realm?: string }, answer: (username?: string, password?: string) => void): void {
		this.cancelAuth();
		const failed = this.lastAuth !== null && this.lastAuth.origin === request.origin && Date.now() - this.lastAuth.at < AUTH_RETRY_MS;
		const pending: PendingAuth = {
			origin: request.origin,
			answer,
			timer: setTimeout(() => {
				if (this.auth !== pending) return;
				this.cancelAuth();
				this.sink.askClosed?.();
			}, AUTH_WAIT_MS),
		};
		pending.timer.unref?.();
		this.auth = pending;
		this.sink.ask?.({ kind: "auth", origin: request.origin, ...(request.realm ? { realm: request.realm } : {}), failed });
	}

	/** The phone's answer: credentials go to the page once, and are kept nowhere. */
	answerAuth(payload: BrowserRemoteAuth): void {
		const pending = this.auth;
		if (!pending || pending.origin !== payload.origin) return;
		this.auth = null;
		clearTimeout(pending.timer);
		if ("cancel" in payload) {
			pending.answer();
			return;
		}
		this.lastAuth = { origin: pending.origin, at: Date.now() };
		pending.answer(String(payload.username ?? ""), String(payload.password ?? ""));
	}

	private cancelAuth(): void {
		const pending = this.auth;
		if (!pending) return;
		this.auth = null;
		clearTimeout(pending.timer);
		pending.answer();
	}

	/** The phone stopped watching: nothing waits for it, and its files go. */
	async dispose(): Promise<void> {
		this.cancelAuth();
		this.upload = null;
		const dirs = [...this.dirs];
		this.dirs.clear();
		await Promise.all(dirs.map((dir) => rm(dir, { recursive: true, force: true })));
	}
}
