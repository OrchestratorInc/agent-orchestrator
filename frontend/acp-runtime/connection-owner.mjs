// Each chat's connection to the shared adapter owns the processes and file
// watchers started while handling it, and the errors raised there. A chat is
// { sessionId, resources: Set, close() }.
import { AsyncLocalStorage } from "node:async_hooks";

const owner = new AsyncLocalStorage();

// runAsChat runs fn with chat as the owner of everything it starts.
export function runAsChat(chat, fn) {
	return owner.run(chat, fn);
}

// track records a child process or file watcher for the current chat.
export function track(resource) {
	const chat = owner.getStore();
	if (chat) {
		chat.resources.add(resource);
		resource.once(typeof resource.kill === "function" ? "exit" : "close", () => chat.resources.delete(resource));
	}
	return resource;
}

// release stops what a closed chat left running: watchers close, processes get
// SIGTERM and then SIGKILL.
export function release(resources, killAfterMs = 2_000) {
	for (const resource of resources) {
		if (typeof resource.kill !== "function") {
			resource.close();
			continue;
		}
		resource.kill("SIGTERM");
		setTimeout(() => resource.exitCode === null && resource.signalCode === null && resource.kill("SIGKILL"), killAfterMs);
	}
}

const label = (chat) => chat.sessionId || "(before its session started)";

// onUncaughtException closes only the chat whose code threw. With no owning
// chat the process exits, as it would without this handler.
export function onUncaughtException(err, exit = process.exit) {
	const chat = owner.getStore();
	if (!chat) {
		console.error("Uncaught exception outside any chat:", err);
		exit(1);
		return;
	}
	console.error(`Uncaught exception in chat ${label(chat)}; closing that chat:`, err);
	chat.close();
}

// onUnhandledRejection logs the rejection with its chat and keeps everything running.
export function onUnhandledRejection(reason) {
	const chat = owner.getStore();
	console.error(`Unhandled rejection in chat ${chat ? label(chat) : "(none)"}:`, reason);
}
