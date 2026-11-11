import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vitest";
import { forgetRemoteHost, syncRemoteCache } from "./remote-chat-cache";

function seeded() {
	const client = new QueryClient();
	for (const hostId of ["box-a", "box-b"]) {
		client.setQueryData(["remote-conversation", hostId, "session-1"], { pages: [], pageParams: [] });
		client.setQueryData(["remote-workspaces", hostId, "sessions"], []);
		client.setQueryData(["remote-workspaces", hostId, "projects"], []);
	}
	client.setQueryData(["remote-conversation-refresh-error", "box-b", "session-1"], true);
	return client;
}

// IndexedDB is absent here; the real app proves the disk path.
it("keeps only the saved hosts' lists and chats", async () => {
	const client = seeded();
	await syncRemoteCache(client, ["box-a"]);
	expect(client.getQueryData(["remote-conversation", "box-a", "session-1"])).toBeDefined();
	expect(client.getQueryData(["remote-workspaces", "box-a", "sessions"])).toBeDefined();
	expect(client.getQueryData(["remote-conversation", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["remote-workspaces", "box-b", "projects"])).toBeUndefined();
	// Only kept queries are the cache's to prune.
	expect(client.getQueryData(["remote-conversation-refresh-error", "box-b", "session-1"])).toBe(true);
});

it("drops everything kept for a host that refused this device", async () => {
	const client = seeded();
	await forgetRemoteHost(client, "box-b");
	expect(client.getQueryData(["remote-conversation", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["remote-workspaces", "box-b", "sessions"])).toBeUndefined();
	expect(client.getQueryData(["remote-conversation", "box-a", "session-1"])).toBeDefined();
});
