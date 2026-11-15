import { describe, expect, it } from "vitest";
import type { CloudAccount } from "../shared/cloud-account";
import { createPendingCloudSignIn } from "./pending-cloud-sign-in";

const account = (id: string): CloudAccount => ({
	authProvider: "workos",
	user: { id, email: `${id}@example.com`, displayName: id },
	storedAt: "2026-11-04T00:00:00Z",
});

describe("the pending cloud sign-in", () => {
	it("hands a sign-in's context over once, whenever it is taken", () => {
		const pending = createPendingCloudSignIn();
		pending.note(account("user_1"), { webDistinctId: "web-1" });
		// No window was listening yet: a token refresh comes and goes first.
		pending.note(account("user_1"));
		expect(pending.take()).toEqual({ account: account("user_1"), signIn: { webDistinctId: "web-1" } });
		expect(pending.take()).toBeNull();
	});

	it("keeps only the newest sign-in, and drops it on sign-out", () => {
		const pending = createPendingCloudSignIn();
		pending.note(account("user_1"), { webDistinctId: "web-1" });
		pending.note(account("user_2"), { webDistinctId: "web-2" });
		expect(pending.take()?.account.user.id).toBe("user_2");
		pending.note(account("user_3"), { webDistinctId: "web-3" });
		pending.note(null);
		expect(pending.take()).toBeNull();
	});
});
