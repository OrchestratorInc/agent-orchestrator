// Each chat's connection to the shared adapter owns the processes and file
// watchers started while handling it, and the errors raised there. A chat is
// { sessionId, resources: Set, released, close(), onSpawn(child) }.
import { AsyncLocalStorage } from "node:async_hooks";

const owner = new AsyncLocalStorage();

// runAsChat runs fn with chat as the owner of everything it starts.
export function runAsChat(chat, fn) {
	return owner.run(chat, fn);
}

// track records a child process or file watcher for the current chat. A
// process stays recorded after it exits: its process group can outlive it.
export function track(resource) {
	const chat = owner.getStore();
	if (!chat) return resource;
	// Work still running when its chat was released, such as a session/new that
	// fails after the release, is stopped as soon as it starts something.
	if (chat.released) {
		stop(resource, 2_000);
		return resource;
	}
	chat.resources.add(resource);
	if (typeof resource.kill !== "function") resource.once("close", () => chat.resources.delete(resource));
	return resource;
}

// ownedSpawn wraps child_process.spawn so each process a chat starts leads its
// own process group: release then stops it and everything it started, and
// nothing of another chat.
export function ownedSpawn(spawn) {
	return (command, args, options) => {
		if (!Array.isArray(args)) [args, options] = [[], args];
		const chat = owner.getStore();
		if (!chat) return spawn(command, args, options);
		const child = track(spawn(command, args, { ...options, detached: true }));
		if (child.pid) chat.onSpawn?.(child);
		return child;
	};
}

const signalGroup = (child, signal) => {
	try {
		process.kill(-child.pid, signal);
	} catch {
		// The group has already exited.
	}
};

const stop = (resource, killAfterMs) => {
	if (typeof resource.kill !== "function") {
		resource.close();
		return;
	}
	signalGroup(resource, "SIGTERM");
	setTimeout(() => signalGroup(resource, "SIGKILL"), killAfterMs);
};

// release stops what a closed chat left running and anything it starts later:
// watchers close, and each process group gets SIGTERM and then SIGKILL, as the
// chat host does with a provider's group.
export function release(chat, killAfterMs = 2_000) {
	chat.released = true;
	for (const resource of chat.resources) stop(resource, killAfterMs);
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
