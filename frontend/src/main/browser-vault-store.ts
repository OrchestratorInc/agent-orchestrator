// Local credential vault persisted under <AO data dir>/vault.
//
// key.bin     a random 256-bit data key, wrapped by the OS keychain
//             (Electron safeStorage). Wrapping the same data key to another
//             machine's host key is the seam for future vault sync.
// items.sealed every item, AES-256-GCM under the data key.
// audit.jsonl one line per fill attempt; never holds a secret.
import { AoError } from "../../../packages/shared/telemetry/ao-error";
import { randomBytes, randomUUID } from "node:crypto";
import { appendFile, chmod, mkdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { normalizeVaultOrigin, openSealed, originAllowed, seal, secureCredentialOrigin, totpCode } from "./browser-vault";

export type VaultKeyProtector = {
  available(): boolean;
  encrypt(data: Buffer): Buffer;
  decrypt(data: Buffer): Buffer;
};

export type VaultField = "username" | "password" | "totp";

export type VaultItemInput = {
  id?: string;
  label?: string;
  origins: string[];
  username: string;
  /** Blank on edit keeps the stored password. */
  password: string;
  /** Blank on edit keeps the stored seed; null clears it. */
  totpSeed?: string | null;
  /** Empty means every project may use the item. */
  allowedProjects: string[];
};

export type VaultItemSummary = {
  id: string;
  label: string;
  origins: string[];
  username: string;
  hasPassword: boolean;
  hasTotp: boolean;
  allowedProjects: string[];
  createdAt: string;
  updatedAt: string;
};

export type VaultAuditEntry = {
  time: string;
  origin: string;
  sessionId: string;
  itemId: string;
  itemLabel?: string;
  field: VaultField | "login";
  outcome: "filled" | "denied" | "error";
  reason?: string;
};

type StoredItem = Omit<VaultItemSummary, "hasPassword" | "hasTotp"> & {
  password: string;
  totpSeed?: string;
};

type Contents = { items: StoredItem[] };

const AUDIT_READ_LIMIT = 500;

/**
 * The saved items exist but cannot be opened: the key file is gone, or the OS
 * keychain now hands back a different key (a keychain reset). The items are
 * left untouched; the user can reset the vault to start again.
 */
export class VaultKeyLostError extends Error {
  readonly code = "VAULT_KEY_LOST";
  constructor() {
    super("Saved passwords can't be opened: the system keychain can't unlock their key. Unlock the keychain if it is locked; reset saved passwords only if the key is gone.");
  }
}

export class VaultStore {
  private readonly dir: string;
  private readonly protector: VaultKeyProtector;
  private readonly now: () => number;
  private key?: Buffer;
  private queue: Promise<unknown> = Promise.resolve();

  constructor(options: { dir: string; protector: VaultKeyProtector; now?: () => number }) {
    this.dir = options.dir;
    this.protector = options.protector;
    this.now = options.now ?? Date.now;
  }

  async list(): Promise<VaultItemSummary[]> {
    return (await this.read()).items.map(summarize);
  }

  save(input: VaultItemInput): Promise<VaultItemSummary> {
    return this.mutate((contents) => {
      const origins = [...new Set(input.origins.map(normalizeVaultOrigin))];
      if (!origins.length) throw new AoError("VAULT_NO_SITE", "Add at least one site");
      const insecure = origins.find((origin) => !secureCredentialOrigin(origin));
      if (insecure) {
        throw new AoError("VAULT_INSECURE_SITE", `${insecure} uses plain HTTP. Saved passwords only fill over HTTPS, or http:// on this computer.`);
      }
      const time = new Date(this.now()).toISOString();
      const existing = input.id ? contents.items.find((item) => item.id === input.id) : undefined;
      if (input.id && !existing) throw new AoError("VAULT_ITEM_NOT_FOUND", "Credential not found");
      const password = input.password || existing?.password || "";
      if (!password) throw new AoError("VAULT_PASSWORD_REQUIRED", "Password is required");
      const seed =
        input.totpSeed === null
          ? undefined
          : input.totpSeed?.trim()
            ? input.totpSeed.replace(/\s/g, "").toUpperCase()
            : existing?.totpSeed;
      if (seed) totpCode(seed); // validates the seed before storing it
      const item: StoredItem = {
        id: existing?.id ?? randomUUID(),
        label: input.label?.trim() || new URL(origins[0]).host,
        origins,
        username: input.username,
        password,
        ...(seed ? { totpSeed: seed } : {}),
        allowedProjects: [...new Set(input.allowedProjects.map((p) => p.trim()).filter(Boolean))],
        createdAt: existing?.createdAt ?? time,
        updatedAt: time,
      };
      if (existing) contents.items[contents.items.indexOf(existing)] = item;
      else contents.items.push(item);
      return summarize(item);
    });
  }

  delete(id: string): Promise<void> {
    return this.mutate((contents) => {
      contents.items = contents.items.filter((item) => item.id !== id);
    });
  }

  /** Items the page origin and project may use, without secrets. */
  async candidates(pageURL: string, projectId: string): Promise<VaultItemSummary[]> {
    return (await this.read()).items
      .filter((item) => originAllowed(item.origins, pageURL) && projectAllowed(item, projectId))
      .map(summarize);
  }

  /**
   * Returns the plaintext for one field. Callers must type it straight into the
   * page and never return, log or persist it.
   */
  async resolveSecret(request: {
    itemId: string;
    field: VaultField;
    pageURL: string;
    projectId: string;
  }): Promise<string> {
    const item = (await this.read()).items.find((candidate) => candidate.id === request.itemId);
    if (!item) throw new AoError("VAULT_ITEM_NOT_FOUND", "Credential not found");
    if (!originAllowed(item.origins, request.pageURL)) {
      throw new AoError("VAULT_ORIGIN_MISMATCH", "Page origin does not match this credential");
    }
    if (!projectAllowed(item, request.projectId)) {
      throw new AoError("VAULT_PROJECT_NOT_ALLOWED", "This project may not use this credential");
    }
    if (request.field === "username") return item.username;
    if (request.field === "password") return item.password;
    if (!item.totpSeed) throw new AoError("VAULT_NO_TOTP", "This credential has no TOTP seed");
    return totpCode(item.totpSeed, this.now());
  }

  /** Deletes every saved item and the key, for a vault whose key was lost. */
  reset(): Promise<void> {
    const run = this.queue.then(async () => {
      await rm(path.join(this.dir, "items.sealed"), { force: true });
      await rm(path.join(this.dir, "key.bin"), { force: true });
      this.key = undefined;
    });
    this.queue = run.catch(() => undefined);
    return run;
  }

  async audit(entry: Omit<VaultAuditEntry, "time" | "itemLabel">): Promise<void> {
    let itemLabel: string | undefined;
    try {
      itemLabel = (await this.read()).items.find((item) => item.id === entry.itemId)?.label;
    } catch {
      itemLabel = undefined;
    }
    const line: VaultAuditEntry = {
      time: new Date(this.now()).toISOString(),
      ...entry,
      ...(itemLabel ? { itemLabel } : {}),
    };
    await this.ensureDir();
    const file = path.join(this.dir, "audit.jsonl");
    await appendFile(file, `${JSON.stringify(line)}\n`, { mode: 0o600 });
  }

  async readAudit(limit = AUDIT_READ_LIMIT): Promise<VaultAuditEntry[]> {
    let raw: string;
    try {
      raw = await readFile(path.join(this.dir, "audit.jsonl"), "utf8");
    } catch {
      return [];
    }
    const entries: VaultAuditEntry[] = [];
    for (const line of raw.split("\n")) {
      if (!line.trim()) continue;
      try {
        entries.push(JSON.parse(line) as VaultAuditEntry);
      } catch {
        // Skip a torn final line from a crash mid-append.
      }
    }
    return entries.reverse().slice(0, limit);
  }

  private async ensureDir(): Promise<void> {
    await mkdir(this.dir, { recursive: true, mode: 0o700 });
    if (process.platform !== "win32") await chmod(this.dir, 0o700);
  }

  private async dataKey(create: boolean): Promise<Buffer | undefined> {
    if (this.key) return this.key;
    if (!this.protector.available()) {
      throw new AoError("VAULT_KEYCHAIN_UNAVAILABLE", "The OS keychain is unavailable, so credentials cannot be stored safely");
    }
    const file = path.join(this.dir, "key.bin");
    let wrapped: Buffer | undefined;
    try {
      wrapped = await readFile(file);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    if (wrapped) {
      try {
        this.key = this.protector.decrypt(wrapped);
        return this.key;
      } catch {
        // The keychain cannot unwrap it: reset, or just locked. Report a lost
        // key and leave everything in place; only the user's reset clears it.
        throw new VaultKeyLostError();
      }
    }
    if (!create) return undefined;
    // Never start a new key over items sealed with the old one: they would be
    // unreadable forever. That is a lost key, and only reset() clears it.
    if (await exists(path.join(this.dir, "items.sealed"))) throw new VaultKeyLostError();
    const key = randomBytes(32);
    await this.ensureDir();
    await atomicWrite(file, this.protector.encrypt(key));
    this.key = key;
    return key;
  }

  private async read(): Promise<Contents> {
    let sealed: string;
    try {
      sealed = await readFile(path.join(this.dir, "items.sealed"), "utf8");
    } catch (error) {
      // Only a missing file is an empty vault. Any other read error must stop
      // a save, or it would overwrite every saved item with just the new one.
      if ((error as NodeJS.ErrnoException).code === "ENOENT") return { items: [] };
      throw error;
    }
    const key = await this.dataKey(false);
    if (!key) throw new VaultKeyLostError();
    try {
      return openSealed<Contents>(key, sealed);
    } catch {
      // A different key (keychain reset) fails authentication.
      throw new VaultKeyLostError();
    }
  }

  private mutate<T>(change: (contents: Contents) => T): Promise<T> {
    const run = this.queue.then(async () => {
      const key = await this.dataKey(true);
      const contents = await this.read();
      const result = change(contents);
      await atomicWrite(path.join(this.dir, "items.sealed"), seal(key!, contents));
      return result;
    });
    this.queue = run.catch(() => undefined);
    return run;
  }
}

function summarize(item: StoredItem): VaultItemSummary {
  return {
    id: item.id,
    label: item.label,
    origins: item.origins,
    username: item.username,
    hasPassword: Boolean(item.password),
    hasTotp: Boolean(item.totpSeed),
    allowedProjects: item.allowedProjects,
    createdAt: item.createdAt,
    updatedAt: item.updatedAt,
  };
}

function projectAllowed(item: StoredItem, projectId: string): boolean {
  return item.allowedProjects.length === 0 || item.allowedProjects.includes(projectId);
}

async function exists(file: string): Promise<boolean> {
  try {
    await stat(file);
    return true;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return false;
    throw error;
  }
}

async function atomicWrite(file: string, data: string | Buffer): Promise<void> {
  const tmp = `${file}.${process.pid}.tmp`;
  try {
    await writeFile(tmp, data, { mode: 0o600 });
    await rename(tmp, file);
  } catch (error) {
    await rm(tmp, { force: true });
    throw error;
  }
}
