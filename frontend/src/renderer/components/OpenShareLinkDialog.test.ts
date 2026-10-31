import { describe, expect, it, vi } from "vitest";
import { redeemShareTarget } from "./OpenShareLinkDialog";

describe("opening a share link", () => {
	it("redeems a short link by its code and an older link by its organization and token", async () => {
		const shared = { shared: { sessionId: "s1" } } as never;
		const client = { redeemShareLink: vi.fn(async () => shared), redeemShareLinkCode: vi.fn(async () => shared) };
		await redeemShareTarget(client, { code: "k7Hq2x9PmB4w" });
		expect(client.redeemShareLinkCode).toHaveBeenCalledWith("k7Hq2x9PmB4w");
		expect(client.redeemShareLink).not.toHaveBeenCalled();
		await redeemShareTarget(client, { orgId: "org-1", token: "tok" });
		expect(client.redeemShareLink).toHaveBeenCalledWith("org-1", "tok");
	});
});
