// Settings UI access to the vault. Secrets are write-only: the renderer can
// save a password or TOTP seed but no channel ever returns one.
import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";
import type { VaultAuditEntry, VaultItemInput, VaultItemSummary, VaultStore } from "./browser-vault-store";

export type VaultListResult = { available: boolean; items: VaultItemSummary[] };

export function registerBrowserVaultIpc(options: {
  ipcMain: Pick<IpcMain, "handle" | "removeHandler">;
  shellWebContents: WebContents;
  store: VaultStore;
  keychainAvailable: () => boolean;
}): { dispose: () => void } {
  const channels: string[] = [];
  const trusted = (event: IpcMainInvokeEvent) => event.sender === options.shellWebContents;
  const handle = (channel: string, fn: (event: IpcMainInvokeEvent, input: unknown) => unknown) => {
    options.ipcMain.handle(channel, fn);
    channels.push(channel);
  };
  const requireTrusted = (event: IpcMainInvokeEvent) => {
    if (!trusted(event)) throw new Error("Vault access is not allowed from this page");
  };

  handle("vault:list", async (event): Promise<VaultListResult> => {
    if (!trusted(event) || !options.keychainAvailable()) return { available: false, items: [] };
    return { available: true, items: await options.store.list() };
  });
  handle("vault:save", async (event, input) => {
    requireTrusted(event);
    return options.store.save(parseItemInput(input));
  });
  handle("vault:delete", async (event, input) => {
    requireTrusted(event);
    const id = (input as { id?: unknown } | null)?.id;
    if (typeof id !== "string" || !id) throw new Error("Invalid credential id");
    await options.store.delete(id);
  });
  handle("vault:audit", async (event): Promise<VaultAuditEntry[]> => {
    if (!trusted(event)) return [];
    return options.store.readAudit();
  });

  return {
    dispose: () => {
      for (const channel of channels.splice(0)) options.ipcMain.removeHandler(channel);
    },
  };
}

function parseItemInput(input: unknown): VaultItemInput {
  const raw = (input ?? {}) as Record<string, unknown>;
  const strings = (value: unknown) => Array.isArray(value) && value.every((v) => typeof v === "string");
  if (
    !strings(raw.origins) ||
    typeof raw.username !== "string" ||
    typeof raw.password !== "string" ||
    !strings(raw.allowedProjects) ||
    (raw.id !== undefined && typeof raw.id !== "string") ||
    (raw.label !== undefined && typeof raw.label !== "string") ||
    (raw.totpSeed !== undefined && raw.totpSeed !== null && typeof raw.totpSeed !== "string")
  ) {
    throw new Error("Invalid credential");
  }
  return {
    ...(raw.id ? { id: raw.id as string } : {}),
    ...(raw.label !== undefined ? { label: raw.label as string } : {}),
    origins: raw.origins as string[],
    username: raw.username,
    password: raw.password,
    ...(raw.totpSeed !== undefined ? { totpSeed: raw.totpSeed as string | null } : {}),
    allowedProjects: raw.allowedProjects as string[],
  };
}
