import { describe, expect, it, vi } from "vitest";
import { createDialogApprover } from "./browser-vault-approval";

const request = { kind: "credential-fill" as const, sessionId: "s1", origin: "https://a.test", itemLabel: "A", username: "alice" };

describe("dialog approver", () => {
  it.each([
    [0, "allow-always"],
    [1, "allow-once"],
    [2, "deny"],
    [7, "deny"],
  ] as const)("maps button %i to %s", async (response, decision) => {
    const show = vi.fn(async (_window: unknown, _options: unknown) => ({ response }));
    await expect(createDialogApprover(show, () => undefined)(request)).resolves.toBe(decision);
  });

  it("names the session and site, defaults to deny, and never shows a secret", async () => {
    const show = vi.fn(async (_window: unknown, _options: unknown) => ({ response: 2 }));
    await createDialogApprover(show, () => undefined)(request);
    const options = show.mock.calls[0][1] as { message: string; defaultId: number; cancelId: number };
    expect(options.message).toContain("s1");
    expect(options.message).toContain("https://a.test");
    expect(options.defaultId).toBe(2);
    expect(options.cancelId).toBe(2);
  });
});
