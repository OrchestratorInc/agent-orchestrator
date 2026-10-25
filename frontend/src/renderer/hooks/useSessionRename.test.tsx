import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { queryClient } from "../lib/query-client";
import { settingsQueryKey } from "./useSettings";
import { useSessionRename } from "./useSessionRename";

const h = vi.hoisted(() => ({ cloudRename: vi.fn(), createClient: vi.fn(), daemonRename: vi.fn() }));
vi.mock("./useCloudCp", () => ({ createRendererCloudCpClient: h.createClient }));
vi.mock("../lib/rename-session", () => ({ renameSession: h.daemonRename }));

beforeEach(() => {
	vi.clearAllMocks();
	h.createClient.mockReturnValue({ renameSession: h.cloudRename });
	h.cloudRename.mockResolvedValue({ session: {} });
	h.daemonRename.mockResolvedValue(undefined);
});

async function rename(session: Parameters<typeof useSessionRename>[0], name: string, cloudControlPlaneUrl = "http://cp.test") {
	queryClient.setQueryData(settingsQueryKey, { cloudControlPlaneUrl });
	const onRenamed = vi.fn();
	const { result } = renderHook(() => useSessionRename(session, onRenamed));
	act(() => result.current.begin());
	act(() => result.current.setDraft(name));
	await act(() => result.current.commit());
	return onRenamed;
}

describe("useSessionRename", () => {
	it("renames a cloud session on its control plane, as a local one is renamed on the daemon", async () => {
		const onRenamed = await rename({ id: "s-1", title: "old", cloud: { orgId: "org-1" } }, " Fix login ");
		expect(h.createClient).toHaveBeenCalledWith("http://cp.test");
		expect(h.cloudRename).toHaveBeenCalledWith("org-1", "s-1", "Fix login");
		expect(h.daemonRename).not.toHaveBeenCalled();
		expect(onRenamed).toHaveBeenCalled();
	});

	it("does not rename a cloud session without a configured control plane", async () => {
		const onRenamed = await rename({ id: "s-1", title: "old", cloud: { orgId: "org-1" } }, "Fix login", "");
		expect(h.cloudRename).not.toHaveBeenCalled();
		expect(onRenamed).not.toHaveBeenCalled();
	});

	it("renames a local session on the daemon", async () => {
		await rename({ id: "s-2", title: "old" }, "New name");
		expect(h.daemonRename).toHaveBeenCalledWith("s-2", "New name");
		expect(h.cloudRename).not.toHaveBeenCalled();
	});
});
