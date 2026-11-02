import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { ipcMain, type WebContents } from "electron";
import type { DictationEvent } from "../shared/dictation";

// One dictation at a time, run by the native helper and streamed to the shell renderer.
export function registerDictationIpc(helperPath: string, shellWebContents: () => WebContents | null): void {
	let helper: ChildProcess | null = null;

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
		createInterface({ input: child.stdout! }).on("line", (line) => {
			try {
				send(JSON.parse(line) as DictationEvent);
			} catch {
				// Not a protocol line.
			}
		});
		child.on("error", () => send({ type: "error", message: "Dictation is not available." }));
		child.on("exit", () => {
			send({ type: "exit" });
			if (helper === child) helper = null;
		});
	});

	ipcMain.handle("dictation:stop", (event) => {
		if (event.sender === shellWebContents()) helper?.stdin?.write("stop\n");
	});

	ipcMain.handle("dictation:cancel", (event) => {
		if (event.sender !== shellWebContents()) return;
		helper?.stdin?.write("cancel\n");
		helper = null;
	});
}
