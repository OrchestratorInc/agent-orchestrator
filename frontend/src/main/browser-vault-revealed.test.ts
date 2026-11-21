// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { revealedSecretsCheck } from "./browser-vault-fill";

// Runs the real in-page check against real DOM. jsdom has no layout, so every
// element gets a box unless a test hides it.
type Filled = HTMLInputElement & { __aoVaultFilled?: string };
const check = (ignore: string[] = []) => (0, eval)(revealedSecretsCheck(ignore)) as boolean;

describe("revealed secret check", () => {
  beforeEach(() => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ width: 100, height: 20 } as DOMRect);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  const filled = (type: string, kind: string) => {
    document.body.innerHTML = `<input type="${type}">`;
    const el = document.querySelector("input") as Filled;
    el.__aoVaultFilled = kind;
    return el;
  };

  it("is false while a filled password stays type=password", () => {
    filled("password", "password");
    expect(check()).toBe(false);
  });

  it("is true once a filled password is turned into a text field", () => {
    const el = filled("password", "password");
    el.type = "text";
    expect(check()).toBe(true);
  });

  it("is true for a code in a visible text field, unless codes are ignored", () => {
    filled("text", "totp");
    expect(check()).toBe(true);
    expect(check(["totp"])).toBe(false);
  });

  it("is false for a hidden or removed field", () => {
    const el = filled("text", "password");
    el.style.display = "none";
    expect(check()).toBe(false);
    el.remove();
    expect(check()).toBe(false);
  });

  it("ignores fields Orchestrator did not fill", () => {
    document.body.innerHTML = `<input type="text" value="hello">`;
    expect(check()).toBe(false);
  });
});
