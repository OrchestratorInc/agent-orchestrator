import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ renameSession: vi.fn() }));
vi.mock("./cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({ renameSession: h.renameSession }),
}));

import { renameCloudSession } from "./rename-session";

describe("renameCloudSession", () => {
	beforeEach(() => { h.renameSession.mockReset(); });

	it("renames on the control plane and returns the stored name, leaving the caches to the commit", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(["settings"], { cloudControlPlaneUrl: "https://cp.test" });
		const cached = [{ id: "s-1", displayName: "Old" }];
		queryClient.setQueryData(["cloud-sessions", "https://cp.test", "org-1"], cached);
		h.renameSession.mockResolvedValue({ session: { id: "s-1", displayName: "New name" } });

		await expect(renameCloudSession(queryClient, { id: "s-1", cloud: { orgId: "org-1" } }, "New name")).resolves.toBe("New name");

		expect(h.renameSession).toHaveBeenCalledWith("org-1", "s-1", "New name");
		// An older answer must not overwrite a newer rename, so showing it is commitSessionRename's job.
		expect(queryClient.getQueryData(["cloud-sessions", "https://cp.test", "org-1"])).toBe(cached);
	});

	it("refuses without a configured control plane", async () => {
		await expect(renameCloudSession(new QueryClient(), { id: "s-1", cloud: { orgId: "org-1" } }, "Name")).rejects.toThrow(/not configured/);
		expect(h.renameSession).not.toHaveBeenCalled();
	});
});
