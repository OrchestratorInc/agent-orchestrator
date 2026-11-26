import { mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import { VaultKeyLostError, VaultStore, type VaultKeyProtector } from "./browser-vault-store";

// Stands in for Electron safeStorage: reversible, but not plaintext.
const fakeProtector: VaultKeyProtector = {
  available: () => true,
  encrypt: (data) => Buffer.from(data.map((b) => b ^ 0x5a)),
  decrypt: (data) => Buffer.from(data.map((b) => b ^ 0x5a)),
};

const PASSWORD = "Correct-Horse-9!";
const SEED = "JBSWY3DPEHPK3PXP";

describe("VaultStore", () => {
  let dir: string;
  let store: VaultStore;
  beforeEach(async () => {
    dir = await mkdtemp(path.join(os.tmpdir(), "ao-vault-"));
    store = new VaultStore({ dir, protector: fakeProtector, now: () => 1_700_000_000_000 });
  });

  const addDemo = () =>
    store.save({
      label: "Demo",
      origins: ["http://localhost:4173/login"],
      username: "alice",
      password: PASSWORD,
      totpSeed: SEED,
      allowedProjects: [],
    });

  it("encrypts everything at rest and never writes secrets in plaintext", async () => {
    await addDemo();
    for (const name of await readdir(dir)) {
      const body = await readFile(path.join(dir, name), "utf8");
      expect(body).not.toContain(PASSWORD);
      expect(body).not.toContain(SEED);
      expect(body).not.toContain("alice");
      if (process.platform !== "win32") {
        expect((await stat(path.join(dir, name))).mode & 0o077).toBe(0);
      }
    }
  });

  it("survives a restart with the same keychain key", async () => {
    const item = await addDemo();
    const reopened = new VaultStore({ dir, protector: fakeProtector });
    const [summary] = await reopened.list();
    expect(summary).toMatchObject({ id: item.id, username: "alice", hasPassword: true, hasTotp: true });
    expect(summary).not.toHaveProperty("password");
    expect(JSON.stringify(summary)).not.toContain(PASSWORD);
  });

  it("refuses to store anything when the keychain is unavailable", async () => {
    const locked = new VaultStore({ dir, protector: { ...fakeProtector, available: () => false } });
    await expect(
      locked.save({ origins: ["https://a.test"], username: "u", password: "p", allowedProjects: [] }),
    ).rejects.toThrow(/keychain/i);
    expect(await readdir(dir)).toEqual([]);
  });

  it("keeps the old password when an edit leaves it blank", async () => {
    const item = await addDemo();
    await store.save({ id: item.id, origins: item.origins, username: "bob", password: "", allowedProjects: [] });
    const secret = await store.resolveSecret({
      itemId: item.id,
      field: "password",
      pageURL: "http://localhost:4173/login",
      projectId: "p",
    });
    expect(secret).toBe(PASSWORD);
    expect((await store.list())[0].username).toBe("bob");
  });

  it("resolves secrets only on an exact origin and an allowed project", async () => {
    const item = await store.save({
      origins: ["https://app.example.com"],
      username: "alice",
      password: PASSWORD,
      totpSeed: SEED,
      allowedProjects: ["proj-a"],
    });
    const base = { itemId: item.id, field: "password" as const, projectId: "proj-a" };
    await expect(store.resolveSecret({ ...base, pageURL: "https://app.example.com/x" })).resolves.toBe(PASSWORD);
    await expect(store.resolveSecret({ ...base, pageURL: "https://example.com/" })).rejects.toThrow(/origin/i);
    await expect(store.resolveSecret({ ...base, pageURL: "http://app.example.com/" })).rejects.toThrow(/origin/i);
    await expect(
      store.resolveSecret({ ...base, projectId: "proj-b", pageURL: "https://app.example.com/" }),
    ).rejects.toThrow(/project/i);
    const code = await store.resolveSecret({ ...base, field: "totp", pageURL: "https://app.example.com/" });
    expect(code).toMatch(/^\d{6}$/);
    expect(await store.candidates("https://app.example.com/login", "proj-a")).toHaveLength(1);
    expect(await store.candidates("https://app.example.com/login", "proj-b")).toHaveLength(0);
    expect(await store.candidates("https://evil.example.com/", "proj-a")).toHaveLength(0);
  });

  it("gives standalone sessions (no project) only all-projects items", async () => {
    await store.save({ origins: ["https://a.test"], username: "any", password: "pw-any-123", allowedProjects: [] });
    await store.save({ origins: ["https://a.test"], username: "scoped", password: "pw-scoped-1", allowedProjects: ["proj-a"] });
    expect((await store.candidates("https://a.test/", "")).map((i) => i.username)).toEqual(["any"]);
  });

  it("does not overwrite saved items when the vault file cannot be read", async () => {
    await addDemo();
    const sealed = path.join(dir, "items.sealed");
    const kept = await readFile(sealed);
    // Stand-in for an I/O error: a directory where the file should be.
    await rm(sealed);
    await mkdir(sealed);
    await expect(store.save({ origins: ["https://b.test"], username: "b", password: "pw-bbbb-1", allowedProjects: [] })).rejects.toThrow();
    await expect(store.list()).rejects.toThrow();
    await rm(sealed, { recursive: true });
    await writeFile(sealed, kept);
    expect((await store.list()).map((i) => i.username)).toEqual(["alice"]);
  });

  it("refuses plain-HTTP sites other than this computer at save time", async () => {
    await expect(
      store.save({ origins: ["http://intranet.example.test"], username: "u", password: "pw-1234-x", allowedProjects: [] }),
    ).rejects.toThrow(/HTTPS/);
    for (const origin of ["http://localhost:3000", "http://127.0.0.1:8080", "http://[::1]:9000", "http://app.localhost"]) {
      await expect(store.save({ origins: [origin], username: "u", password: "pw-1234-x", allowedProjects: [] })).resolves.toMatchObject({ origins: [origin] });
    }
  });

  it("does not replace a lost key over existing items, and can be reset", async () => {
    await addDemo();
    const sealed = path.join(dir, "items.sealed");
    const kept = await readFile(sealed, "utf8");
    await rm(path.join(dir, "key.bin"));
    const reopened = new VaultStore({ dir, protector: fakeProtector });
    await expect(reopened.list()).rejects.toBeInstanceOf(VaultKeyLostError);
    await expect(
      reopened.save({ origins: ["https://b.test"], username: "b", password: "pw-bbbb-1", allowedProjects: [] }),
    ).rejects.toBeInstanceOf(VaultKeyLostError);
    // Nothing was overwritten, so a restored key would still open the items.
    expect(await readFile(sealed, "utf8")).toBe(kept);
    await reopened.reset();
    expect(await reopened.list()).toEqual([]);
    await expect(
      reopened.save({ origins: ["https://b.test"], username: "b", password: "pw-bbbb-1", allowedProjects: [] }),
    ).resolves.toMatchObject({ username: "b" });
  });

  it("reports a keychain that now hands back a different key as a lost key", async () => {
    await addDemo();
    const otherKeychain: VaultKeyProtector = { ...fakeProtector, decrypt: (data) => Buffer.from(data.map((b) => b ^ 0x33)) };
    const reopened = new VaultStore({ dir, protector: otherKeychain });
    await expect(reopened.list()).rejects.toBeInstanceOf(VaultKeyLostError);
  });

  it("deletes an item", async () => {
    const item = await addDemo();
    await store.delete(item.id);
    expect(await store.list()).toEqual([]);
  });

  it("appends an audit trail without secrets, newest first", async () => {
    const item = await addDemo();
    await store.audit({ origin: "http://localhost:4173", sessionId: "s1", itemId: item.id, field: "password", outcome: "filled" });
    await store.audit({ origin: "https://evil.test", sessionId: "s1", itemId: item.id, field: "password", outcome: "denied", reason: "origin mismatch" });
    const entries = await store.readAudit();
    expect(entries.map((e) => e.outcome)).toEqual(["denied", "filled"]);
    expect(entries[1]).toMatchObject({ itemLabel: "Demo", time: new Date(1_700_000_000_000).toISOString() });
    const raw = await readFile(path.join(dir, "audit.jsonl"), "utf8");
    expect(raw).not.toContain(PASSWORD);
  });
});
