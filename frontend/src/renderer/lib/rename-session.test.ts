import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ renameSession: vi.fn() }));
vi.mock("./cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({ renameSession: h.renameSession }),
}));

import { renameCloudSession } from "./rename-session";

describe("renameCloudSession", () => {
	beforeEach(() => { h.renameSession.mockReset(); });

	it("renames on the control plane and shows the new title in the cached lists at once", async () => {
		const queryClient = new QueryClient();
		queryClient.setQueryData(["settings"], { cloudControlPlaneUrl: "https://cp.test" });
		queryClient.setQueryData(["cloud-sessions", "https://cp.test", "org-1"], [
			{ id: "s-1", displayName: "Old" },
			{ id: "s-2", displayName: "Other" },
		]);
		h.renameSession.mockResolvedValue({ session: { id: "s-1", displayName: "New name" } });
		const invalidate = vi.spyOn(queryClient, "invalidateQueries");

		await renameCloudSession(queryClient, { id: "s-1", cloud: { orgId: "org-1" } }, "New name");

		expect(h.renameSession).toHaveBeenCalledWith("org-1", "s-1", "New name");
		expect(queryClient.getQueryData(["cloud-sessions", "https://cp.test", "org-1"])).toEqual([
			{ id: "s-1", displayName: "New name" },
			{ id: "s-2", displayName: "Other" },
		]);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cloud-sessions"] });
	});

	it("refuses without a configured control plane", async () => {
		await expect(renameCloudSession(new QueryClient(), { id: "s-1", cloud: { orgId: "org-1" } }, "Name")).rejects.toThrow(/not configured/);
		expect(h.renameSession).not.toHaveBeenCalled();
	});
});
