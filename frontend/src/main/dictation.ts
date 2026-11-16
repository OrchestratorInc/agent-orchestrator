import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { app, ipcMain, type WebContents } from "electron";
import type { DictationEvent, DictationMessage } from "../shared/dictation";

// One dictation at a time, run by the native helper and streamed to the surface
// that started it: the shell renderer, or a browser page's annotation composer
// (see `isAllowedSender`). Returns `stop`, which finishes a running dictation
// and keeps its text.
export function registerDictationIpc(
	helperPath: string,
	isAllowedSender: (sender: WebContents) => boolean,
): { stop(): void } {
	let helper: ChildProcess | null = null;
	let owner: WebContents | null = null;
	/** The run number the owner gave for the running dictation. */
	let ownerRun = 0;
	const watched = new WeakSet<WebContents>();
	const stop = () => {
		helper?.stdin?.write("stop\n");
	};
	const cancel = () => {
		helper?.stdin?.write("cancel\n");
		helper = null;
		owner = null;
	};
	app.on("will-quit", () => helper?.kill());

	// A surface that closes, crashes or leaves its page mid-dictation cannot
	// cancel; stop listening for it.
	const watch = (sender: WebContents) => {
		if (watched.has(sender)) return;
		watched.add(sender);
		const release = () => {
			if (owner === sender) cancel();
		};
		sender.once("destroyed", release);
		sender.on("render-process-gone", release);
		sender.on("did-navigate", release);
	};

	// Resolves to whether the dictation started, so a refused surface does not
	// wait for events that never come.
	ipcMain.handle("dictation:start", (event, run: unknown): boolean => {
		if (process.platform !== "darwin" || !isAllowedSender(event.sender)) return false;
		const sender = event.sender;
		const runId = typeof run === "number" && Number.isSafeInteger(run) ? run : 0;
		const previous = helper;
		// Another surface's dictation ends here; tell it, since the helper it was
		// listening to goes quiet. Within one renderer the new owner ends the old one.
		if (previous && owner && owner !== sender && !owner.isDestroyed())
			owner.send("dictation:event", { type: "superseded", run: ownerRun } satisfies DictationMessage);
		const child = spawn(helperPath, [], { stdio: ["pipe", "pipe", "ignore"] });
		helper = child;
		owner = sender;
		ownerRun = runId;
		watch(sender);
		// A helper that was replaced or cancelled is still shutting down; its
		// events, exit included, belong to a dictation that already ended.
		const send = (message: DictationEvent) => {
			if (helper === child && !sender.isDestroyed()) sender.send("dictation:event", { ...message, run: runId } satisfies DictationMessage);
		};
		previous?.kill();
		// A stop written while the helper is exiting would otherwise throw EPIPE in main.
		child.stdin?.on("error", () => undefined);
		createInterface({ input: child.stdout! }).on("line", (line) => {
			try {
				send(JSON.parse(line) as DictationEvent);
			} catch {
				// Not a protocol line.
			}
		});
		child.on("error", () => send({ type: "error", message: "Dictation is not available." }));
		// "close", not "exit": exit can arrive before the last stdout lines, so the
		// renderer would hear "exit" ahead of the helper's final "end".
		child.on("close", () => {
			send({ type: "exit" });
			if (helper === child) {
				helper = null;
				owner = null;
			}
		});
		return true;
	});

	// Only the surface that started the running dictation may stop or cancel it.
	ipcMain.handle("dictation:stop", (event) => {
		if (event.sender === owner) stop();
	});

	ipcMain.handle("dictation:cancel", (event) => {
		if (event.sender === owner) cancel();
	});

	return { stop };
}
