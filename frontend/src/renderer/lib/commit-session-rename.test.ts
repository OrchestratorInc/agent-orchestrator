import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../api/schema";
import { renameReadStarted, resetOptimisticRenamesForTest } from "../hooks/optimistic-renames";
import { applyOptimisticCloudSessionTitles, applyOptimisticSessionViewTitles } from "../hooks/optimistic-session-titles";
import { cloudSessionsQueryKey, remoteSessionsQueryKey, remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSummary } from "../types/workspace";
import type { CloudCpSession } from "./cloud-cp";

type SessionView = components["schemas"]["ControllersSessionView"];

const h = vi.hoisted(() => ({ renameSession: vi.fn(), renameCloudSession: vi.fn() }));
vi.mock("./rename-session", () => ({ renameSession: h.renameSession, renameCloudSession: h.renameCloudSession }));

import { commitSessionRename } from "./commit-session-rename";

type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void; reject: (error: unknown) => void };
function deferred<T>(): Deferred<T> {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

beforeEach(() => {
	resetOptimisticRenamesForTest();
	useUiStore.getState().clearGlobalToast();
	h.renameSession.mockReset();
	h.renameCloudSession.mockReset();
});

describe("renaming a session on a remote host", () => {
	const target = { id: "s-1", hostId: "box", title: "Old" };
	const workspaces = (title: string): WorkspaceSummary[] => [
		{ id: "p-1", name: "repo", path: "/repo", hostId: "box", sessions: [{ ...target, workspaceId: "p-1", workspaceName: "repo", title }] } as unknown as WorkspaceSummary,
	];
	const views = [{ id: "s-1", projectId: "p-1", displayName: "Old", terminalTitle: "vim", isTerminated: false, updatedAt: "" }] as SessionView[];
	const remoteClient = () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(remoteWorkspaceQueryKey("box"), workspaces("Old"));
		queryClient.setQueryData(remoteSessionsQueryKey("box"), views);
		return queryClient;
	};
	const shownIn = (queryClient: QueryClient) => ({
		list: queryClient.getQueryData<WorkspaceSummary[]>(remoteWorkspaceQueryKey("box"))?.[0]?.sessions[0]?.title,
		view: queryClient.getQueryData<typeof views>(remoteSessionsQueryKey("box"))?.[0],
	});

	it("writes the new name to the host's lists before the request returns", () => {
		h.renameSession.mockReturnValue(new Promise(() => {}));
		const queryClient = remoteClient();

		void commitSessionRename(queryClient, target, "  New  ");

		expect(h.renameSession).toHaveBeenCalledWith("s-1", "New", "box");
		const shown = shownIn(queryClient);
		expect(shown.list).toBe("New");
		// A given name hides the program's title, as it does on the daemon.
		expect(shown.view).toMatchObject({ displayName: "New" });
		expect(shown.view?.terminalTitle).toBeUndefined();
	});

	it("sends a newer name only after the older request answers, and keeps it", async () => {
		const older = deferred<void>();
		const newer = deferred<void>();
		h.renameSession.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
		const queryClient = remoteClient();
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		const first = commitSessionRename(queryClient, target, "First");
		const second = commitSessionRename(queryClient, { ...target, title: "First" }, "Second");
		// The host stores renames in arrival order, so the newer one waits.
		expect(h.renameSession).toHaveBeenCalledTimes(1);
		older.resolve();
		await first;
		await vi.waitFor(() => expect(h.renameSession).toHaveBeenCalledTimes(2));
		expect(h.renameSession).toHaveBeenLastCalledWith("s-1", "Second", "box");
		// The older answer does not bring its name back.
		expect(shownIn(queryClient).list).toBe("Second");
		newer.resolve();
		await second;

		expect(shownIn(queryClient).list).toBe("Second");
	});

	it("still sends a newer name after an older request fails", async () => {
		h.renameSession.mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(undefined);
		const queryClient = remoteClient();
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		const first = commitSessionRename(queryClient, target, "First");
		const second = commitSessionRename(queryClient, { ...target, title: "First" }, "Second");

		await expect(first).resolves.toBe(false);
		await expect(second).resolves.toBe(true);
		expect(shownIn(queryClient).list).toBe("Second");
		// The failure was superseded before it landed, so nothing is said.
		expect(useUiStore.getState().globalToasts).toHaveLength(0);
	});

	it("rolls a failure back to the program's title, not a given name made from it", async () => {
		h.renameSession.mockRejectedValue(new Error("boom"));
		const queryClient = new QueryClient();
		// The tab shows the program's title ("vim"); the given name is still "Old".
		queryClient.setQueryData(remoteWorkspaceQueryKey("box"), workspaces("vim"));
		queryClient.setQueryData(remoteSessionsQueryKey("box"), views);
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		await expect(commitSessionRename(queryClient, { ...target, title: "vim" }, "New")).resolves.toBe(false);

		const shown = shownIn(queryClient);
		expect(shown.list).toBe("vim");
		expect(shown.view).toMatchObject({ displayName: "Old", terminalTitle: "vim" });
	});

	it("shows its rename over a host read that began before the answer", async () => {
		const answer = deferred<void>();
		h.renameSession.mockReturnValue(answer.promise);
		const queryClient = remoteClient();
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
		const pending = commitSessionRename(queryClient, target, "New");
		const staleRead = renameReadStarted();
		answer.resolve();
		await pending;
		const freshRead = renameReadStarted();

		expect(applyOptimisticSessionViewTitles(views, "box", staleRead)[0]).toMatchObject({ displayName: "New", terminalTitle: undefined });
		// The same id on another host, or a read begun after the answer, is left alone.
		expect(applyOptimisticSessionViewTitles(views, "other", staleRead)).toBe(views);
		expect(applyOptimisticSessionViewTitles(views, "box", freshRead)).toBe(views);
	});
});

describe("renaming without a query cache", () => {
	it("still runs the caller's refresh once the rename is stored", async () => {
		h.renameSession.mockResolvedValue(undefined);
		const refresh = vi.fn();

		await expect(commitSessionRename(null, { id: "s-1", title: "Old" }, "New", { refresh })).resolves.toBe(true);

		expect(h.renameSession).toHaveBeenCalledWith("s-1", "New");
		expect(refresh).toHaveBeenCalledTimes(1);
	});
});

describe("renaming a Cloud session", () => {
	const cloud = { orgId: "org-1" };
	const target = { id: "c-1", title: "Old", cloud };
	const listKey = [...cloudSessionsQueryKey, "https://cp.test", "org-1"];
	const lookupKey = ["cloud-session", "https://cp.test", "org-1", "c-1"];
	const sessions = [{ id: "c-1", displayName: "Old" }, { id: "c-2", displayName: "Other" }] as CloudCpSession[];
	const cloudClient = () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(listKey, sessions);
		queryClient.setQueryData(lookupKey, sessions[0]);
		return queryClient;
	};
	const shownIn = (queryClient: QueryClient) => ({
		list: queryClient.getQueryData<CloudCpSession[]>(listKey)?.map((session) => session.displayName),
		lookup: queryClient.getQueryData<CloudCpSession>(lookupKey)?.displayName,
	});

	it("writes the new name to the control plane's lists at once, then the stored name, and reads them again", async () => {
		const answer = deferred<string>();
		h.renameCloudSession.mockReturnValue(answer.promise);
		const queryClient = cloudClient();
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		const done = commitSessionRename(queryClient, target, "New");
		expect(h.renameCloudSession).toHaveBeenCalledWith(queryClient, { id: "c-1", cloud }, "New");
		expect(shownIn(queryClient)).toEqual({ list: ["New", "Other"], lookup: "New" });

		const staleRead = renameReadStarted();
		answer.resolve("New (stored)");
		await expect(done).resolves.toBe(true);
		expect(shownIn(queryClient)).toEqual({ list: ["New (stored)", "Other"], lookup: "New (stored)" });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: cloudSessionsQueryKey });
		// A read begun before the answer shows the stored name, not the one sent.
		expect(applyOptimisticCloudSessionTitles(sessions, staleRead).map((session) => session.displayName)).toEqual(["New (stored)", "Other"]);
	});

	it("rolls a failed rename back and says so", async () => {
		h.renameCloudSession.mockRejectedValue(new Error("Forbidden"));
		const queryClient = cloudClient();
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		await expect(commitSessionRename(queryClient, target, "New")).resolves.toBe(false);

		expect(shownIn(queryClient)).toEqual({ list: ["Old", "Other"], lookup: "Old" });
		expect(useUiStore.getState().globalToasts).toEqual([
			expect.objectContaining({ title: "Couldn't rename to New", body: "Forbidden", tone: "error" }),
		]);
	});

	it("rolls a failure back to no given name when the title was the session id", async () => {
		h.renameCloudSession.mockRejectedValue(new Error("Forbidden"));
		const queryClient = new QueryClient();
		queryClient.setQueryData(listKey, [{ id: "c-1", displayName: "" }] as CloudCpSession[]);
		vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

		await expect(commitSessionRename(queryClient, { ...target, title: "c-1" }, "New")).resolves.toBe(false);

		expect(queryClient.getQueryData<CloudCpSession[]>(listKey)?.[0]?.displayName).toBe("");
	});

	it("shows an in-flight rename over a control-plane read", () => {
		h.renameCloudSession.mockReturnValue(new Promise(() => {}));
		const read = renameReadStarted();
		void commitSessionRename(cloudClient(), target, "New");

		expect(applyOptimisticCloudSessionTitles(sessions, read).map((session) => session.displayName)).toEqual(["New", "Other"]);
		expect(applyOptimisticCloudSessionTitles(sessions).map((session) => session.displayName)).toEqual(["New", "Other"]);
	});
});
