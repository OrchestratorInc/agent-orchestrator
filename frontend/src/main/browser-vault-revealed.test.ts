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

describe("filled field scripts", () => {
  beforeEach(() => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ width: 100, height: 20 } as DOMRect);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("reports focus on a filled field, not on others", async () => {
    const { filledFieldFocused } = await import("./browser-vault-fill");
    document.body.innerHTML = `<input id="pw" type="password"><input id="other">`;
    const pw = document.getElementById("pw") as Filled;
    pw.__aoVaultFilled = "password";
    pw.focus();
    expect((0, eval)(filledFieldFocused)).toBe(true);
    (document.getElementById("other") as HTMLInputElement).focus();
    expect((0, eval)(filledFieldFocused)).toBe(false);
  });

  it("returns the current values of filled fields, even after edits", async () => {
    const { filledFieldValues } = await import("./browser-vault-fill");
    document.body.innerHTML = `<input id="pw" type="password" value="Secret-pass-1"><input id="user" value="alice">`;
    const pw = document.getElementById("pw") as Filled;
    pw.__aoVaultFilled = "password";
    pw.value = "Secret-pass-";
    expect((0, eval)(filledFieldValues)).toEqual(["Secret-pass-"]);
  });
});

describe("tagging filled fields", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("tags a field that received a secret, but not one that received a username", async () => {
    const { selectVerified } = await import("./browser-vault-fill");
    document.body.innerHTML = `<input id="user"><input id="pw" type="password">`;
    const user = document.getElementById("user") as Filled & { __aoVaultToken?: string };
    const pw = document.getElementById("pw") as Filled & { __aoVaultToken?: string };
    user.__aoVaultToken = "t1";
    user.focus();
    expect((0, eval)(selectVerified("t1", ""))).toBe(true);
    expect(user.__aoVaultFilled).toBeUndefined();
    pw.__aoVaultToken = "t2";
    pw.focus();
    expect((0, eval)(selectVerified("t2", "password"))).toBe(true);
    expect(pw.__aoVaultFilled).toBe("password");
    // A field without the probe's token is not the verified one.
    expect((0, eval)(selectVerified("t3", "password"))).toBe(false);
  });
});

describe("focus probe", () => {
  beforeEach(() => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ width: 100, height: 20 } as DOMRect);
  });
  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("does not treat a textarea with type=password as a password field", async () => {
    const { probeFocus } = await import("./browser-vault-fill");
    document.body.innerHTML = `<textarea type="password"></textarea><input type="PASSWORD">`;
    (document.querySelector("textarea") as HTMLTextAreaElement).focus();
    expect((0, eval)(probeFocus("t"))).toMatchObject({ tag: "TEXTAREA", type: "" });
    (document.querySelector("input") as HTMLInputElement).focus();
    expect((0, eval)(probeFocus("t"))).toMatchObject({ tag: "INPUT", type: "password" });
  });
});

describe("value attribute of a filled field", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("is removed after the fill and stays removed when the page mirrors it back", async () => {
    const { afterInsert } = await import("./browser-vault-fill");
    document.body.innerHTML = `<input id="pw" type="password">`;
    const pw = document.getElementById("pw") as Filled & { __aoVaultToken?: string };
    pw.__aoVaultToken = "t1";
    pw.focus();
    // React mirrors a controlled input's value into the attribute.
    pw.setAttribute("value", "Secret-pass-1");
    expect((0, eval)(afterInsert("t1"))).toBe(true);
    expect(pw.hasAttribute("value")).toBe(false);
    expect(document.querySelector('input[value^="S"]')).toBeNull();
    pw.setAttribute("value", "Secret-pass-1");
    await Promise.resolve();
    expect(pw.hasAttribute("value")).toBe(false);
    expect(document.querySelector('input[value^="S"]')).toBeNull();
  });
});
