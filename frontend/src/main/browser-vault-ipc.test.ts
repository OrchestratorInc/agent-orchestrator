import { mkdtemp } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { registerBrowserVaultIpc } from "./browser-vault-ipc";
import { VaultStore } from "./browser-vault-store";

type Handler = (event: { sender: unknown }, ...args: unknown[]) => unknown;

async function setupWithDir(dir: string) {
  return setup(true, dir);
}

async function setup(available = true, existingDir?: string) {
  const handlers = new Map<string, Handler>();
  const shell = { id: 1 };
  const store = new VaultStore({
    dir: existingDir ?? (await mkdtemp(path.join(os.tmpdir(), "ao-vault-ipc-"))),
    protector: { available: () => available, encrypt: (d) => Buffer.from(d), decrypt: (d) => Buffer.from(d) },
  });
  const ipc = registerBrowserVaultIpc({
    ipcMain: {
      handle: (channel: string, fn: Handler) => handlers.set(channel, fn),
      removeHandler: (channel: string) => handlers.delete(channel),
    } as never,
    shellWebContents: shell as never,
    store,
    keychainAvailable: () => available,
  });
  const call = (channel: string, arg?: unknown, sender: unknown = shell) => handlers.get(channel)!({ sender }, arg);
  return { call, handlers, ipc, store };
}

describe("vault IPC", () => {
  it("saves write-only: no channel ever returns the password or seed", async () => {
    const { call } = await setup();
    const saved = await call("vault:save", {
      label: "Demo",
      origins: ["localhost:4173"],
      username: "alice",
      password: "ipc-secret-pass",
      totpSeed: "JBSWY3DPEHPK3PXP",
      allowedProjects: [],
    });
    const listed = await call("vault:list");
    const audit = await call("vault:audit");
    expect(JSON.stringify([saved, listed, audit])).not.toMatch(/ipc-secret-pass|JBSWY3DP/);
    expect(listed).toMatchObject({ available: true, items: [{ username: "alice", hasPassword: true, hasTotp: true }] });
  });

  it("ignores senders other than the app shell", async () => {
    const { call } = await setup();
    await call("vault:save", { origins: ["a.test"], username: "u", password: "pw-123456", allowedProjects: [] });
    await expect(call("vault:list", undefined, { id: 2 })).resolves.toEqual({ available: false, items: [] });
    await expect(
      call("vault:save", { origins: ["b.test"], username: "u", password: "x", allowedProjects: [] }, { id: 2 }),
    ).rejects.toThrow(/not allowed/i);
  });

  it("rejects malformed input and returns readable errors", async () => {
    const { call } = await setup();
    await expect(call("vault:save", { origins: "nope" })).rejects.toThrow(/invalid/i);
    await expect(
      call("vault:save", { origins: ["ftp://x"], username: "u", password: "p", allowedProjects: [] }),
    ).rejects.toThrow(/http/i);
  });

  it("reports an unavailable keychain instead of storing", async () => {
    const { call } = await setup(false);
    await expect(call("vault:list")).resolves.toEqual({ available: false, items: [] });
  });

  it("reports a lost key instead of a crypto error, and resets on request", async () => {
    const { call, store } = await setup();
    await call("vault:save", { origins: ["https://a.test"], username: "u", password: "pw-123456", allowedProjects: [] });
    const { rm } = await import("node:fs/promises");
    await rm(path.join((store as unknown as { dir: string }).dir, "key.bin"));
    const fresh = await setupWithDir((store as unknown as { dir: string }).dir);
    await expect(fresh.call("vault:list")).resolves.toEqual({ available: true, items: [], keyLost: true });
    await expect(fresh.call("vault:reset", undefined, { id: 2 })).rejects.toThrow(/not allowed/i);
    await fresh.call("vault:reset");
    await expect(fresh.call("vault:list")).resolves.toEqual({ available: true, items: [] });
  });

  it("deletes items and removes handlers on dispose", async () => {
    const { call, handlers, ipc } = await setup();
    const saved = (await call("vault:save", { origins: ["a.test"], username: "u", password: "pw-123456", allowedProjects: [] })) as { id: string };
    await call("vault:delete", { id: saved.id });
    expect(await call("vault:list")).toMatchObject({ items: [] });
    ipc.dispose();
    expect(handlers.size).toBe(0);
  });
});
