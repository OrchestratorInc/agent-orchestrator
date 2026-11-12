import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { app, ipcMain, type WebContents } from "electron";
import type { DictationEvent } from "../shared/dictation";

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
	const watched = new WeakSet<WebContents>();
	const stop = () => {
		helper?.stdin?.write("stop\n");
	};
	app.on("will-quit", () => helper?.kill());

	ipcMain.handle("dictation:start", (event) => {
		if (process.platform !== "darwin" || !isAllowedSender(event.sender)) return;
		const sender = event.sender;
		const previous = helper;
		// Another surface's dictation ends here; tell it, since the helper it was
		// listening to goes quiet. Within one renderer the new owner ends the old one.
		if (previous && owner && owner !== sender && !owner.isDestroyed()) owner.send("dictation:event", { type: "superseded" } satisfies DictationEvent);
		const child = spawn(helperPath, [], { stdio: ["pipe", "pipe", "ignore"] });
		helper = child;
		owner = sender;
		// A surface that closes mid-dictation cannot cancel; stop listening for it.
		if (!watched.has(sender)) {
			watched.add(sender);
			sender.once("destroyed", () => {
				if (owner !== sender) return;
				helper?.stdin?.write("cancel\n");
				helper = null;
				owner = null;
			});
		}
		// A helper that was replaced or cancelled is still shutting down; its
		// events, exit included, belong to a dictation that already ended.
		const send = (message: DictationEvent) => {
			if (helper === child && !sender.isDestroyed()) sender.send("dictation:event", message);
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
	});

	// Only the surface that started the running dictation may stop or cancel it.
	ipcMain.handle("dictation:stop", (event) => {
		if (event.sender === owner) stop();
	});

	ipcMain.handle("dictation:cancel", (event) => {
		if (event.sender !== owner) return;
		helper?.stdin?.write("cancel\n");
		helper = null;
		owner = null;
	});

	return { stop };
}
