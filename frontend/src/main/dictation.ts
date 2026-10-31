import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { ipcMain, type WebContents } from "electron";
import type { DictationEvent } from "../shared/dictation";

// One dictation at a time, run by the native helper and streamed to the shell renderer.
export function registerDictationIpc(helperPath: string, shellWebContents: () => WebContents | null): void {
	let helper: ChildProcess | null = null;

	ipcMain.handle("dictation:start", (event) => {
		if (process.platform !== "darwin" || event.sender !== shellWebContents()) return;
		helper?.kill();
		const sender = event.sender;
		const send = (message: DictationEvent) => {
			if (!sender.isDestroyed()) sender.send("dictation:event", message);
		};
		const child = spawn(helperPath, [], { stdio: ["pipe", "pipe", "ignore"] });
		helper = child;
		createInterface({ input: child.stdout! }).on("line", (line) => {
			try {
				send(JSON.parse(line) as DictationEvent);
			} catch {
				// Not a protocol line.
			}
		});
		child.on("error", () => send({ type: "error", message: "Dictation is not available." }));
		child.on("exit", () => {
			if (helper === child) helper = null;
			send({ type: "exit" });
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
