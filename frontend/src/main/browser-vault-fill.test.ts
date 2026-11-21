import { mkdtemp } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { SECRET_MASK } from "./browser-vault";
import { createCredentialFiller, type CdpLike, type FillTarget } from "./browser-vault-fill";
import { VaultStore, type VaultKeyProtector } from "./browser-vault-store";

const protector: VaultKeyProtector = {
  available: () => true,
  encrypt: (d) => Buffer.from(d),
  decrypt: (d) => Buffer.from(d),
};
const PASSWORD = "Tr0ub4dor&3-secret";
const ORIGIN = "http://localhost:4173";

type FakeField = { tag: string; type: string; role?: "username" | "password" | "totp"; visible?: boolean };
type FakeFrame = {
  id: string;
  url: string;
  focused?: FakeField;
  fields?: FakeField[];
  // Out-of-process frames cannot be scripted from the page's target.
  oopif?: boolean;
  // document.hasFocus(): false while the app window is in the background.
  hasFocus?: boolean;
  // The verified field lost focus (or the document changed) before typing.
  focusMovesBeforeTyping?: boolean;
  // A filled field of this kind is on the page, visible and unmasked (a code
  // in a text field, or a password revealed with an eye toggle).
  revealedKind?: "password" | "totp";
};

function fakeCdp(frames: FakeFrame[]) {
  const inserted: string[] = [];
  const calls: string[] = [];
  const contexts = new Map<number, FakeFrame>();
  let nextContext = 1;
  const cdp: CdpLike = {
    async sendCommand(method, params = {}) {
      calls.push(method);
      if (method === "Page.getFrameTree") {
        const [top, ...children] = frames;
        return {
          frameTree: {
            frame: { id: top.id, url: top.url },
            childFrames: children.map((f) => ({ frame: { id: f.id, url: f.url } })),
          },
        };
      }
      if (method === "Page.createIsolatedWorld") {
        const frame = frames.find((f) => f.id === params.frameId)!;
        if (frame.oopif) throw new Error("No frame for given id found");
        const id = nextContext++;
        contexts.set(id, frame);
        return { executionContextId: id };
      }
      if (method === "Runtime.evaluate") {
        const frame = contexts.get(params.contextId as number)!;
        const expression = String(params.expression);
        if (expression.includes("ao-vault:revealed")) {
          const kind = frame.revealedKind;
          return { result: { value: Boolean(kind) && !expression.includes(`ignore:${kind}`) } };
        }
        if (expression.includes("ao-vault:select")) {
          return { result: { value: !frame.focusMovesBeforeTyping } };
        }
        if (expression.includes("ao-vault:probe")) {
          return { result: { value: frame.focused ? { tag: frame.focused.tag, type: frame.focused.type, visible: frame.focused.visible ?? true, hasFocus: frame.hasFocus ?? true } : null } };
        }
        if (expression.includes("ao-vault:find")) {
          const roles: Record<string, number> = {};
          (frame.fields ?? []).forEach((f, i) => {
            if (f.role) roles[f.role] = i;
          });
          return { result: { value: roles } };
        }
        const focusMatch = /ao-vault:focus:(\d+)/.exec(expression);
        if (focusMatch) {
          for (const other of frames) other.focused = undefined;
          frame.focused = frame.fields![Number(focusMatch[1])];
          return { result: { value: true } };
        }
        return { result: { value: true } };
      }
      if (method === "Input.insertText") {
        inserted.push(String(params.text));
        return {};
      }
      return {};
    },
  };
  return { cdp, inserted, calls };
}

describe("credential filler", () => {
  let store: VaultStore;
  let itemId: string;

  beforeEach(async () => {
    const dir = await mkdtemp(path.join(os.tmpdir(), "ao-vault-fill-"));
    store = new VaultStore({ dir, protector });
    itemId = (
      await store.save({
        label: "Demo",
        origins: [ORIGIN],
        username: "alice",
        password: PASSWORD,
        totpSeed: "JBSWY3DPEHPK3PXP",
        allowedProjects: ["proj"],
      })
    ).id;
  });

  const target = (frames: FakeFrame[], pageURL = frames[0].url): FillTarget & ReturnType<typeof fakeCdp> => {
    const fake = fakeCdp(frames);
    return { ...fake, sessionId: "s1", projectId: "proj", pageURL: () => pageURL, documentKey: () => "tab1:0", approvedOrigin: ORIGIN };
  };

  it("types the password into the focused field and returns only a mask", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }]);
    const result = await filler.fillSecret(t, { itemId, field: "password" });
    expect(t.inserted).toEqual([PASSWORD]);
    expect(JSON.stringify(result)).not.toContain(PASSWORD);
    expect(result).toMatchObject({ filled: true, field: "password", value: SECRET_MASK });
    const [entry] = await store.readAudit();
    expect(entry).toMatchObject({ outcome: "filled", sessionId: "s1", itemId, origin: ORIGIN, field: "password" });
  });

  it("scrubs a filled secret from later results for the session", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }]);
    await filler.fillSecret(t, { itemId, field: "password" });
    const leaked = { value: PASSWORD, body: `password=${encodeURIComponent(PASSWORD)}` };
    expect(JSON.stringify(filler.scrub("s1", leaked))).not.toContain("Tr0ub4dor");
    expect(filler.scrub("other-session", leaked)).toEqual(leaked);
    filler.forget("s1");
    expect(filler.scrub("s1", leaked)).toEqual(leaked);
  });

  it.each([
    ["a lookalike top origin", [{ id: "top", url: "http://localhost.evil.test/login", focused: { tag: "INPUT", type: "password" } }]],
    ["https instead of http", [{ id: "top", url: "https://localhost:4173/login", focused: { tag: "INPUT", type: "password" } }]],
    [
      "a foreign iframe holding focus",
      [
        { id: "top", url: `${ORIGIN}/login` },
        { id: "evil", url: "https://evil.test/frame", focused: { tag: "INPUT", type: "password" } },
      ],
    ],
    [
      "the real site framed by a foreign page",
      [
        { id: "top", url: "https://evil.test/" },
        { id: "real", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } },
      ],
    ],
    [
      "an out-of-process frame holding focus",
      [
        { id: "top", url: `${ORIGIN}/login`, focused: { tag: "IFRAME", type: "" } },
        { id: "x", url: `${ORIGIN}/inner`, oopif: true },
      ],
    ],
    ["a visible text box for a password", [{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "text" } }]],
  ] as const)("refuses %s and audits it", async (_name, frames) => {
    const filler = createCredentialFiller({ store });
    const t = target(frames.map((f) => ({ ...f })) as FakeFrame[]);
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow();
    expect(t.inserted).toEqual([]);
    expect((await store.readAudit())[0]).toMatchObject({ outcome: "denied" });
  });

  it("fills the top page's focused field while the app window is in the background", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, hasFocus: false, focused: { tag: "INPUT", type: "password" } }]);
    await filler.fillSecret(t, { itemId, field: "password" });
    expect(t.inserted).toEqual([PASSWORD]);
  });

  it("login finds and fills the fields itself while the app window is in the background", async () => {
    const filler = createCredentialFiller({ store });
    const t = {
      ...target([
        {
          id: "top",
          url: `${ORIGIN}/login`,
          hasFocus: false,
          fields: [
            { tag: "INPUT", type: "email", role: "username" },
            { tag: "INPUT", type: "password", role: "password" },
          ],
        },
      ]),
      requestedOrigin: ORIGIN,
    };
    await expect(filler.login(t, {})).resolves.toMatchObject({ filled: ["username", "password"] });
    expect(t.inserted).toEqual(["alice", PASSWORD]);
  });

  it("does not trust a child frame's stale focus without document focus", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([
      { id: "top", url: `${ORIGIN}/login`, hasFocus: false, focused: { tag: "IFRAME", type: "" } },
      { id: "child", url: `${ORIGIN}/inner`, hasFocus: false, focused: { tag: "INPUT", type: "password" } },
    ]);
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/focused/);
    expect(t.inserted).toEqual([]);
  });

  it.each([
    ["http://accounts.example.test", true],
    ["http://10.0.0.5:8080", true],
    ["http://localhost:4173", false],
    ["http://127.0.0.1:9000", false],
    ["http://[::1]:3000", false],
    ["https://accounts.example.test", false],
  ])("on %s refuses an unencrypted fill: %s", async (origin, refused) => {
    const id = (await store.save({ origins: [origin], username: "u", password: "pw-transport-1", allowedProjects: [] })).id;
    const filler = createCredentialFiller({ store });
    const t = { ...target([{ id: "top", url: `${origin}/login`, focused: { tag: "INPUT", type: "password" } }]), approvedOrigin: origin };
    const fill = filler.fillSecret(t, { itemId: id, field: "password" });
    if (refused) {
      await expect(fill).rejects.toThrow(/HTTPS/);
      expect(t.inserted).toEqual([]);
    } else {
      await fill;
      expect(t.inserted).toEqual(["pw-transport-1"]);
    }
  });

  it("refuses when the verified field is no longer focused when typing starts", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, focusMovesBeforeTyping: true, focused: { tag: "INPUT", type: "password" } }]);
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/changed before/);
    expect(t.inserted).toEqual([]);
    expect((await store.readAudit())[0]).toMatchObject({ outcome: "denied", reason: "field changed" });
  });

  it("refuses a password too short to mask in later output", async () => {
    const id = (await store.save({ origins: [ORIGIN], username: "u", password: "abc", allowedProjects: [] })).id;
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }]);
    await expect(filler.fillSecret(t, { itemId: id, field: "password" })).rejects.toThrow(/too short/);
    expect(t.inserted).toEqual([]);
  });

  it("blocks agent screenshots while a filled code is visible in its document", async () => {
    let now = 1_000_000;
    const frames: FakeFrame[] = [{ id: "top", url: `${ORIGIN}/2fa`, focused: { tag: "INPUT", type: "text" } }];
    const filler = createCredentialFiller({ store, now: () => now });
    const t = { ...target(frames), documentKey: () => "tab1:3" };
    await filler.fillSecret(t, { itemId, field: "totp" });
    frames[0].revealedKind = "totp";
    await expect(filler.screenshotBlocked("s1", "tab1:3", t.cdp)).resolves.toMatch(/on screen/);
    await expect(filler.screenshotBlocked("other", "tab1:3", t.cdp)).resolves.toBeUndefined();
    // Another tab, or a replaced document, is not the one holding the code.
    await expect(filler.screenshotBlocked("s1", "tab2:0", t.cdp)).resolves.toBeUndefined();
    await expect(filler.screenshotBlocked("s1", "tab1:4", t.cdp)).resolves.toBeUndefined();
    await expect(filler.screenshotBlocked("s1", "tab1:3", t.cdp)).resolves.toMatch(/on screen/);
    // An expired code is no longer a secret worth hiding.
    now += 91_000;
    await expect(filler.screenshotBlocked("s1", "tab1:3", t.cdp)).resolves.toBeUndefined();
  });

  it("allows screenshots while a filled password stays masked, and blocks them once it is revealed", async () => {
    let now = 1_000_000;
    const frames: FakeFrame[] = [{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }];
    const filler = createCredentialFiller({ store, now: () => now });
    const t = { ...target(frames), documentKey: () => "tab1:0" };
    await filler.fillSecret(t, { itemId, field: "password" });
    await expect(filler.screenshotBlocked("s1", "tab1:0", t.cdp)).resolves.toBeUndefined();
    // The page (or the agent clicking an eye toggle) turns it into type=text.
    frames[0].revealedKind = "password";
    await expect(filler.screenshotBlocked("s1", "tab1:0", t.cdp)).resolves.toMatch(/on screen/);
    // A password does not expire the way a 2FA code does.
    now += 10 * 60_000;
    await expect(filler.screenshotBlocked("s1", "tab1:0", t.cdp)).resolves.toMatch(/on screen/);
    // Masked again: screenshots resume.
    frames[0].revealedKind = undefined;
    await expect(filler.screenshotBlocked("s1", "tab1:0", t.cdp)).resolves.toBeUndefined();
  });

  it("refuses the screenshot when the field cannot be checked", async () => {
    const frames: FakeFrame[] = [{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }];
    const filler = createCredentialFiller({ store });
    const t = { ...target(frames), documentKey: () => "tab1:0" };
    await filler.fillSecret(t, { itemId, field: "password" });
    const failing = { sendCommand: async () => { throw new Error("target crashed"); } };
    await expect(filler.screenshotBlocked("s1", "tab1:0", failing)).resolves.toMatch(/could not check/i);
  });

  it("refuses a project the item is not allowed in", async () => {
    const filler = createCredentialFiller({ store });
    const t = { ...target([{ id: "top", url: `${ORIGIN}/`, focused: { tag: "INPUT", type: "password" } }]), projectId: "other" };
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/project/i);
    expect(t.inserted).toEqual([]);
  });

  it("refuses a fill the daemon did not approve", async () => {
    const filler = createCredentialFiller({ store });
    const t = { ...target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }]), approvedOrigin: undefined };
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/not approved/i);
    expect(t.inserted).toEqual([]);
    expect((await store.readAudit())[0]).toMatchObject({ outcome: "denied", reason: "not approved" });
  });

  it("refuses when the page is not on the approved origin", async () => {
    const other = (await store.save({ origins: ["http://localhost:5000"], username: "a", password: "x-pass-1234", allowedProjects: [] })).id;
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: "http://localhost:5000/", focused: { tag: "INPUT", type: "password" } }]);
    await expect(filler.fillSecret(t, { itemId: other, field: "password" })).rejects.toThrow(/approved/i);
    expect(t.inserted).toEqual([]);
  });

  it("refuses a hidden or read-only focused field", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password", visible: false } }]);
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/hidden/i);
    expect(t.inserted).toEqual([]);
  });

  it("does not type after the command was cancelled", async () => {
    const controller = new AbortController();
    controller.abort();
    const filler = createCredentialFiller({ store });
    const t = { ...target([{ id: "top", url: `${ORIGIN}/login`, focused: { tag: "INPUT", type: "password" } }]), signal: controller.signal };
    await expect(filler.fillSecret(t, { itemId, field: "password" })).rejects.toThrow(/cancel/i);
    expect(t.inserted).toEqual([]);
    expect((await store.readAudit())[0]).toMatchObject({ outcome: "denied", reason: "cancelled" });
  });

  it("describes a fill with the label and username only", async () => {
    const filler = createCredentialFiller({ store });
    const t = { ...target([{ id: "top", url: `${ORIGIN}/login` }]), pageTitle: () => "Sign in" };
    const described = await filler.describe(t, { action: "login" });
    expect(described).toEqual({
      origin: ORIGIN,
      pageUrl: `${ORIGIN}/login`,
      pageTitle: "Sign in",
      item: { id: itemId, label: "Demo", username: "alice" },
    });
    expect(JSON.stringify(described)).not.toContain(PASSWORD);
    await expect(filler.describe(target([{ id: "top", url: "https://evil.test/" }]), { action: "fill-secret", itemId })).rejects.toThrow(/not saved for/);
    await expect(filler.describe({ ...target([{ id: "top", url: `${ORIGIN}/` }]), projectId: "other" }, { action: "login", itemId })).rejects.toThrow(/project/i);
  });

  it("login <origin> refuses a page that redirected elsewhere", async () => {
    const filler = createCredentialFiller({ store });
    const redirected = { ...target([{ id: "top", url: "https://sso.evil.test/login" }]), requestedOrigin: "http://localhost:4173" };
    await expect(filler.describe(redirected, { action: "login" })).rejects.toThrow(/redirected/);
    await expect(filler.login({ ...redirected, approvedOrigin: ORIGIN }, {})).rejects.toThrow();
    const same = { ...target([{ id: "top", url: `${ORIGIN}/login` }]), requestedOrigin: "http://localhost:4173/login" };
    await expect(filler.describe(same, { action: "login" })).resolves.toMatchObject({ origin: ORIGIN });
    const wrongScheme = { ...target([{ id: "top", url: `${ORIGIN}/login` }]), requestedOrigin: "localhost:4173" };
    await expect(filler.describe(wrongScheme, { action: "login" })).rejects.toThrow(/not https:\/\/localhost:4173/);
  });

  it("login fills username then password on the matching page", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([
      {
        id: "top",
        url: `${ORIGIN}/login`,
        fields: [
          { tag: "INPUT", type: "email", role: "username" },
          { tag: "INPUT", type: "password", role: "password" },
        ],
      },
    ]);
    const result = await filler.login(t, {});
    expect(t.inserted).toEqual(["alice", PASSWORD]);
    expect(result).toMatchObject({ filled: ["username", "password"], itemId, origin: ORIGIN });
    expect(JSON.stringify(result)).not.toContain(PASSWORD);
  });

  it("login fills a TOTP code on a second-factor page", async () => {
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/2fa`, fields: [{ tag: "INPUT", type: "text", role: "totp" }] }]);
    const result = await filler.login(t, {});
    expect(t.inserted).toEqual([expect.stringMatching(/^\d{6}$/)]);
    expect(result).toMatchObject({ filled: ["totp"] });
    expect(JSON.stringify(filler.scrub("s1", { text: t.inserted[0] }))).not.toContain(t.inserted[0]);
  });

  it("login lists candidates without secrets when several items match", async () => {
    await store.save({ origins: [ORIGIN], username: "bob", password: "other-pass-123", allowedProjects: [] });
    const filler = createCredentialFiller({ store });
    const t = target([{ id: "top", url: `${ORIGIN}/login`, fields: [] }]);
    const result = (await filler.login(t, {})) as { candidates: { username: string }[] };
    expect(result.candidates.map((c) => c.username).sort()).toEqual(["alice", "bob"]);
    expect(JSON.stringify(result)).not.toMatch(/Tr0ub4dor|other-pass/);
    expect(t.inserted).toEqual([]);
  });

  it("login reports when no saved credential matches the page", async () => {
    const filler = createCredentialFiller({ store });
    await expect(filler.login(target([{ id: "top", url: "https://nowhere.test/" }]), {})).rejects.toThrow(/no saved credential/i);
  });
});
