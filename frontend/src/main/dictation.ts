import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { app, ipcMain, type WebContents } from "electron";
import type { DictationEvent } from "../shared/dictation";

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
		const send = (message: DictationEvent) => {
			if (helper === child && !sender.isDestroyed()) sender.send("dictation:event", message);
		};
		previous?.kill();
		child.stdin?.on("error", () => undefined);
		createInterface({ input: child.stdout! }).on("line", (line) => {
			try {
				send(JSON.parse(line) as DictationEvent);
			} catch {
			}
		});
		child.on("error", () => send({ type: "error", message: "Dictation is not available." }));
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
