// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FIND } from "./browser-vault-fill";

// Runs the real field finder against real DOM, so its heuristics are tested
// rather than stubbed. jsdom has no layout: give every element a box.
const runFind = () => (0, eval)(FIND) as Record<string, number>;
const fields = () => (window as unknown as { __aoVaultFields: HTMLInputElement[] }).__aoVaultFields;

describe("login field finder", () => {
  beforeEach(() => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ width: 100, height: 20 } as DOMRect);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("finds a 2FA field named only 'code'", () => {
    document.body.innerHTML = `<form><input name="code" type="text"><button>Verify</button></form>`;
    const roles = runFind();
    expect(roles).toEqual({ totp: 0 });
    expect(fields()[roles.totp].name).toBe("code");
  });

  it.each([
    [`<input autocomplete="one-time-code">`],
    [`<input id="otp">`],
    [`<input aria-label="Authenticator code">`],
    [`<input placeholder="6-digit verification code">`],
  ])("finds a 2FA field from %s", (html) => {
    document.body.innerHTML = html;
    expect(runFind()).toEqual({ totp: 0 });
  });

  it("does not take a field whose attributes only contain 'code' as part of other text", () => {
    document.body.innerHTML = `<input name="promo" placeholder="Discount code here">`;
    expect(runFind()).toEqual({});
  });

  it("finds username before password, ignoring hidden fields", () => {
    document.body.innerHTML = `<form><input type="hidden" name="csrf"><input type="email" name="u"><input type="password" name="p"></form>`;
    expect(runFind()).toEqual({ username: 0, password: 1 });
    expect(fields().map((el) => el.name)).toEqual(["u", "p"]);
  });

  it("ignores a new-password field", () => {
    document.body.innerHTML = `<input type="password" autocomplete="new-password">`;
    expect(runFind()).toEqual({});
  });
});
