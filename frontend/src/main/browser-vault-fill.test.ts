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
        if (expression.includes("ao-vault:probe")) {
          return { result: { value: frame.focused ? { tag: frame.focused.tag, type: frame.focused.type, visible: frame.focused.visible ?? true } : null } };
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
    return { ...fake, sessionId: "s1", projectId: "proj", pageURL: () => pageURL, approvedOrigin: ORIGIN };
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
