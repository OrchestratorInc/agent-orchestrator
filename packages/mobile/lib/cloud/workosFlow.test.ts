import { describe, expect, it, vi } from "vitest";
import { runWorkOSSignIn } from "./workosFlow";

const tokens = { accessToken: "a", refreshToken: "r", expiresAt: 10_000 };

function deps(over: Partial<Parameters<typeof runWorkOSSignIn>[0]> = {}) {
	return {
		createPkce: async () => ({ verifier: "v", challenge: "c" }),
		makeState: () => "st",
		authUrl: ({ challenge, state }: { challenge: string; state: string }) =>
			`https://api.workos.com/user_management/authorize?code_challenge=${challenge}&state=${state}`,
		openAuth: vi.fn(async () => ({ type: "success" as const, url: "aoagents.ao://callback?code=xyz&state=st" })),
		exchange: vi.fn(async () => tokens),
		...over,
	};
}

describe("runWorkOSSignIn", () => {
	it("exchanges the returned code with the matching verifier", async () => {
		const d = deps();
		expect(await runWorkOSSignIn(d)).toEqual(tokens);
		expect(d.exchange).toHaveBeenCalledWith(expect.objectContaining({ code: "xyz", codeVerifier: "v" }));
	});

	it("returns null when the user dismisses the browser", async () => {
		const d = deps({ openAuth: vi.fn(async () => ({ type: "cancel" as const })) });
		expect(await runWorkOSSignIn(d)).toBeNull();
		expect(d.exchange).not.toHaveBeenCalled();
	});

	// Without this check a crafted deep link could complete a sign-in the user
	// never started.
	it("refuses a callback whose state does not match the request", async () => {
		const d = deps({
			openAuth: vi.fn(async () => ({ type: "success" as const, url: "aoagents.ao://callback?code=xyz&state=attacker" })),
		});
		await expect(runWorkOSSignIn(d)).rejects.toThrow(/state/i);
		expect(d.exchange).not.toHaveBeenCalled();
	});

	it("surfaces an error the provider returned instead of a code", async () => {
		const d = deps({
			openAuth: vi.fn(async () => ({ type: "success" as const, url: "aoagents.ao://callback?error=access_denied&state=st" })),
		});
		await expect(runWorkOSSignIn(d)).rejects.toThrow(/access_denied/);
		expect(d.exchange).not.toHaveBeenCalled();
	});

	it("rejects a callback carrying neither a code nor an error", async () => {
		const d = deps({
			openAuth: vi.fn(async () => ({ type: "success" as const, url: "aoagents.ao://callback?state=st" })),
		});
		await expect(runWorkOSSignIn(d)).rejects.toThrow();
		expect(d.exchange).not.toHaveBeenCalled();
	});
});
