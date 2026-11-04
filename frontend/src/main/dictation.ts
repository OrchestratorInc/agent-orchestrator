import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { app, ipcMain, type WebContents } from "electron";
import type { DictationEvent } from "../shared/dictation";

// One dictation at a time, run by the native helper and streamed to the shell
// renderer. Returns `stop`, which finishes a running dictation and keeps its text.
export function registerDictationIpc(helperPath: string, shellWebContents: () => WebContents | null): { stop(): void } {
	let helper: ChildProcess | null = null;
	const stop = () => {
		helper?.stdin?.write("stop\n");
	};
	app.on("will-quit", () => helper?.kill());

	ipcMain.handle("dictation:start", (event) => {
		if (process.platform !== "darwin" || event.sender !== shellWebContents()) return;
		const sender = event.sender;
		const previous = helper;
		const child = spawn(helperPath, [], { stdio: ["pipe", "pipe", "ignore"] });
		helper = child;
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
			if (helper === child) helper = null;
		});
	});

	ipcMain.handle("dictation:stop", (event) => {
		if (event.sender === shellWebContents()) stop();
	});

	ipcMain.handle("dictation:cancel", (event) => {
		if (event.sender !== shellWebContents()) return;
		helper?.stdin?.write("cancel\n");
		helper = null;
	});

	return { stop };
}
