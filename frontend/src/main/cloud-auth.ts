import { createWorkOS, type User } from "@workos-inc/node";
import { app, dialog, ipcMain, safeStorage, shell } from "electron";
import { createHash } from "node:crypto";
import {
  chmod,
  mkdir,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { createServer, type Server } from "node:http";
import path from "node:path";
import { isWebDistinctId, type CloudAccount, type CloudSignInContext } from "../shared/cloud-account";
// Static import (not dynamic): the dynamic import created a static+dynamic
// import cycle with cloud-auth-local that made the bundler split chunks
// incorrectly and panic. Both modules only use each other's exports inside
// functions, so the static ES cycle is safe at module-init time.
import { revokeLocalSession } from "./cloud-auth-local";
import { providerAuthFlow } from "./provider-auth-flow";

// persistLocalClaudeOAuthToken writes a captured Claude setup-token under the AO
// data dir so local claude sessions can authenticate with the SAME credential
// pushed to the cloud (read back by the daemon's claudecode adapter). Scoped to
// ~/.ao, never the user's ~/.claude; 0600 file in a 0700 dir. This is not the
// token chosen in AO's Claude login menu (harnesses/claude-code/setup-token):
// that one outranks a native login, this one never does.
async function persistLocalClaudeOAuthToken(dataDir: string, token: string): Promise<void> {
  const dir = path.join(dataDir, "harnesses", "claude-code");
  await mkdir(dir, { recursive: true, mode: 0o700 });
  const file = path.join(dir, "oauth-token");
  await writeFile(file, token, { mode: 0o600 });
  await chmod(file, 0o600);
}

// The WorkOS AuthKit client id is public configuration (it appears in every
// sign-in URL), so a baked default keeps sign-in working without build-time
// setup; VITE_WORKOS_CLIENT_ID overrides it per build when needed.
const DEFAULT_WORKOS_CLIENT_ID = "client_01KZ3VRKC374HS91XGRDPT3671";
const CLIENT_ID =
  import.meta.env.VITE_WORKOS_CLIENT_ID?.trim() ||
  (process.env.VITEST ? "client_test" : DEFAULT_WORKOS_CLIENT_ID);
// A loopback redirect is one we service with a local HTTP server: a dev build's
// http://127.0.0.1 default, or an explicit 127.0.0.1/localhost override. An
// HTTPS bounce page is NOT a loopback — it returns to the app via the ao-app://
// deep link, so no local server is armed for it.
function isLoopbackRedirect(redirectUri: string): boolean {
  try {
    const url = new URL(redirectUri);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      (url.hostname === "127.0.0.1" || url.hostname === "localhost")
    );
  } catch {
    return false;
  }
}

// The packaged app returns from WorkOS via an HTTPS bounce page on our own domain,
// which re-emits the result to the ao-app:// deep link the installed app handles.
// Routing through that page (rather than setting ao-app:// as the WorkOS
// redirect_uri directly) matters for two reasons: the browser lands on a real
// HTTPS page instead of hanging forever on an unloadable custom-scheme navigation
// (no stuck "loading" tab), and the OS "open Agent Orchestrator?" prompt is
// attributed to our own domain instead of the opaque WorkOS AuthKit subdomain.
//
// A build signing in with the production AuthKit client returns through the
// website's page, orchestrator.inc/app/auth/return/ (the trailing slash is part
// of the URL WorkOS has registered). Only the production WorkOS environment lists
// that page, so any other client, such as the baked default, which belongs to
// the staging environment, keeps the control plane's page at api.aoagents.dev.
//
// A development build doesn't claim ao-app:// by default (see
// registerCloudProtocol): owning it would take the scheme away from the
// installed app. It signs in through a loopback callback server instead, the
// standard desktop-OAuth pattern, with zero setup. AO_CLOUD_AUTH_REDIRECT
// overrides every default (e.g. to point a packaged build at a staging bounce
// page).
//
// The production AuthKit client (auth.orchestrator.inc). Release builds (stable,
// nightly and preview, all made by build-artifacts.yml) get it from the
// VITE_WORKOS_CLIENT_ID repository variable.
const PRODUCTION_WORKOS_CLIENT_ID = "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X";
const DEFAULT_PACKAGED_REDIRECT_URI = "https://orchestrator.inc/app/auth/return/";
const CONTROL_PLANE_PACKAGED_REDIRECT_URI = "https://api.aoagents.dev/app/auth/return";
const LOOPBACK_REDIRECT_URI = "http://127.0.0.1:3000/callback";

/** The redirect_uri a build sends to WorkOS when it starts a sign-in. */
export function resolveCloudAuthRedirect(
  env: Record<string, string | undefined>,
  isPackaged: boolean,
  clientId: string,
): string {
  const override = env.AO_CLOUD_AUTH_REDIRECT?.trim();
  if (override) return override;
  if (!isPackaged) return LOOPBACK_REDIRECT_URI;
  return clientId === PRODUCTION_WORKOS_CLIENT_ID
    ? DEFAULT_PACKAGED_REDIRECT_URI
    : CONTROL_PLANE_PACKAGED_REDIRECT_URI;
}

const REDIRECT_URI = resolveCloudAuthRedirect(process.env, app.isPackaged, CLIENT_ID);
const useLoopbackRedirect = isLoopbackRedirect(REDIRECT_URI);
const AUTH_STORE_FILE = "cloud-auth.bin";
const LEGACY_SESSION_FILE = "cloud-session.json";
const PKCE_TTL_MS = 10 * 60 * 1000;
const workos = CLIENT_ID ? createWorkOS({ clientId: CLIENT_ID }) : null;

// Set by installCloudIPC so the loopback callback server can push the new
// session to the renderer exactly like the deep-link path does.
let notifyRenderersFn: ((session: CloudAccount | null) => void) | null = null;
// At most one loopback callback server is armed at a time.
let loopbackServer: Server | null = null;

let activeProviderAuthAbort: AbortController | null = null;

export interface StoredSession extends CloudAccount {
  accessToken: string;
  // WorkOS sessions carry a rotating refresh token; local (opaque-token)
  // sessions have none, so it is optional and absent for authProvider "local".
  refreshToken?: string;
  // Loopback control-plane base URL a local session was minted against, so
  // sign-out can revoke the opaque token server-side. Local sessions only.
  cpBaseUrl?: string;
}

export interface AuthStore {
  session: StoredSession | null;
  // Digest of the verified callback, retained with its session so a browser
  // return button can be clicked again without replaying a single-use code.
  completedCallback?: string;
  pkce: {
    codeVerifier: string;
    state: string;
    expiresAt: number;
  } | null;
}

/** A stored local (opaque-token) session, distinct from the WorkOS/JWT path. */
export function isLocalSession(
  session: StoredSession | null | undefined,
): session is StoredSession & { authProvider: "local" } {
  return session?.authProvider === "local";
}

const emptyStore = (): AuthStore => ({ session: null, pkce: null });

// A store written while protected storage was unavailable, kept in this process
// until it can be saved. One holding a credential (a sign-in or a rotated token)
// replaces the file, which was deleted then. One without only adds a pending
// sign-in on top of the file, which stays for when storage is back.
interface MemoryStore {
  store: AuthStore;
  replacesFile: boolean;
}
const memoryStores = new Map<string, MemoryStore>();
// Data dirs whose file could not be read during an outage, so the signed-out
// store handed out then is not a sign-out.
const unreadFiles = new Set<string>();
// Data dirs whose file did not decrypt this run: the keychain was locked, its
// access prompt was denied, or the key is not the one the file was saved
// with. The file is kept for the next launch and not decrypted again in this
// one, so a denied prompt does not come back on every read.
const undecryptable = new Set<string>();
const storeOps = new Map<string, Promise<void>>();
const refreshes = new Map<string, Promise<CloudAccount | null>>();
const authGenerations = new Map<string, number>();
const authMutations = new Map<string, Promise<void>>();

// Runs one operation per data dir at a time, in call order.
async function serialize<T>(
  queue: Map<string, Promise<void>>,
  dataDir: string,
  operation: () => Promise<T>,
): Promise<T> {
  const previous = queue.get(dataDir) ?? Promise.resolve();
  const result = previous.catch(() => undefined).then(operation);
  const settled = result.then(
    () => undefined,
    () => undefined,
  );
  queue.set(dataDir, settled);
  try {
    return await result;
  } finally {
    if (queue.get(dataDir) === settled) queue.delete(dataDir);
  }
}

function authGeneration(dataDir: string): number {
  return authGenerations.get(dataDir) ?? 0;
}

export function invalidateAuthOperations(dataDir: string): number {
  const generation = authGeneration(dataDir) + 1;
  authGenerations.set(dataDir, generation);
  return generation;
}

export function withAuthMutation<T>(
  dataDir: string,
  mutation: () => Promise<T>,
): Promise<T> {
  return serialize(authMutations, dataDir, mutation);
}

/**
 * Where the Cloud sign-in is kept. An AO_DATA_DIR override gives that data
 * dir its own sign-in, as it gives it its own daemon data, so two instances
 * with different data dirs never share an account. Otherwise ~/.ao, or
 * ~/.ao/dev for a development build.
 */
export function resolveCloudAuthDir(
  env: Record<string, string | undefined>,
  homeDir: string,
  launchWorkingDirectory: string,
  isPackaged: boolean,
): string {
  const configured = env.AO_DATA_DIR?.trim();
  if (configured) return path.resolve(launchWorkingDirectory, configured);
  return isPackaged ? path.join(homeDir, ".ao") : path.join(homeDir, ".ao", "dev");
}

function storePath(dataDir: string): string {
  return path.join(dataDir, AUTH_STORE_FILE);
}

/** Whether safeStorage protects what it encrypts (not Linux's basic_text backend). */
export function protectedStorageAvailable(): boolean {
  if (!safeStorage.isEncryptionAvailable()) return false;
  if (process.platform !== "linux") return true;
  const backend = safeStorage.getSelectedStorageBackend();
  return backend !== "basic_text" && backend !== "unknown";
}

function encodeStore(store: AuthStore): Buffer {
  return safeStorage.encryptString(JSON.stringify(store));
}

/**
 * The saved login exists but could not be read just now (too many open files,
 * a busy disk, no permission). It is not a sign-out: the file is left as it
 * is, and the next read can find the login again. Carries only the error code,
 * never the path or the file's contents.
 */
export class AuthStoreUnreadableError extends Error {
  readonly code: string;

  constructor(code: string) {
    super(`The saved Orchestrator Cloud login could not be read (${code}).`);
    this.name = "AuthStoreUnreadableError";
    this.code = code;
  }
}

// Read errors that usually clear within moments, so the read tries again
// before giving up: the process is out of file handles (EMFILE, ENFILE), or
// the file is briefly locked (EAGAIN, EBUSY).
const TRANSIENT_READ_CODES = new Set(["EMFILE", "ENFILE", "EAGAIN", "EBUSY"]);
const TRANSIENT_READ_RETRY_MS = [25, 100, 250];

function errorCode(error: unknown): string {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" && code !== "" ? code : "UNKNOWN";
}

// The encrypted store file's bytes, or null when there is no file. Any other
// failure throws AuthStoreUnreadableError and leaves the file in place.
async function readStoreBytes(file: string): Promise<Buffer | null> {
  for (let attempt = 0; ; attempt += 1) {
    try {
      return await readFile(file);
    } catch (error) {
      const code = errorCode(error);
      if (code === "ENOENT") return null;
      const retryMs = TRANSIENT_READ_CODES.has(code) ? TRANSIENT_READ_RETRY_MS[attempt] : undefined;
      if (retryMs === undefined) throw new AuthStoreUnreadableError(code);
      await new Promise((resolve) => setTimeout(resolve, retryMs));
    }
  }
}

// The file's store, or null when the file is there but did not decrypt. No
// file is the only read that means "no saved login". A read that failed
// throws AuthStoreUnreadableError instead, so nothing downstream takes it for
// a sign-out or writes an empty store over the login.
async function readStoreFile(dataDir: string): Promise<AuthStore | null> {
  const value = await readStoreBytes(storePath(dataDir));
  if (value === null) return emptyStore();
  let text: string;
  try {
    text = safeStorage.decryptString(value);
  } catch {
    return null;
  }
  try {
    const store: unknown = JSON.parse(text);
    if (!store || typeof store !== "object") throw new Error("Not an auth store.");
    return store as AuthStore;
  } catch {
    // The file decrypts but holds no store this app can use, so no later
    // read can recover a login from it.
    await rm(storePath(dataDir), { force: true });
    return emptyStore();
  }
}

// The file did not decrypt. Keep it, and read as signed out until a restart
// tries again or a new sign-in is saved over it.
function noteUndecryptable(dataDir: string): void {
  if (!undecryptable.has(dataDir)) {
    console.warn(
      "Orchestrator Cloud sign-in could not be decrypted. It is kept, and the next launch tries again.",
    );
  }
  undecryptable.add(dataDir);
  unreadFiles.add(dataDir);
}

// Writes a temporary file and renames it over the store, so a write that fails
// partway leaves the saved sign-in as it was.
async function writeStoreFile(dataDir: string, store: AuthStore): Promise<void> {
  await mkdir(dataDir, { recursive: true });
  const target = storePath(dataDir);
  const staging = `${target}.${process.pid}.tmp`;
  try {
    await writeFile(staging, encodeStore(store), { mode: 0o600 });
    await chmod(staging, 0o600);
    await rename(staging, target);
  } catch (error) {
    await rm(staging, { force: true });
    throw error;
  }
}

// A pending sign-in written during an outage goes onto the session the file
// still holds, so a sign-in that was started but never finished keeps it.
// Null when the file did not decrypt, so there is nothing to merge onto.
async function onFileSession(dataDir: string, store: AuthStore): Promise<AuthStore | null> {
  const file = await readStoreFile(dataDir);
  if (file === null) {
    noteUndecryptable(dataDir);
    return null;
  }
  return { ...file, pkce: store.pkce };
}

// The file was read for the first time since an outage hid it: show its
// account again in every renderer.
function noteFileRead(dataDir: string, store: AuthStore): void {
  if (unreadFiles.delete(dataDir) && store.session) {
    notifyRenderersFn?.(publicAccount(store.session));
  }
}

/**
 * Whether the saved sign-in is hidden right now (secure storage is out, or
 * the file did not decrypt), so the signed-out store read from it is not a
 * sign-out. Callers that would clear account data on a signed-out read wait.
 */
export function isSavedSignInHidden(dataDir: string): boolean {
  return unreadFiles.has(dataDir);
}

/**
 * Reads the store again when a sign-in is waiting on secure storage and it is
 * available now. Nothing announces that storage is back, so the app calls
 * this when it is focused or the screen is unlocked: the read saves a sign-in
 * kept in memory and shows a hidden one again in every renderer.
 */
export function recheckSavedSignIn(dataDir: string): void {
  if (!unreadFiles.has(dataDir) && !memoryStores.has(dataDir)) return;
  if (!protectedStorageAvailable() || undecryptable.has(dataDir)) return;
  void readAuthStore(dataDir).catch(() => undefined);
}

export function readAuthStore(dataDir: string): Promise<AuthStore> {
  return serialize(storeOps, dataDir, async () => {
    const memory = memoryStores.get(dataDir);
    if (!protectedStorageAvailable() || undecryptable.has(dataDir)) {
      // An outage, such as a locked keychain or a keyring that has not started
      // yet, is often temporary. The file stays for when storage is back and
      // reads as signed out until then, unless this run signed in since.
      if (!memory?.replacesFile) unreadFiles.add(dataDir);
      return memory?.store ?? emptyStore();
    }
    if (memory) {
      // Storage is back: save what was kept in memory.
      const store = memory.replacesFile
        ? memory.store
        : await onFileSession(dataDir, memory.store);
      if (store === null) return memory.store;
      try {
        await writeStoreFile(dataDir, store);
      } catch {
        return store;
      }
      memoryStores.delete(dataDir);
      noteFileRead(dataDir, store);
      return store;
    }
    const store = await readStoreFile(dataDir);
    if (store === null) {
      noteUndecryptable(dataDir);
      return emptyStore();
    }
    noteFileRead(dataDir, store);
    return store;
  });
}

export function writeAuthStore(
  dataDir: string,
  store: AuthStore,
): Promise<void> {
  return serialize(storeOps, dataDir, async () => {
    if (!protectedStorageAvailable()) {
      // A rotating refresh token must never fall back to plaintext persistence.
      // Linux's basic_text backend also reports encryption as available despite
      // using an unprotected hardcoded password, so keep that process-local too.
      const wasReplacing = memoryStores.get(dataDir)?.replacesFile === true;
      memoryStores.set(dataDir, { store, replacesFile: wasReplacing || store.session !== null });
      if (store.session !== null) {
        // This credential is newer than the file's. Delete the file so the
        // account it replaced can never come back.
        unreadFiles.delete(dataDir);
        undecryptable.delete(dataDir);
        await rm(storePath(dataDir), { force: true });
        if (!wasReplacing) {
          console.warn(
            "Orchestrator Cloud sign-in is kept in memory only: secure storage is unavailable, so it will not survive a restart until storage is available again.",
          );
        }
      }
      return;
    }
    // What is kept in memory is dropped only once the file is saved, so a
    // failed write cannot lose a sign-in held nowhere else.
    if (store.session === null && unreadFiles.has(dataDir)) {
      // Read during the outage, this store never saw the file's session.
      const next = undecryptable.has(dataDir) ? null : await onFileSession(dataDir, store);
      if (next === null) {
        // The file did not decrypt, so its session cannot be kept in a new
        // one. Hold this store (a pending sign-in) in memory and leave the
        // file for the next launch; a sign-in it completes is saved over it.
        memoryStores.set(dataDir, { store, replacesFile: false });
        return;
      }
      await writeStoreFile(dataDir, next);
      memoryStores.delete(dataDir);
      noteFileRead(dataDir, next);
      return;
    }
    await writeStoreFile(dataDir, store);
    memoryStores.delete(dataDir);
    unreadFiles.delete(dataDir);
    undecryptable.delete(dataDir);
  });
}

export function removeAuthStore(dataDir: string): Promise<void> {
  return serialize(storeOps, dataDir, async () => {
    memoryStores.delete(dataDir);
    unreadFiles.delete(dataDir);
    undecryptable.delete(dataDir);
    await Promise.all([
      rm(storePath(dataDir), { force: true }),
      rm(path.join(dataDir, LEGACY_SESSION_FILE), { force: true }),
    ]);
  });
}

function displayName(user: User): string {
  return (
    user.name?.trim() ||
    [user.firstName, user.lastName].filter(Boolean).join(" ") ||
    user.email
  );
}

function toStoredSession(
  accessToken: string,
  refreshToken: string,
  user: User,
): StoredSession {
  return {
    accessToken,
    refreshToken,
    authProvider: "workos",
    user: {
      id: user.id,
      email: user.email,
      displayName: displayName(user),
      ...(user.profilePictureUrl ? { avatarUrl: user.profilePictureUrl } : {}),
    },
    storedAt: new Date().toISOString(),
  };
}

export function publicAccount(session: StoredSession): CloudAccount {
  return {
    authProvider: session.authProvider,
    user: session.user,
    ...(session.organizations ? { organizations: session.organizations } : {}),
    storedAt: session.storedAt,
  };
}

function jwtPayload(token: string): Record<string, unknown> | null {
  try {
    const payload = token.split(".")[1];
    if (!payload) return null;
    return JSON.parse(
      Buffer.from(payload.replace(/-/g, "+").replace(/_/g, "/"), "base64").toString("utf8"),
    ) as Record<string, unknown>;
  } catch {
    return null;
  }
}

function tokenExpiresSoon(token: string): boolean {
  const exp = jwtPayload(token)?.exp;
  return typeof exp !== "number" || Date.now() >= exp * 1000 - 60_000;
}

function isTerminalRefreshFailure(error: unknown): boolean {
  if (!error || typeof error !== "object") {
    return String(error).toLowerCase().includes("refresh token already consumed");
  }
  const candidate = error as {
    code?: unknown;
    error?: unknown;
    message?: unknown;
    rawData?: { code?: unknown; error?: unknown; message?: unknown };
  };
  const details = [
    candidate.code,
    candidate.error,
    candidate.message,
    candidate.rawData?.code,
    candidate.rawData?.error,
    candidate.rawData?.message,
  ]
    .filter((value): value is string => typeof value === "string")
    .join(" ")
    .toLowerCase();
  return (
    details.includes("invalid_grant") ||
    details.includes("refresh token already consumed") ||
    details.includes("refresh token expired") ||
    details.includes("refresh token revoked")
  );
}

export async function getCloudSession(
  dataDir: string,
): Promise<CloudAccount | null> {
  const store = await readAuthStore(dataDir);
  // Local (opaque-token) sessions never refresh and do not require WorkOS to be
  // configured; return the stored identity directly.
  if (isLocalSession(store.session)) return publicAccount(store.session);
  if (!workos) return null;
  const activeRefresh = refreshes.get(dataDir);
  if (activeRefresh) return activeRefresh;
  if (!store.session) return null;
  if (!tokenExpiresSoon(store.session.accessToken)) {
    return publicAccount(store.session);
  }

  const pendingRefresh = refreshes.get(dataDir);
  if (pendingRefresh) return pendingRefresh;
  const refresh = refreshCloudSession(
    dataDir,
    store.session,
    authGeneration(dataDir),
  );
  refreshes.set(dataDir, refresh);
  try {
    return await refresh;
  } finally {
    if (refreshes.get(dataDir) === refresh) refreshes.delete(dataDir);
  }
}

/**
 * Fast, non-blocking variant of getCloudSession for the renderer's initial
 * mount. Returns the on-disk identity immediately (no WorkOS round-trip), so
 * the sidebar and the create-project Cloud tab render right away instead of
 * blanking for seconds while a token refresh runs on cold open. If the stored
 * token is stale, the refresh runs in the background and the corrected result
 * (rotated session, or null on terminal failure) is pushed over
 * cloud:sessionChanged, which the renderer already subscribes to. The CP proxy
 * refreshes independently via getCloudAccessToken, so outbound requests stay
 * authorized regardless.
 */
export async function getCloudSessionCached(
  dataDir: string,
): Promise<CloudAccount | null> {
  const store = await readAuthStore(dataDir);
  // Local sessions carry no rotating token — return the stored identity as-is.
  if (isLocalSession(store.session)) return publicAccount(store.session);
  if (!workos) return null;
  if (!store.session) return null;
  if (tokenExpiresSoon(store.session.accessToken)) {
    void getCloudSession(dataDir)
      .then((refreshed) => notifyRenderersFn?.(refreshed))
      .catch(() => {});
  }
  return publicAccount(store.session);
}

/**
 * Raw bearer token for the main-process control-plane proxy
 * (cloud-cp-proxy.ts). Runs the same refresh-if-expiring path as
 * getCloudSession, then reads the (possibly rotated) token back out of the
 * store. Main-process only: the token must never be sent to a renderer or
 * exposed over IPC.
 */
export async function getCloudAccessToken(
  dataDir: string,
): Promise<string | null> {
  const store = await readAuthStore(dataDir);
  // The local opaque token is returned verbatim (no JWT refresh path).
  if (isLocalSession(store.session)) return store.session.accessToken;
  const account = await getCloudSession(dataDir);
  if (account === null) {
    // The auth store may also have been cleared by another dev-app process.
    // A proxied request is authoritative evidence that this renderer can no
    // longer use AO Cloud, so retire any cached signed-in UI. A sign-in hidden
    // by a secure storage outage is not gone: a signed-out notice would clear
    // the account's share links and previews, which do not come back with it.
    if (!isSavedSignInHidden(dataDir)) notifyRenderersFn?.(null);
    return null;
  }
  const refreshed = await readAuthStore(dataDir);
  return refreshed.session?.accessToken ?? null;
}

async function refreshCloudSession(
  dataDir: string,
  storedSession: StoredSession,
  generation: number,
): Promise<CloudAccount | null> {
  // Only WorkOS sessions carry a refresh token and reach this path; a session
  // without one cannot be rotated, so surface it unchanged.
  if (!storedSession.refreshToken) return publicAccount(storedSession);
  try {
    const refreshed = await workos!.userManagement.authenticateWithRefreshToken({
      clientId: CLIENT_ID,
      refreshToken: storedSession.refreshToken,
    });
    const session = toStoredSession(
      refreshed.accessToken,
      refreshed.refreshToken,
      refreshed.user,
    );
    return withAuthMutation(dataDir, async () => {
      if (authGeneration(dataDir) !== generation) return null;
      let currentStore: AuthStore;
      try {
        currentStore = await readAuthStore(dataDir);
      } catch (error) {
        if (!(error instanceof AuthStoreUnreadableError)) throw error;
        // WorkOS has already used up the old refresh token, so the rotated
        // one is the only login that still works. Save it without the file's
        // other contents: at worst a pending sign-in is dropped.
        await writeAuthStore(dataDir, { session, pkce: null });
        return publicAccount(session);
      }
      if (
        currentStore.session?.refreshToken !== storedSession.refreshToken
      ) {
        return null;
      }
      await writeAuthStore(dataDir, { ...currentStore, session });
      return publicAccount(session);
    });
  } catch (error) {
    if (!isTerminalRefreshFailure(error)) {
      return publicAccount(storedSession);
    }
    const sessionCleared = await withAuthMutation(dataDir, async () => {
      if (authGeneration(dataDir) !== generation) return false;
      const currentStore = await readAuthStore(dataDir);
      if (
        currentStore.session?.refreshToken === storedSession.refreshToken
      ) {
        await removeAuthStore(dataDir);
        return true;
      }
      return false;
    });
    // Keep every renderer in lockstep with the credential store. Without this
    // notification, a terminal refresh failure leaves the last public account
    // (and its cached cloud projects/sessions) visible even though all future
    // control-plane requests are unauthenticated.
    if (sessionCleared) notifyRenderersFn?.(null);
    return null;
  }
}

export async function beginCloudSignIn(dataDir: string): Promise<void> {
  if (!workos) {
    throw new Error("WorkOS is not configured.");
  }
  const { url, state, codeVerifier } =
    await workos.userManagement.getAuthorizationUrlWithPKCE({
      clientId: CLIENT_ID,
      provider: "authkit",
      prompt: "login",
      maxAge: 0,
      redirectUri: REDIRECT_URI,
    });
  invalidateAuthOperations(dataDir);
  const store = await readAuthStore(dataDir);
  await writeAuthStore(dataDir, {
    ...store,
    pkce: {
      codeVerifier,
      state,
      expiresAt: Date.now() + PKCE_TTL_MS,
    },
  });
  if (useLoopbackRedirect) {
    startLoopbackCallbackServer(dataDir);
  }
  await shell.openExternal(url);
}

// What a verified callback produced: the signed-in account, and whether this
// callback exchanged its code (a new sign-in) rather than returning the account
// an earlier callback had already signed in.
interface CompletedCloudSignIn {
  account: CloudAccount;
  exchanged: boolean;
}

// completeCloudSignIn validates the pending PKCE state and exchanges the code
// for a session. Shared by the deep-link path and the loopback callback server.
async function completeCloudSignIn(
  code: string | null,
  callbackState: string | null,
  dataDir: string,
): Promise<CompletedCloudSignIn> {
  if (!workos) throw new Error("WorkOS is not configured.");
  if (!code || !callbackState) throw new Error("WorkOS callback is incomplete.");

  const callbackDigest = createHash("sha256")
    .update(JSON.stringify([code, callbackState]))
    .digest("hex");
  return withAuthMutation(dataDir, async () => {
    const store = await readAuthStore(dataDir);
    if (
      store.session &&
      !isLocalSession(store.session) &&
      (!store.pkce || store.completedCallback === callbackDigest)
    ) {
      // With no login pending, returning to an already signed-in app cannot
      // change its account. This also covers sessions saved before deduping.
      return { account: publicAccount(store.session), exchanged: false };
    }
    if (!store.pkce) throw new Error("No WorkOS sign-in is pending.");
    if (store.pkce.expiresAt < Date.now()) {
      await writeAuthStore(dataDir, { ...store, pkce: null });
      throw new Error("The WorkOS sign-in request expired.");
    }
    if (callbackState !== store.pkce.state) {
      throw new Error("WorkOS callback state did not match.");
    }

    const result = await workos!.userManagement.authenticateWithCode({
      clientId: CLIENT_ID,
      code,
      codeVerifier: store.pkce.codeVerifier,
    });
    const session = toStoredSession(
      result.accessToken,
      result.refreshToken,
      result.user,
    );
    await writeAuthStore(dataDir, {
      session,
      pkce: null,
      completedCallback: callbackDigest,
    });
    return { account: publicAccount(session), exchanged: true };
  });
}

export interface CloudDeepLinkSignIn {
  account: CloudAccount;
  // Present only when this link completed a new sign-in, so a repeated return
  // link never hands it over twice. It holds what the orchestrator.inc return
  // page adds: the website visitor id (web_distinct_id) for visitors who
  // accepted analytics there, when it is well formed. Analytics only: it
  // plays no part in signing in, and nothing stores or logs it.
  signIn?: CloudSignInContext;
}

export async function handleCloudDeepLink(
  rawURL: string,
  dataDir: string,
): Promise<CloudDeepLinkSignIn | null> {
  if (!workos) throw new Error("WorkOS is not configured.");
  const url = new URL(rawURL);
  if (url.protocol !== "ao-app:" || url.hostname !== "callback") return null;
  const error = url.searchParams.get("error");
  if (error) {
    throw new Error(
      url.searchParams.get("error_description") || `WorkOS sign-in failed: ${error}`,
    );
  }
  const { account, exchanged } = await completeCloudSignIn(
    url.searchParams.get("code"),
    url.searchParams.get("state"),
    dataDir,
  );
  if (!exchanged) return { account };
  // Read only once signing in has finished, so it cannot change how that went.
  // A sign-in without the website's id still hands over an empty context: it
  // tells the renderer a sign-in completed, even for the account already
  // signed in, which the session notification alone does not.
  const webDistinctId = url.searchParams.get("web_distinct_id");
  return { account, signIn: isWebDistinctId(webDistinctId) ? { webDistinctId } : {} };
}

const CALLBACK_HTML = (title: string, body: string): string =>
  `<!doctype html><meta charset="utf-8"><title>${title}</title>` +
  `<body style="font:15px -apple-system,system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#111">` +
  `<h1 style="font-size:1.25rem">${title}</h1><p style="color:#555">${body}</p></body>`;

function closeLoopbackServer(): void {
  if (loopbackServer) {
    loopbackServer.close();
    loopbackServer = null;
  }
}

// startLoopbackCallbackServer arms a one-shot local HTTP server that receives
// the WorkOS redirect for a development build, exchanges the code, and hands the
// session to the renderer. It closes itself after the first callback or the PKCE
// window, whichever comes first.
function startLoopbackCallbackServer(dataDir: string): void {
  const redirect = new URL(REDIRECT_URI);
  closeLoopbackServer();
  const server = createServer((req, res) => {
    const requestURL = new URL(req.url ?? "/", `http://${redirect.host}`);
    if (requestURL.pathname !== redirect.pathname) {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("Not found");
      return;
    }
    void (async () => {
      try {
        const error = requestURL.searchParams.get("error");
        if (error) {
          throw new Error(
            requestURL.searchParams.get("error_description") ||
              `WorkOS sign-in failed: ${error}`,
          );
        }
        const { account } = await completeCloudSignIn(
          requestURL.searchParams.get("code"),
          requestURL.searchParams.get("state"),
          dataDir,
        );
        notifyRenderersFn?.(account);
        res.writeHead(200, { "Content-Type": "text/html" });
        res.end(
          CALLBACK_HTML(
            "Signed in to Orchestrator Cloud",
            "You can close this tab and return to Orchestrator.",
          ),
        );
      } catch (callbackError) {
        console.error("WorkOS loopback callback failed:", callbackError);
        res.writeHead(400, { "Content-Type": "text/html" });
        res.end(
          CALLBACK_HTML(
            "Sign-in failed",
            "Return to Orchestrator and try signing in again.",
          ),
        );
      } finally {
        closeLoopbackServer();
      }
    })();
  });
  server.on("error", (error) => {
    console.error("WorkOS loopback callback server error:", error);
    closeLoopbackServer();
  });
  server.listen(Number(redirect.port) || 80, redirect.hostname);
  loopbackServer = server;
  const timer = setTimeout(closeLoopbackServer, PKCE_TTL_MS);
  timer.unref?.();
}

export async function signOutCloud(dataDir: string): Promise<void> {
  invalidateAuthOperations(dataDir);
  await withAuthMutation(dataDir, () => removeAuthStore(dataDir));
}

function signInFailureDetail(error: unknown): string {
  if (error instanceof AuthStoreUnreadableError) {
    return "Orchestrator could not read its saved sign-in just now. Nothing was changed. Try again in a moment.";
  }
  const message = error instanceof Error ? error.message.toLowerCase() : "";
  if (message.includes("cancel") || message.includes("access_denied")) {
    return "The WorkOS sign-in was cancelled. You can try again whenever you are ready.";
  }
  if (message.includes("expired")) {
    return "The WorkOS sign-in request expired. Start sign-in again to continue.";
  }
  if (
    message.includes("state") ||
    message.includes("pending") ||
    message.includes("incomplete")
  ) {
    return "The WorkOS response could not be verified. Start sign-in again to continue.";
  }
  if (message.includes("network") || message.includes("fetch")) {
    return "Orchestrator could not reach WorkOS. Check your connection and try again.";
  }
  return "Orchestrator could not complete WorkOS sign-in. Please try again.";
}

export async function showCloudSignInFailure(error: unknown): Promise<void> {
  await dialog.showMessageBox({
    type: "error",
    title: "Orchestrator Cloud sign-in failed",
    message: "Unable to sign in to Orchestrator Cloud",
    detail: signInFailureDetail(error),
  });
}

// registerCloudProtocol makes this build the ao-app:// handler, or gives the
// scheme up when it shouldn't hold it. Call it once this process is the app's
// single instance, before app.whenReady().
//
// A development build signing in through the loopback redirect never needs
// ao-app://, and claiming it takes the scheme from the installed app: macOS
// ignores the path/args below and binds it to the stock node_modules
// Electron.app, Windows points it at a worktree script, and the installed app's
// sign-in callbacks and billing return links then land in the wrong process. So
// such a build only releases a claim an earlier dev run left behind. On macOS
// that hands the scheme back to the installed app at once; on Windows it
// deletes the stale entry, and the installed app takes the scheme again the
// next time it starts. Linux has no release (Electron's is a no-op there), and
// a Linux dev build registers the installed app's own .desktop entry anyway.
//
// A development build pointed at a deep-link redirect (AO_CLOUD_AUTH_REDIRECT
// set to a bounce page or ao-app://callback) does need the scheme. It claims it
// for as long as it runs and gives it back when it quits.
export function registerCloudProtocol(): void {
  // A dev build is launched as `electron <app dir>`; Windows ties the scheme to
  // that exact command, so every call below names it.
  const devApp: [path?: string, args?: string[]] =
    process.defaultApp && process.argv.length >= 2
      ? [process.execPath, [path.resolve(process.argv[1])]]
      : [];
  const canRelease = process.platform !== "linux";
  if (!app.isPackaged && useLoopbackRedirect) {
    if (canRelease && app.isDefaultProtocolClient("ao-app", ...devApp)) {
      app.removeAsDefaultProtocolClient("ao-app", ...devApp);
    }
    return;
  }
  app.setAsDefaultProtocolClient("ao-app", ...devApp);
  if (!app.isPackaged && canRelease) {
    app.once("will-quit", () => {
      app.removeAsDefaultProtocolClient("ao-app", ...devApp);
    });
  }
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// The control plane's reason for refusing an account login (the same login is
// already saved, or a sign-in again used another login), from its error
// envelope's message. Never the response body itself.
async function controlPlaneRefusal(response: Response): Promise<string | undefined> {
  if (response.status !== 409 && response.status !== 422) return undefined;
  try {
    const envelope = (await response.json()) as { message?: unknown };
    return typeof envelope.message === "string" && envelope.message.trim() !== "" ? envelope.message.slice(0, 300) : undefined;
  } catch {
    return undefined;
  }
}

// Whether the control plane saved an account login as its harness's default
// account, from its answer ({ agentAccount: { isDefault } }).
async function savedAsDefaultAccount(response: Response): Promise<boolean> {
  try {
    const answer = (await response.json()) as { agentAccount?: { isDefault?: unknown } };
    return answer.agentAccount?.isDefault === true;
  } catch {
    return false;
  }
}

export function installCloudIPC(
  getDataDir: () => string,
  notifyRenderers: (session: CloudAccount | null) => void,
  // Runs before an explicit sign-out completes, so its effects are durable even
  // if the app quits right after.
  onSignedOut: () => Promise<void> = async () => undefined,
): void {
  notifyRenderersFn = notifyRenderers;
  ipcMain.handle("cloud:getSession", () => getCloudSessionCached(getDataDir()));
  ipcMain.handle("cloud:signIn", async () => {
    if (!workos) {
      await dialog.showMessageBox({
        type: "info",
        title: "WorkOS not configured",
        message: "WorkOS sign-in is not configured.",
        detail:
          "Set VITE_WORKOS_CLIENT_ID and restart Orchestrator to enable sign-in.",
      });
      return;
    }
    try {
      await beginCloudSignIn(getDataDir());
    } catch (error) {
      // The renderer starts sign-in without waiting on it, so say why it did not open.
      if (!(error instanceof AuthStoreUnreadableError)) throw error;
      await showCloudSignInFailure(error);
    }
  });
  ipcMain.handle("cloud:signOut", async () => {
    const dataDir = getDataDir();
    // A local (opaque-token) session is revoked server-side first, best-effort;
    // WorkOS sessions skip this. Store removal is uniform via signOutCloud.
    try {
      const store = await readAuthStore(dataDir);
      if (isLocalSession(store.session)) {
        await revokeLocalSession(store.session);
      }
    } catch {
      // Sign-out must never fail on a revoke round-trip; clear the store anyway.
    }
    await signOutCloud(dataDir);
    await onSignedOut().catch(() => undefined);
    notifyRenderers(null);
  });
  ipcMain.handle("cloud:cancelProviderAuth", async () => {
    if (activeProviderAuthAbort) {
      activeProviderAuthAbort.abort();
      activeProviderAuthAbort = null;
    }
  });
  ipcMain.handle("cloud:connectProviderAuth", async (_event, input: unknown) => {
    if (typeof input !== "object" || input === null) throw new Error("Invalid Cloud provider login request.");
    const { baseUrl, provider, persistLocalClaudeToken, account } = input as Record<string, unknown>;
    if (typeof baseUrl !== "string" || typeof provider !== "string") throw new Error("Invalid Cloud provider login request.");
    // account: "new" saves the login as another account, an account id signs
    // that account in again; absent, the login replaces the agent's default.
    if (account !== undefined && account !== "new" && !(typeof account === "string" && UUID_PATTERN.test(account))) throw new Error("Invalid Cloud provider login request.");
    let base: URL;
    try {
      base = new URL(baseUrl);
    } catch {
      throw new Error("Cloud control plane URL is invalid.");
    }
    if (base.protocol !== "https:" && !(base.protocol === "http:" && (base.hostname === "localhost" || base.hostname === "127.0.0.1"))) throw new Error("Cloud control plane must use HTTPS.");
    if (base.username !== "" || base.password !== "" || base.search !== "" || base.hash !== "") throw new Error("Cloud control plane URL must not include credentials, a query string, or a fragment.");
    const dataDir = getDataDir();
    const token = await getCloudAccessToken(dataDir);
    if (!token) throw new Error("Sign in to Orchestrator Cloud before connecting a provider.");
    
    if (activeProviderAuthAbort) activeProviderAuthAbort.abort();
    activeProviderAuthAbort = new AbortController();
    let credential;
    try {
      credential = await providerAuthFlow(provider).authenticate(dataDir, activeProviderAuthAbort.signal);
    } finally {
      activeProviderAuthAbort = null;
    }

    if (credential.provider !== provider) throw new Error("Cloud provider login returned an unexpected provider.");

    if (provider === "github") {
      // Returned to the renderer, which saves it via the daemon's
      // PUT /api/v1/github/pat endpoint. Include the OAuth refresh material so
      // the daemon can renew an expiring GitHub App token without a reconnect.
      return {
        secret: credential.secret,
        refreshToken: credential.refreshToken,
        expiresIn: credential.expiresIn,
        refreshTokenExpiresIn: credential.refreshTokenExpiresIn,
      };
    }

    const basePath = base.pathname.replace(/\/+$/, "");
    // Cloud agent credentials are always personal: the caller's own connection,
    // usable in every org they belong to.
    const save = account === "new"
      ? { method: "POST", path: "/me/agent-accounts", body: { agent: credential.provider, credentialType: credential.credentialType, secret: credential.secret } }
      : typeof account === "string"
        ? { method: "PUT", path: `/me/agent-accounts/${account}/credential`, body: { credentialType: credential.credentialType, secret: credential.secret } }
        : { method: "PUT", path: `/me/providers/${encodeURIComponent(credential.provider)}`, body: { credentialType: credential.credentialType, secret: credential.secret } };
    const target = new URL(`${base.origin}${basePath}/api/cloud/v1${save.path}`);
    const response = await fetch(target, {
      method: save.method,
      redirect: "error",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(save.body),
    });
    if (!response.ok) throw new Error(await controlPlaneRefusal(response) ?? "Orchestrator Cloud could not save the provider credential.");

    // Persist the captured Claude setup-token locally as a fallback for local
    // sessions, only once the cloud copy is saved: a failed cloud save must not
    // leave a local login behind. The daemon's claudecode adapter injects it only
    // when no native login is present, so it never shadows an existing login.
    // Of a harness's several accounts only the default's: the control plane
    // says which account the login became (an added one is the default when it
    // is the first, or when the default stopped working).
    if (persistLocalClaudeToken === true && credential.provider === "claude-code" && credential.credentialType === "oauth_token" &&
      (account === undefined || await savedAsDefaultAccount(response))) {
      await persistLocalClaudeOAuthToken(dataDir, credential.secret);
    }
    return undefined;
  });
}
