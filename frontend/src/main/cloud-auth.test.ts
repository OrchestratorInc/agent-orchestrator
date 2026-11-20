import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

const ACCESS_TOKEN = `header.${Buffer.from(
  JSON.stringify({ exp: 4_102_444_800, sid: "session_123" }),
).toString("base64url")}.signature`;
const EXPIRED_ACCESS_TOKEN = `header.${Buffer.from(
  JSON.stringify({ exp: 1, sid: "session_123" }),
).toString("base64url")}.signature`;

const mocks = vi.hoisted(() => ({
  authenticateWithCode: vi.fn(),
  authenticateWithRefreshToken: vi.fn(),
  decryptString: vi.fn((value: Buffer) => value.toString("utf8")),
  encryptString: vi.fn((value: string) => Buffer.from(value, "utf8")),
  encryptionAvailable: true,
  selectedStorageBackend: "gnome_libsecret",
  getAuthorizationUrlWithPKCE: vi.fn(),
  ipcHandle: vi.fn(),
  notifyRenderers: vi.fn(),
  openExternal: vi.fn(),
  showMessageBox: vi.fn(),
  providerAuthenticate: vi.fn(),
  // Error codes the next reads of the login file fail with, one per read.
  storeReadFailures: [] as string[],
  storeReads: 0,
}));

// Reads of the login file can be made to fail with a chosen code; every other
// file operation is the real one.
vi.mock("node:fs/promises", async (importOriginal) => {
  const actual = await importOriginal<typeof import("node:fs/promises")>();
  const readFile = ((...args: Parameters<typeof actual.readFile>) => {
    if (String(args[0]).endsWith("cloud-auth.bin")) {
      mocks.storeReads += 1;
      const code = mocks.storeReadFailures.shift();
      if (code) return Promise.reject(Object.assign(new Error(`${code}: synthetic read failure`), { code }));
    }
    return actual.readFile(...args);
  }) as typeof actual.readFile;
  return { ...actual, readFile, default: { ...actual, readFile } };
});

// The browser login itself is exercised in provider-auth-flow tests; here it
// just returns a captured credential.
vi.mock("./provider-auth-flow", () => ({
  providerAuthFlow: () => ({ authenticate: mocks.providerAuthenticate }),
}));

vi.mock("@workos-inc/node", () => ({
  createWorkOS: () => ({
    userManagement: {
      authenticateWithCode: mocks.authenticateWithCode,
      authenticateWithRefreshToken: mocks.authenticateWithRefreshToken,
      getAuthorizationUrlWithPKCE: mocks.getAuthorizationUrlWithPKCE,
    },
  }),
}));

vi.mock("electron", () => ({
  app: {
    isDefaultProtocolClient: vi.fn(() => false),
    once: vi.fn(),
    removeAsDefaultProtocolClient: vi.fn(),
    setAsDefaultProtocolClient: vi.fn(),
    // These tests exercise the packaged deep-link (ao-app://) sign-in flow;
    // unpackaged builds default to a loopback redirect instead.
    isPackaged: true,
  },
  dialog: { showMessageBox: mocks.showMessageBox },
  ipcMain: { handle: mocks.ipcHandle },
  safeStorage: {
    decryptString: mocks.decryptString,
    encryptString: mocks.encryptString,
    getSelectedStorageBackend: () => mocks.selectedStorageBackend,
    isEncryptionAvailable: () => mocks.encryptionAvailable,
  },
  shell: { openExternal: mocks.openExternal },
}));

import {
  AuthStoreUnreadableError,
  beginCloudSignIn,
  getCloudAccessToken,
  getCloudSession,
  handleCloudDeepLink,
  installCloudIPC,
  readAuthStore,
  resolveCloudAuthDir,
  resolveCloudAuthRedirect,
  showCloudSignInFailure,
  signOutCloud,
  writeAuthStore,
} from "./cloud-auth";

describe("native WorkOS authentication", () => {
  let dataDir: string;

  beforeEach(async () => {
    vi.clearAllMocks();
    mocks.storeReadFailures = [];
    mocks.storeReads = 0;
    mocks.encryptionAvailable = true;
    mocks.selectedStorageBackend = "gnome_libsecret";
    dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-cloud-auth-"));
    installCloudIPC(() => dataDir, mocks.notifyRenderers);
    mocks.getAuthorizationUrlWithPKCE.mockResolvedValue({
      url: "https://workos.example/authorize",
      state: "state_123",
      codeVerifier: "verifier_123",
    });
    mocks.authenticateWithCode.mockResolvedValue({
      accessToken: ACCESS_TOKEN,
      refreshToken: "refresh_123",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
        firstName: "Person",
        lastName: "Example",
      },
    });
  });

  afterEach(async () => {
    vi.restoreAllMocks();
    await rm(dataDir, { recursive: true, force: true });
  });

  it("starts PKCE and exchanges the callback without an AO website", async () => {
    await beginCloudSignIn(dataDir);
    expect(mocks.openExternal).toHaveBeenCalledWith(
      "https://workos.example/authorize",
    );
    expect(mocks.getAuthorizationUrlWithPKCE).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: "authkit",
        prompt: "login",
        maxAge: 0,
        redirectUri: "https://api.aoagents.dev/app/auth/return",
      }),
    );

    const signIn = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );
    // A completed sign-in is marked even without the website's visitor id.
    expect(signIn?.signIn).toEqual({});
    const session = signIn?.account;
    expect(session).toMatchObject({
      authProvider: "workos",
      user: {
        id: "user_123",
        email: "person@example.com",
        displayName: "Person Example",
      },
    });
    expect(session).not.toHaveProperty("accessToken");
    expect(session).not.toHaveProperty("refreshToken");
    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
  });

  it("silently accepts the same completed callback without exchanging its code again", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    const first = await handleCloudDeepLink(callback, dataDir);

    // The repeat returns the same account, and no completed sign-in to report again.
    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual({ account: first?.account });
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
    expect(mocks.showMessageBox).not.toHaveBeenCalled();
  });

  it("exchanges a callback once when the OS delivers it concurrently", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    const accounts = await Promise.all([
      handleCloudDeepLink(callback, dataDir),
      handleCloudDeepLink(callback, dataDir),
    ]);

    expect(accounts[0]?.account).toEqual(accounts[1]?.account);
    // Only the call that exchanged the code reports a completed sign-in.
    expect(accounts.filter((result) => result?.signIn)).toHaveLength(1);
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("ignores callbacks with no login pending without changing the signed-in account", async () => {
    await beginCloudSignIn(dataDir);
    const account = (await handleCloudDeepLink("ao-app://callback?code=code_123&state=state_123", dataDir))?.account;
    await expect(handleCloudDeepLink(
      "ao-app://callback?code=different_code&state=state_123", dataDir,
    )).resolves.toEqual({ account });
    await expect(handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=unverified_state", dataDir,
    )).resolves.toEqual({ account });
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("quietly returns to an account saved before callback deduplication was added", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    const account = (await handleCloudDeepLink(callback, dataDir))?.account;
    const store = await readAuthStore(dataDir);
    delete store.completedCallback;
    await writeAuthStore(dataDir, store);

    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual({ account });
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("does not restore a signed-out account when its old return button is clicked", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    await handleCloudDeepLink(callback, dataDir);
    await signOutCloud(dataDir);

    await expect(handleCloudDeepLink(callback, dataDir)).rejects.toThrow("No WorkOS sign-in is pending");
    await expect(getCloudSession(dataDir)).resolves.toBeNull();
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("keeps a newer pending login when an already completed callback repeats", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    const account = (await handleCloudDeepLink(callback, dataDir))?.account;
    mocks.getAuthorizationUrlWithPKCE.mockResolvedValueOnce({
      url: "https://workos.example/authorize", state: "state_456", codeVerifier: "verifier_456",
    });
    await beginCloudSignIn(dataDir);

    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual({ account });
    await handleCloudDeepLink("ao-app://callback?code=code_456&state=state_456", dataDir);
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(2);
    expect(mocks.authenticateWithCode).toHaveBeenLastCalledWith(expect.objectContaining({
      code: "code_456", codeVerifier: "verifier_456",
    }));
  });

  // The orchestrator.inc return page adds web_distinct_id for visitors who
  // accepted website analytics. It must never change how signing in goes.
  describe("website visitor id on the return link", () => {
    const VISITOR = "019a3f5e-8c2b-7d41-b6f0-3e2d1c0b9a87";
    const callback = (extra = "") => `ao-app://callback?code=code_123&state=state_123${extra}`;

    // Everything the sign-in produced, for comparing a run with and without the id.
    async function signInWith(extra: string) {
      await beginCloudSignIn(dataDir);
      const result = await handleCloudDeepLink(callback(extra), dataDir);
      const store = await readAuthStore(dataDir);
      return {
        result,
        account: result && { ...result.account, storedAt: "<time>" },
        exchange: [...mocks.authenticateWithCode.mock.calls],
        store: { ...store, session: store.session && { ...store.session, storedAt: "<time>" } },
      };
    }

    async function filesUnder(dir: string): Promise<string> {
      const names = await readdir(dir, { recursive: true, withFileTypes: true });
      const files = names.filter((entry) => entry.isFile()).map((entry) => path.join(entry.parentPath, entry.name));
      return (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
    }

    it("signs in exactly as without it, and hands a well-formed id over with the account", async () => {
      const without = await signInWith("");
      await signOutCloud(dataDir);
      mocks.authenticateWithCode.mockClear();
      const withVisitor = await signInWith(`&web_distinct_id=${VISITOR}`);

      expect(withVisitor.exchange).toEqual(without.exchange);
      expect(withVisitor.exchange).toEqual([[{ clientId: "client_test", code: "code_123", codeVerifier: "verifier_123" }]]);
      expect(withVisitor.store).toEqual(without.store);
      expect(withVisitor.account).toEqual(without.account);
      expect(without.result).toEqual({ account: without.result?.account, signIn: {} });
      expect(withVisitor.result).toEqual({ account: withVisitor.result?.account, signIn: { webDistinctId: VISITOR } });
    });

    it("decodes the id the website percent-encodes", async () => {
      const { result } = await signInWith("&web_distinct_id=%24device%3Aabc.123_x-y");
      expect(result?.signIn?.webDistinctId).toBe("$device:abc.123_x-y");
    });

    it.each([
      ["empty", ""],
      ["a space", "abc%20def"],
      ["a slash", "abc%2Fdef"],
      ["markup", "%3Cscript%3E"],
      ["non-ASCII", "caf%C3%A9"],
      ["too long", "a".repeat(201)],
      ["a placeholder PostHog never merges", "undefined"],
      ["the cookieless placeholder", "%24posthog_cookieless"],
    ])("ignores an id with %s and signs in the same", async (_name, value) => {
      const without = await signInWith("");
      await signOutCloud(dataDir);
      mocks.authenticateWithCode.mockClear();
      const invalid = await signInWith(`&web_distinct_id=${value}`);

      expect(invalid.result).toEqual({ account: invalid.result?.account, signIn: {} });
      expect(invalid.exchange).toEqual(without.exchange);
      expect(invalid.store).toEqual(without.store);
      expect(invalid.account).toEqual(without.account);
    });

    it("accepts the longest id the website sends", async () => {
      const { result } = await signInWith(`&web_distinct_id=${"a".repeat(200)}`);
      expect(result?.signIn?.webDistinctId).toBe("a".repeat(200));
    });

    it("hands the id over once: a repeated return link signs nothing in and carries none", async () => {
      await beginCloudSignIn(dataDir);
      const link = callback(`&web_distinct_id=${VISITOR}`);
      const first = await handleCloudDeepLink(link, dataDir);
      const again = await handleCloudDeepLink(link, dataDir);
      // The same code and state with a different id is still the same callback.
      const otherVisitor = await handleCloudDeepLink(callback("&web_distinct_id=someone-else"), dataDir);

      expect(first?.signIn?.webDistinctId).toBe(VISITOR);
      expect(again).toEqual({ account: first?.account });
      expect(otherVisitor).toEqual({ account: first?.account });
      expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
    });

    it("hands the id over once when the OS delivers the link twice at the same time", async () => {
      await beginCloudSignIn(dataDir);
      const link = callback(`&web_distinct_id=${VISITOR}`);
      const results = await Promise.all([handleCloudDeepLink(link, dataDir), handleCloudDeepLink(link, dataDir)]);
      expect(results.filter((result) => result?.signIn?.webDistinctId === VISITOR)).toHaveLength(1);
      expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
    });

    it("never stores or logs the id", async () => {
      const logs = [vi.spyOn(console, "log"), vi.spyOn(console, "info"), vi.spyOn(console, "warn"), vi.spyOn(console, "error")];
      await signInWith(`&web_distinct_id=${VISITOR}`);
      // The test safeStorage mock "encrypts" to plain text, so the store is readable here.
      const stored = await readFile(path.join(dataDir, "cloud-auth.bin"), "utf8");
      expect(stored).toContain("user_123");
      expect(stored).not.toContain(VISITOR);
      expect(await filesUnder(dataDir)).not.toContain(VISITOR);
      expect(JSON.stringify(await readAuthStore(dataDir))).not.toContain(VISITOR);
      for (const log of logs) expect(JSON.stringify(log.mock.calls)).not.toContain(VISITOR);
    });

    it("keeps an error return exactly as before, whatever id rides along", async () => {
      await beginCloudSignIn(dataDir);
      const pending = await readAuthStore(dataDir);
      await expect(handleCloudDeepLink(
        `ao-app://callback?error=access_denied&error_description=User%20cancelled&web_distinct_id=${VISITOR}`,
        dataDir,
      )).rejects.toThrow("User cancelled");
      await expect(handleCloudDeepLink(
        `ao-app://callback?error=access_denied&web_distinct_id=${VISITOR}`,
        dataDir,
      )).rejects.toThrow("WorkOS sign-in failed: access_denied");
      expect(mocks.authenticateWithCode).not.toHaveBeenCalled();
      await expect(readAuthStore(dataDir)).resolves.toEqual(pending);
    });

    it("keeps a state mismatch a failure, whatever id rides along", async () => {
      await beginCloudSignIn(dataDir);
      await expect(handleCloudDeepLink(
        `ao-app://callback?code=code_123&state=attacker_state&web_distinct_id=${VISITOR}`,
        dataDir,
      )).rejects.toThrow("state did not match");
      await expect(handleCloudDeepLink(
        `ao-app://callback?web_distinct_id=${VISITOR}`,
        dataDir,
      )).rejects.toThrow("WorkOS callback is incomplete");
      expect(mocks.authenticateWithCode).not.toHaveBeenCalled();
    });
  });

  it("requires an AO Cloud session before starting provider login", async () => {
    const handler = mocks.ipcHandle.mock.calls.find(
      ([channel]) => channel === "cloud:connectProviderAuth",
    )?.[1] as ((event: unknown, input: unknown) => Promise<void>) | undefined;
    expect(handler).toBeTypeOf("function");
    await expect(
      handler?.({}, {
        baseUrl: "https://cloud.example",
        provider: "codex",
      }),
    ).rejects.toThrow("Sign in to Orchestrator Cloud before connecting a provider.");
  });

  describe("Claude browser login local fallback token", () => {
    const tokenFile = () => path.join(dataDir, "harnesses", "claude-code", "oauth-token");
    const connect = async () => {
      const handler = mocks.ipcHandle.mock.calls.find(
        ([channel]) => channel === "cloud:connectProviderAuth",
      )?.[1] as (event: unknown, input: unknown) => Promise<void>;
      return handler({}, { baseUrl: "https://cloud.example", provider: "claude-code", persistLocalClaudeToken: true });
    };

    beforeEach(async () => {
      await beginCloudSignIn(dataDir);
      await handleCloudDeepLink("ao-app://callback?code=code_123&state=state_123", dataDir);
      mocks.providerAuthenticate.mockResolvedValue({
        provider: "claude-code",
        credentialType: "oauth_token",
        secret: "sk-ant-oat01-test-token",
      });
    });

    it("keeps the local token once the cloud credential is saved", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 200 }));
      await connect();
      await expect(readFile(tokenFile(), "utf8")).resolves.toBe("sk-ant-oat01-test-token");
    });

    it("does not leave a local token behind when the cloud save fails", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 500 }));
      await expect(connect()).rejects.toThrow("Orchestrator Cloud could not save the provider credential.");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });
    });

    it("does not leave a local token behind when the cloud is unreachable", async () => {
      vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("fetch failed"));
      await expect(connect()).rejects.toThrow("fetch failed");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });
    });

    // Local sessions use the default account's login: an account login is
    // kept locally only when the control plane saved it as the default.
    const connectAccount = async (account: string) => {
      const handler = mocks.ipcHandle.mock.calls.find(
        ([channel]) => channel === "cloud:connectProviderAuth",
      )?.[1] as (event: unknown, input: unknown) => Promise<void>;
      return handler({}, { baseUrl: "https://cloud.example", provider: "claude-code", persistLocalClaudeToken: true, account });
    };

    it("keeps an added account's token only when the control plane made it the default", async () => {
      const answer = (isDefault: boolean) => new Response(JSON.stringify({ agentAccount: { id: "a", isDefault } }), { status: 201 });
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(answer(false));
      await connectAccount("new");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });

      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(answer(true));
      await connectAccount("new");
      await expect(readFile(tokenFile(), "utf8")).resolves.toBe("sk-ant-oat01-test-token");
    });

    it("keeps a signed-in-again account's token only when it is the default", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
        new Response(JSON.stringify({ agentAccount: { id: "a", isDefault: false } }), { status: 200 }));
      await connectAccount("33333333-3333-4333-8333-333333333333");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });
    });
  });

  describe("Cloud agent accounts", () => {
    const connect = (account: unknown) => {
      const handler = mocks.ipcHandle.mock.calls.find(
        ([channel]) => channel === "cloud:connectProviderAuth",
      )?.[1] as (event: unknown, input: unknown) => Promise<void>;
      return handler({}, { baseUrl: "https://cloud.example", provider: "codex", account });
    };
    const accountId = "33333333-3333-4333-8333-333333333333";

    beforeEach(async () => {
      await beginCloudSignIn(dataDir);
      await handleCloudDeepLink("ao-app://callback?code=code_123&state=state_123", dataDir);
      mocks.providerAuthenticate.mockResolvedValue({ provider: "codex", credentialType: "auth_json", secret: "{\"tokens\":{}}" });
    });

    it("saves a login as another account, or signs an account in again", async () => {
      const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 201 }));
      await connect("new");
      await connect(accountId);
      const requests = fetchMock.mock.calls.map(([url, init]) => ({ url: String(url), method: init?.method, body: JSON.parse(String(init?.body)) }));
      expect(requests).toEqual([
        { url: "https://cloud.example/api/cloud/v1/me/agent-accounts", method: "POST", body: { agent: "codex", credentialType: "auth_json", secret: "{\"tokens\":{}}" } },
        { url: `https://cloud.example/api/cloud/v1/me/agent-accounts/${accountId}/credential`, method: "PUT", body: { credentialType: "auth_json", secret: "{\"tokens\":{}}" } },
      ]);
    });

    it("refuses an account that is not an id before the login starts", async () => {
      await expect(connect("../providers/codex")).rejects.toThrow("Invalid Cloud provider login request.");
      expect(mocks.providerAuthenticate).not.toHaveBeenCalled();
    });

    it("shows why the control plane refused the login", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ code: "AGENT_ACCOUNT_DUPLICATE", message: "That login is already saved." }), { status: 409 }));
      await expect(connect("new")).rejects.toThrow("That login is already saved.");
    });
  });

  it("rejects callbacks whose OAuth state does not match", async () => {
    await beginCloudSignIn(dataDir);
    await expect(
      handleCloudDeepLink(
        "ao-app://callback?code=code_123&state=attacker_state",
        dataDir,
      ),
    ).rejects.toThrow("state did not match");
  });

  it("keeps the auth store in memory when OS encryption is unavailable", async () => {
    mocks.encryptionAvailable = false;
    await beginCloudSignIn(dataDir);
    const signIn = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    expect(signIn?.account.user.email).toBe("person@example.com");
    await expect(
      readFile(path.join(dataDir, "cloud-auth.bin")),
    ).rejects.toMatchObject({ code: "ENOENT" });
    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
    await signOutCloud(dataDir);
  });

  it("keeps the auth store in memory with Linux basic_text storage", async () => {
    vi.spyOn(process, "platform", "get").mockReturnValue("linux");
    mocks.selectedStorageBackend = "basic_text";
    await beginCloudSignIn(dataDir);
    const signIn = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    expect(signIn?.account.user.email).toBe("person@example.com");
    await expect(
      readFile(path.join(dataDir, "cloud-auth.bin")),
    ).rejects.toMatchObject({ code: "ENOENT" });
    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
    await signOutCloud(dataDir);
  });

  // Secure storage can be out for a while: a locked keychain, or a Linux
  // keyring that had not started when the app did (basic_text for that run).
  describe("through a secure storage outage", () => {
    type CloudAuth = typeof import("./cloud-auth");
    const PERSON = { id: "user_123", email: "person@example.com", name: "Person Example" };
    const OTHER = { id: "user_456", email: "other@example.com", name: "Other Person" };
    const storeFile = () => path.join(dataDir, "cloud-auth.bin");
    const fileStore = async () => JSON.parse(await readFile(storeFile(), "utf8")) as Record<string, unknown>;
    const signedInAs = (email: string) => expect.objectContaining({ user: expect.objectContaining({ email }) });
    let warn: MockInstance<typeof console.warn>;

    beforeEach(() => {
      warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
      mocks.decryptString.mockImplementation((value: Buffer) => value.toString("utf8"));
    });

    // A fresh copy of the module: what the app holds in memory is gone, the
    // file on disk stays.
    async function restart(): Promise<CloudAuth> {
      vi.resetModules();
      const auth = await import("./cloud-auth");
      auth.installCloudIPC(() => dataDir, mocks.notifyRenderers);
      return auth;
    }

    async function signIn(auth: CloudAuth, code = "code_123") {
      await auth.beginCloudSignIn(dataDir);
      return auth.handleCloudDeepLink(`ao-app://callback?code=${code}&state=state_123`, dataDir);
    }

    const outages: Array<[string, () => void]> = [
      ["encryption is unavailable", () => {
        mocks.encryptionAvailable = false;
      }],
      ["Linux falls back to basic_text", () => {
        vi.spyOn(process, "platform", "get").mockReturnValue("linux");
        mocks.selectedStorageBackend = "basic_text";
      }],
    ];
    const storageBack = () => {
      mocks.encryptionAvailable = true;
      mocks.selectedStorageBackend = "gnome_libsecret";
    };

    it.each(outages)("keeps the saved sign-in when %s at launch, and shows it again once storage is back", async (_name, outage) => {
      await signIn(await restart());
      outage();
      const auth = await restart();

      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      await expect(readFile(storeFile())).resolves.toBeInstanceOf(Buffer);

      storageBack();
      await expect(auth.getCloudSession(dataDir)).resolves.toEqual(signedInAs("person@example.com"));
      expect(mocks.notifyRenderers).toHaveBeenCalledWith(signedInAs("person@example.com"));
      await expect((await restart()).getCloudSession(dataDir)).resolves.toEqual(signedInAs("person@example.com"));
    });

    it("saves a sign-in made during the outage over the older saved account once storage is back", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      const auth = await restart();
      mocks.authenticateWithCode.mockResolvedValueOnce({ accessToken: ACCESS_TOKEN, refreshToken: "refresh_456", user: OTHER });
      await signIn(auth, "code_456");

      storageBack();
      await expect(auth.getCloudSession(dataDir)).resolves.toEqual(signedInAs("other@example.com"));
      expect(await fileStore()).toMatchObject({ session: { refreshToken: "refresh_456", user: { email: "other@example.com" } } });
      await expect((await restart()).getCloudSession(dataDir)).resolves.toEqual(signedInAs("other@example.com"));
    });

    it("says a sign-in made during the outage will not survive a restart, and never brings back the account it replaced", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      let auth = await restart();
      mocks.authenticateWithCode.mockResolvedValueOnce({ accessToken: ACCESS_TOKEN, refreshToken: "refresh_456", user: OTHER });

      await expect(signIn(auth, "code_456")).resolves.toMatchObject({ account: signedInAs("other@example.com") });
      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });
      expect(warn).toHaveBeenCalledOnce();
      expect(String(warn.mock.calls[0]?.[0])).toContain("will not survive a restart");

      auth = await restart();
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      storageBack();
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });
    });

    it("saves a token rotated during the outage once storage is back, warning once per sign-in", async () => {
      mocks.encryptionAvailable = false;
      const auth = await restart();
      mocks.authenticateWithCode.mockResolvedValueOnce({ accessToken: EXPIRED_ACCESS_TOKEN, refreshToken: "refresh_123", user: PERSON });
      mocks.authenticateWithRefreshToken.mockResolvedValueOnce({ accessToken: ACCESS_TOKEN, refreshToken: "refresh_456", user: PERSON });
      await signIn(auth);
      await expect(auth.getCloudSession(dataDir)).resolves.toEqual(signedInAs("person@example.com"));
      expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledOnce();

      storageBack();
      await auth.getCloudSession(dataDir);
      expect(await fileStore()).toMatchObject({ session: { refreshToken: "refresh_456" } });
      expect(warn).toHaveBeenCalledOnce();
    });

    it("keeps the saved sign-in when a sign-in started during the outage is not finished, and lets it finish later", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      const auth = await restart();
      await auth.beginCloudSignIn(dataDir);
      await expect(readFile(storeFile())).resolves.toBeInstanceOf(Buffer);

      storageBack();
      await expect(auth.getCloudSession(dataDir)).resolves.toEqual(signedInAs("person@example.com"));
      expect(await fileStore()).toMatchObject({ session: { user: { email: "person@example.com" } }, pkce: { state: "state_123" } });

      mocks.authenticateWithCode.mockResolvedValueOnce({ accessToken: ACCESS_TOKEN, refreshToken: "refresh_456", user: OTHER });
      await expect(
        auth.handleCloudDeepLink("ao-app://callback?code=code_456&state=state_123", dataDir),
      ).resolves.toMatchObject({ account: signedInAs("other@example.com") });
      expect(await fileStore()).toMatchObject({ session: { user: { email: "other@example.com" } }, pkce: null });
    });

    it("removes the saved sign-in for good when signing out during the outage", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      const auth = await restart();

      await auth.signOutCloud(dataDir);
      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });

      storageBack();
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      await expect((await restart()).getCloudSession(dataDir)).resolves.toBeNull();
      expect(mocks.notifyRenderers).not.toHaveBeenCalledWith(signedInAs("person@example.com"));
    });

    it("does not tell the renderers the account signed out when a token is asked for during the outage", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      const auth = await restart();

      await expect(auth.getCloudAccessToken(dataDir)).resolves.toBeNull();
      expect(auth.isSavedSignInHidden(dataDir)).toBe(true);
      expect(mocks.notifyRenderers).not.toHaveBeenCalledWith(null);
    });

    it("shows the saved sign-in again when the app is focused once storage is back", async () => {
      await signIn(await restart());
      mocks.encryptionAvailable = false;
      const auth = await restart();
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();

      auth.recheckSavedSignIn(dataDir);
      await Promise.resolve();
      expect(mocks.notifyRenderers).not.toHaveBeenCalledWith(signedInAs("person@example.com"));

      storageBack();
      auth.recheckSavedSignIn(dataDir);
      await vi.waitFor(() => expect(mocks.notifyRenderers).toHaveBeenCalledWith(signedInAs("person@example.com")));
      expect(auth.isSavedSignInHidden(dataDir)).toBe(false);
    });

    it("keeps a saved sign-in that does not decrypt, without asking the keychain again, until the next launch", async () => {
      await signIn(await restart());
      const saved = await readFile(storeFile());
      const auth = await restart();
      mocks.decryptString.mockImplementation(() => {
        throw new Error("keychain access denied");
      });
      mocks.decryptString.mockClear();

      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();
      expect(mocks.decryptString).toHaveBeenCalledOnce();
      expect(warn).toHaveBeenCalledOnce();
      expect(auth.isSavedSignInHidden(dataDir)).toBe(true);
      await expect(auth.getCloudAccessToken(dataDir)).resolves.toBeNull();
      expect(mocks.notifyRenderers).not.toHaveBeenCalledWith(null);
      // Starting a sign-in does not write over the file it could not read.
      await auth.beginCloudSignIn(dataDir);
      await expect(readFile(storeFile())).resolves.toEqual(saved);

      mocks.decryptString.mockImplementation((value: Buffer) => value.toString("utf8"));
      await expect((await restart()).getCloudSession(dataDir)).resolves.toEqual(signedInAs("person@example.com"));
    });

    it("saves a new sign-in over a saved one that does not decrypt", async () => {
      await signIn(await restart());
      const auth = await restart();
      mocks.decryptString.mockImplementationOnce(() => {
        throw new Error("keychain access denied");
      });
      await expect(auth.getCloudSession(dataDir)).resolves.toBeNull();

      mocks.authenticateWithCode.mockResolvedValueOnce({ accessToken: ACCESS_TOKEN, refreshToken: "refresh_456", user: OTHER });
      await signIn(auth, "code_456");

      expect(await fileStore()).toMatchObject({ session: { refreshToken: "refresh_456" }, pkce: null });
      expect(auth.isSavedSignInHidden(dataDir)).toBe(false);
      await expect((await restart()).getCloudSession(dataDir)).resolves.toEqual(signedInAs("other@example.com"));
    });
  });

  it("shows a bounded error when the callback cannot be completed", async () => {
    await showCloudSignInFailure(
      new Error("The WorkOS sign-in request expired with secret details"),
    );

    expect(mocks.showMessageBox).toHaveBeenCalledWith({
      type: "error",
      title: "Orchestrator Cloud sign-in failed",
      message: "Unable to sign in to Orchestrator Cloud",
      detail:
        "The WorkOS sign-in request expired. Start sign-in again to continue.",
    });
  });

  it("shares one rotating-token refresh between concurrent callers", async () => {
    mocks.authenticateWithCode.mockResolvedValueOnce({
      accessToken: EXPIRED_ACCESS_TOKEN,
      refreshToken: "refresh_123",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });
    let resolveRefresh:
      | ((value: {
          accessToken: string;
          refreshToken: string;
          user: {
            id: string;
            email: string;
            name: string;
          };
        }) => void)
      | undefined;
    mocks.authenticateWithRefreshToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRefresh = resolve;
      }),
    );
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    const first = getCloudSession(dataDir);
    const second = getCloudSession(dataDir);
    await vi.waitFor(() =>
      expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledOnce(),
    );
    resolveRefresh?.({
      accessToken: ACCESS_TOKEN,
      refreshToken: "refresh_456",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });

    const [firstAccount, secondAccount] = await Promise.all([first, second]);
    expect(firstAccount).toEqual(secondAccount);
    expect(firstAccount).not.toHaveProperty("accessToken");
    expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledOnce();
  });

  it("clears an expired session when token refresh fails", async () => {
    mocks.authenticateWithCode.mockResolvedValueOnce({
      accessToken: EXPIRED_ACCESS_TOKEN,
      refreshToken: "refresh_123",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });
    mocks.authenticateWithRefreshToken.mockRejectedValueOnce(
      new Error("refresh token already consumed"),
    );
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    await expect(getCloudSession(dataDir)).resolves.toBeNull();
    await expect(getCloudSession(dataDir)).resolves.toBeNull();
    expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledOnce();
    expect(mocks.notifyRenderers).toHaveBeenCalledWith(null);
  });

  it("publishes signed-out state when a token request finds the auth store missing", async () => {
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );
    await rm(path.join(dataDir, "cloud-auth.bin"), { force: true });

    await expect(getCloudAccessToken(dataDir)).resolves.toBeNull();
    expect(mocks.notifyRenderers).toHaveBeenCalledWith(null);
  });

  it("preserves encrypted credentials after a retryable refresh failure", async () => {
    mocks.authenticateWithCode.mockResolvedValueOnce({
      accessToken: EXPIRED_ACCESS_TOKEN,
      refreshToken: "refresh_123",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });
    mocks.authenticateWithRefreshToken
      .mockRejectedValueOnce(
        Object.assign(new Error("WorkOS temporarily unavailable"), {
          status: 503,
        }),
      )
      .mockResolvedValueOnce({
        accessToken: ACCESS_TOKEN,
        refreshToken: "refresh_456",
        user: {
          id: "user_123",
          email: "person@example.com",
          name: "Person Example",
        },
      });
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
    await expect(
      readFile(path.join(dataDir, "cloud-auth.bin")),
    ).resolves.toBeInstanceOf(Buffer);
    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
    expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledTimes(2);
  });

  it("does not restore a refreshed session after explicit sign-out", async () => {
    mocks.authenticateWithCode.mockResolvedValueOnce({
      accessToken: EXPIRED_ACCESS_TOKEN,
      refreshToken: "refresh_123",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });
    let resolveRefresh:
      | ((value: {
          accessToken: string;
          refreshToken: string;
          user: {
            id: string;
            email: string;
            name: string;
          };
        }) => void)
      | undefined;
    mocks.authenticateWithRefreshToken.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRefresh = resolve;
      }),
    );
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    const pendingSession = getCloudSession(dataDir);
    await vi.waitFor(() =>
      expect(mocks.authenticateWithRefreshToken).toHaveBeenCalledOnce(),
    );
    await signOutCloud(dataDir);
    resolveRefresh?.({
      accessToken: ACCESS_TOKEN,
      refreshToken: "refresh_456",
      user: {
        id: "user_123",
        email: "person@example.com",
        name: "Person Example",
      },
    });

    await expect(pendingSession).resolves.toBeNull();
    await expect(getCloudSession(dataDir)).resolves.toBeNull();
    await expect(
      readFile(path.join(dataDir, "cloud-auth.bin")),
    ).rejects.toMatchObject({ code: "ENOENT" });
  });

  it("signs out locally without opening the browser", async () => {
    await beginCloudSignIn(dataDir);
    await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );
    mocks.openExternal.mockClear();

    await signOutCloud(dataDir);

    expect(mocks.openExternal).not.toHaveBeenCalled();
    await expect(getCloudSession(dataDir)).resolves.toBeNull();
  });

  describe("a saved login the app cannot read just now", () => {
    const storeFile = () => path.join(dataDir, "cloud-auth.bin");

    beforeEach(async () => {
      await beginCloudSignIn(dataDir);
      await handleCloudDeepLink(
        "ao-app://callback?code=code_123&state=state_123",
        dataDir,
      );
      mocks.openExternal.mockClear();
      mocks.notifyRenderers.mockClear();
      mocks.storeReads = 0;
    });

    it("reads the login once the file handles free up", async () => {
      mocks.storeReadFailures = ["EMFILE", "EMFILE"];

      await expect(getCloudSession(dataDir)).resolves.toMatchObject({
        user: { email: "person@example.com" },
      });
      expect(mocks.storeReads).toBe(3);
    });

    it("keeps the login file when reads keep failing, and finds the login on the next read", async () => {
      const saved = await readFile(storeFile());
      mocks.storeReadFailures = ["EMFILE", "EMFILE", "EMFILE", "EMFILE"];

      const failed = await getCloudSession(dataDir).catch((error: unknown) => error);
      expect(failed).toBeInstanceOf(AuthStoreUnreadableError);
      expect(failed).toMatchObject({ code: "EMFILE" });
      // Only the code, never where the login is kept.
      expect((failed as Error).message).not.toContain(dataDir);
      expect(mocks.storeReadFailures).toEqual([]);
      await expect(readFile(storeFile())).resolves.toEqual(saved);
      expect(mocks.notifyRenderers).not.toHaveBeenCalled();

      await expect(getCloudSession(dataDir)).resolves.toMatchObject({
        user: { email: "person@example.com" },
      });
      await expect(readFile(storeFile())).resolves.toEqual(saved);
    });

    it("does not tell the proxies or renderers the account signed out", async () => {
      const saved = await readFile(storeFile());
      mocks.storeReadFailures = ["EMFILE", "EMFILE", "EMFILE", "EMFILE"];

      await expect(getCloudAccessToken(dataDir)).rejects.toBeInstanceOf(
        AuthStoreUnreadableError,
      );
      expect(mocks.notifyRenderers).not.toHaveBeenCalled();
      await expect(readFile(storeFile())).resolves.toEqual(saved);

      await expect(getCloudAccessToken(dataDir)).resolves.toBe(ACCESS_TOKEN);
    });

    it("keeps the login file without retrying a read it has no permission for", async () => {
      const saved = await readFile(storeFile());
      mocks.storeReads = 0;
      mocks.storeReadFailures = ["EACCES"];

      await expect(getCloudSession(dataDir)).rejects.toMatchObject({
        code: "EACCES",
      });
      expect(mocks.storeReads).toBe(1);
      await expect(readFile(storeFile())).resolves.toEqual(saved);
      await expect(getCloudSession(dataDir)).resolves.toMatchObject({
        user: { email: "person@example.com" },
      });
    });

    it("does not write a new sign-in over the login it could not read", async () => {
      const saved = await readFile(storeFile());
      mocks.storeReadFailures = ["EIO"];

      await expect(beginCloudSignIn(dataDir)).rejects.toBeInstanceOf(
        AuthStoreUnreadableError,
      );
      expect(mocks.openExternal).not.toHaveBeenCalled();
      await expect(readFile(storeFile())).resolves.toEqual(saved);
      await expect(getCloudSession(dataDir)).resolves.toMatchObject({
        user: { email: "person@example.com" },
      });
    });

    it("does not clear the login after a refresh the server refused while the file is unreadable", async () => {
      mocks.authenticateWithCode.mockResolvedValueOnce({
        accessToken: EXPIRED_ACCESS_TOKEN,
        refreshToken: "refresh_123",
        user: { id: "user_123", email: "person@example.com", name: "Person Example" },
      });
      await beginCloudSignIn(dataDir);
      await handleCloudDeepLink(
        "ao-app://callback?code=code_456&state=state_123",
        dataDir,
      );
      const saved = await readFile(storeFile());
      // The first read finds the expiring login; the one that would confirm
      // nothing replaced it before clearing it fails.
      mocks.authenticateWithRefreshToken.mockImplementationOnce(() => {
        mocks.storeReadFailures = ["EACCES"];
        return Promise.reject(new Error("refresh token already consumed"));
      });

      await expect(getCloudSession(dataDir)).rejects.toBeInstanceOf(
        AuthStoreUnreadableError,
      );
      await expect(readFile(storeFile())).resolves.toEqual(saved);
      expect(mocks.notifyRenderers).not.toHaveBeenCalledWith(null);
    });

    it("saves a refreshed login even when the read before saving it fails", async () => {
      mocks.authenticateWithCode.mockResolvedValueOnce({
        accessToken: EXPIRED_ACCESS_TOKEN,
        refreshToken: "refresh_123",
        user: { id: "user_123", email: "person@example.com", name: "Person Example" },
      });
      await beginCloudSignIn(dataDir);
      await handleCloudDeepLink(
        "ao-app://callback?code=code_456&state=state_123",
        dataDir,
      );
      // WorkOS rotates the token, using up the old one; the read that would
      // merge the new one into the file then fails.
      mocks.authenticateWithRefreshToken.mockImplementationOnce(() => {
        mocks.storeReadFailures = ["EACCES"];
        return Promise.resolve({
          accessToken: ACCESS_TOKEN,
          refreshToken: "refresh_456",
          user: { id: "user_123", email: "person@example.com", name: "Person Example" },
        });
      });

      await expect(getCloudSession(dataDir)).resolves.toMatchObject({
        user: { email: "person@example.com" },
      });
      expect(JSON.parse(await readFile(storeFile(), "utf8"))).toMatchObject({
        session: { accessToken: ACCESS_TOKEN, refreshToken: "refresh_456" },
        pkce: null,
      });
    });

    it("says why sign-in did not open when the login cannot be read", async () => {
      const signIn = mocks.ipcHandle.mock.calls.find(
        ([channel]) => channel === "cloud:signIn",
      )?.[1] as () => Promise<void>;
      mocks.storeReadFailures = ["EIO"];

      await expect(signIn()).resolves.toBeUndefined();

      expect(mocks.openExternal).not.toHaveBeenCalled();
      expect(mocks.showMessageBox).toHaveBeenCalledWith(expect.objectContaining({
        type: "error",
        detail: expect.stringContaining("could not read its saved sign-in"),
      }));
    });

    it("still signs out from the app menu when the login cannot be read", async () => {
      const signOut = mocks.ipcHandle.mock.calls.find(
        ([channel]) => channel === "cloud:signOut",
      )?.[1] as () => Promise<void>;
      mocks.storeReadFailures = ["EACCES"];

      await signOut();

      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });
      expect(mocks.notifyRenderers).toHaveBeenCalledWith(null);
      await expect(getCloudSession(dataDir)).resolves.toBeNull();
    });

    it("treats a missing login file as signed out without creating one", async () => {
      await rm(storeFile());

      await expect(getCloudSession(dataDir)).resolves.toBeNull();
      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });
    });

    it("removes a login file that reads but cannot be decoded", async () => {
      await writeFile(storeFile(), "not an encrypted store");

      await expect(getCloudSession(dataDir)).resolves.toBeNull();
      await expect(readFile(storeFile())).rejects.toMatchObject({ code: "ENOENT" });
    });
  });
});

// registerCloudProtocol decides from how the build was launched and where it
// signs in; the platform only decides whether a claim can be released (not on
// Linux), so each case pins process.platform.
describe("registerCloudProtocol", () => {
  const argv = process.argv;
  const defaultApp = Object.getOwnPropertyDescriptor(process, "defaultApp");
  const platform = Object.getOwnPropertyDescriptor(process, "platform")!;
  const devScript = path.resolve("/worktree/frontend");

  afterEach(async () => {
    vi.unstubAllEnvs();
    vi.resetModules();
    const { app } = await import("electron");
    (app as { isPackaged: boolean }).isPackaged = true;
    vi.mocked(app.isDefaultProtocolClient).mockReturnValue(false);
    process.argv = argv;
    if (defaultApp) Object.defineProperty(process, "defaultApp", defaultApp);
    else delete (process as { defaultApp?: boolean }).defaultApp;
    Object.defineProperty(process, "platform", platform);
  });

  // Loads cloud-auth fresh, since it picks its sign-in redirect once at import
  // from app.isPackaged and AO_CLOUD_AUTH_REDIRECT. The electron mock itself
  // survives resetModules, so its calls are cleared here. A dev build is
  // launched as `electron <app dir>`, which sets process.defaultApp; a
  // packaged build leaves it unset.
  async function load(options: {
    isPackaged: boolean;
    redirect?: string;
    ownsScheme?: boolean;
    platform?: NodeJS.Platform;
    argv?: string[];
  }) {
    vi.resetModules();
    vi.stubEnv("AO_CLOUD_AUTH_REDIRECT", options.redirect ?? "");
    if (options.isPackaged) delete (process as { defaultApp?: boolean }).defaultApp;
    else (process as { defaultApp?: boolean }).defaultApp = true;
    process.argv = options.argv ?? (options.isPackaged ? [process.execPath] : [process.execPath, devScript]);
    Object.defineProperty(process, "platform", { ...platform, value: options.platform ?? "darwin" });
    const { app } = await import("electron");
    vi.clearAllMocks();
    (app as { isPackaged: boolean }).isPackaged = options.isPackaged;
    vi.mocked(app.isDefaultProtocolClient).mockReturnValue(options.ownsScheme ?? false);
    const { registerCloudProtocol } = await import("./cloud-auth");
    return { app, registerCloudProtocol };
  }

  it("claims ao-app:// for the packaged app, and keeps it", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: true });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app");
    expect(app.removeAsDefaultProtocolClient).not.toHaveBeenCalled();
    expect(app.once).not.toHaveBeenCalled();
  });

  it("claims ao-app:// for a packaged app signing in through a loopback override", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: true, redirect: "http://127.0.0.1:3000/callback" });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app");
    expect(app.removeAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it("claims the bare scheme for a packaged app launched with a deep link", async () => {
    // Windows and Linux start the installed app with the link as argv[1].
    const { app, registerCloudProtocol } = await load({
      isPackaged: true,
      argv: [process.execPath, "ao-app://callback?code=c&state=s"],
      platform: "win32",
    });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app");
  });

  it("keeps a loopback dev build from claiming ao-app://", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
    expect(app.removeAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it.each(["darwin", "win32"] as const)("hands ao-app:// back on %s when an earlier dev run took it", async (os) => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false, ownsScheme: true, platform: os });
    registerCloudProtocol();
    // Windows only matches (and removes) the registration for this exact
    // executable and script.
    expect(app.isDefaultProtocolClient).toHaveBeenCalledWith("ao-app", process.execPath, [devScript]);
    expect(app.removeAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app", process.execPath, [devScript]);
    expect(app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it("leaves the scheme alone for a loopback dev build on Linux, which cannot release it", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false, ownsScheme: true, platform: "linux" });
    registerCloudProtocol();
    expect(app.isDefaultProtocolClient).not.toHaveBeenCalled();
    expect(app.removeAsDefaultProtocolClient).not.toHaveBeenCalled();
    expect(app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it("releases with the bare scheme when a dev build has no app path", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false, ownsScheme: true, argv: [process.execPath] });
    expect(() => registerCloudProtocol()).not.toThrow();
    expect(app.isDefaultProtocolClient).toHaveBeenCalledWith("ao-app");
    expect(app.removeAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app");
  });

  it("treats a localhost override like the loopback default", async () => {
    const { app, registerCloudProtocol } = await load({
      isPackaged: false,
      redirect: "http://localhost:4000/callback",
    });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it.each([
    ["a bounce page", "https://staging.aoagents.dev/app/auth/return"],
    ["the deep link itself", "ao-app://callback"],
  ])("claims ao-app:// for a dev build signing in through %s, until it quits", async (_name, redirect) => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false, redirect, ownsScheme: true });
    registerCloudProtocol();
    // Windows launches this checkout's script with the dev Electron binary.
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app", process.execPath, [devScript]);
    expect(app.removeAsDefaultProtocolClient).not.toHaveBeenCalled();
    expect(app.once).toHaveBeenCalledWith("will-quit", expect.any(Function));
    const onQuit = vi.mocked(app.once).mock.calls[0][1] as () => void;
    onQuit();
    expect(app.removeAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app", process.execPath, [devScript]);
  });

  it("claims for a deep-link dev build on Linux without a release on quit", async () => {
    const { app, registerCloudProtocol } = await load({ isPackaged: false, redirect: "ao-app://callback", platform: "linux" });
    registerCloudProtocol();
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao-app", process.execPath, [devScript]);
    expect(app.once).not.toHaveBeenCalled();
  });
});

describe("resolveCloudAuthDir", () => {
  it("keeps the sign-in in the configured data dir, so instances with their own data dir never share an account", () => {
    const home = "/Users/someone";
    const first = resolveCloudAuthDir({ AO_DATA_DIR: "/tmp/ao/alice" }, home, "/work", false);
    const second = resolveCloudAuthDir({ AO_DATA_DIR: "/tmp/ao/bob" }, home, "/work", false);
    expect(first).toBe("/tmp/ao/alice");
    expect(second).toBe("/tmp/ao/bob");
    expect(resolveCloudAuthDir({ AO_DATA_DIR: " relative/data " }, home, "/work", true)).toBe("/work/relative/data");
  });

  it("uses ~/.ao for a packaged build and ~/.ao/dev for a development build", () => {
    expect(resolveCloudAuthDir({}, "/Users/someone", "/work", true)).toBe("/Users/someone/.ao");
    expect(resolveCloudAuthDir({ AO_DATA_DIR: "  " }, "/Users/someone", "/work", false)).toBe("/Users/someone/.ao/dev");
  });
});

describe("the sign-in redirect each build sends", () => {
  const PRODUCTION_CLIENT = "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X";
  // The baked default, which belongs to the staging WorkOS environment.
  const STAGING_CLIENT = "client_01KZ3VRKC374HS91XGRDPT3671";
  const WEBSITE_PAGE = "https://orchestrator.inc/app/auth/return/";
  const CONTROL_PLANE_PAGE = "https://api.aoagents.dev/app/auth/return";
  const LOOPBACK = "http://127.0.0.1:3000/callback";

  it.each([
    ["a release build (stable, nightly, preview) on the production client", true, PRODUCTION_CLIENT, WEBSITE_PAGE],
    ["a packaged build on the baked staging client", true, STAGING_CLIENT, CONTROL_PLANE_PAGE],
    ["a packaged build on any other client", true, "client_someone_else", CONTROL_PLANE_PAGE],
    ["a development build on the production client", false, PRODUCTION_CLIENT, LOOPBACK],
    ["a development build on the baked staging client", false, STAGING_CLIENT, LOOPBACK],
  ])("%s", (_name, isPackaged, clientId, redirect) => {
    expect(resolveCloudAuthRedirect({}, isPackaged, clientId)).toBe(redirect);
    // The control plane a build talks to plays no part in it.
    expect(resolveCloudAuthRedirect({ AO_CLOUD_CONTROL_PLANE_URL: "https://staging-api.aoagents.dev" }, isPackaged, clientId)).toBe(redirect);
  });

  it.each([true, false])("lets AO_CLOUD_AUTH_REDIRECT override every default (packaged: %s)", (isPackaged) => {
    for (const clientId of [PRODUCTION_CLIENT, STAGING_CLIENT]) {
      expect(resolveCloudAuthRedirect(
        { AO_CLOUD_AUTH_REDIRECT: " https://staging-api.aoagents.dev/app/auth/return " },
        isPackaged,
        clientId,
      )).toBe("https://staging-api.aoagents.dev/app/auth/return");
    }
    expect(resolveCloudAuthRedirect({ AO_CLOUD_AUTH_REDIRECT: "   " }, isPackaged, PRODUCTION_CLIENT)).toBe(isPackaged ? WEBSITE_PAGE : LOOPBACK);
  });

  describe("at sign-in", () => {
    let dataDir: string;

    beforeEach(async () => {
      dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-cloud-redirect-"));
    });

    afterEach(async () => {
      vi.unstubAllEnvs();
      vi.resetModules();
      await rm(dataDir, { recursive: true, force: true });
    });

    // cloud-auth picks its client and redirect once at import.
    async function startSignIn(clientId: string) {
      vi.resetModules();
      vi.stubEnv("VITE_WORKOS_CLIENT_ID", clientId);
      vi.stubEnv("AO_CLOUD_AUTH_REDIRECT", "");
      mocks.getAuthorizationUrlWithPKCE.mockReset().mockResolvedValue({
        url: "https://workos.example/authorize",
        state: "state_123",
        codeVerifier: "verifier_123",
      });
      const { beginCloudSignIn: begin } = await import("./cloud-auth");
      await begin(dataDir);
      return mocks.getAuthorizationUrlWithPKCE.mock.calls[0]?.[0];
    }

    it("sends the website page with the production client", async () => {
      await expect(startSignIn(PRODUCTION_CLIENT)).resolves.toMatchObject({
        clientId: PRODUCTION_CLIENT,
        redirectUri: WEBSITE_PAGE,
      });
    });

    it("keeps the control plane page with the staging client, which staging WorkOS accepts", async () => {
      await expect(startSignIn(STAGING_CLIENT)).resolves.toMatchObject({
        clientId: STAGING_CLIENT,
        redirectUri: CONTROL_PLANE_PAGE,
      });
    });
  });
});

describe("cloud:signOut", () => {
  it("finishes the signed-out hook before telling the renderers", async () => {
    const dataDir = await mkdtemp(path.join(os.tmpdir(), "ao-cloud-auth-"));
    const order: string[] = [];
    mocks.ipcHandle.mockClear();
    mocks.notifyRenderers.mockImplementation(() => order.push("notified"));
    installCloudIPC(() => dataDir, mocks.notifyRenderers, async () => {
      await new Promise((resolve) => setTimeout(resolve, 5));
      order.push("telemetry retired");
    });
    const handler = mocks.ipcHandle.mock.calls.find(([channel]) => channel === "cloud:signOut")?.[1] as () => Promise<void>;
    await handler();
    expect(order).toEqual(["telemetry retired", "notified"]);
    await rm(dataDir, { recursive: true, force: true });
  });
});

describe("cloud:signOut with telemetry", () => {
  it("retires a handed-off account's telemetry binding before renderers are told, with the daemon down", async () => {
    const { existsSync } = await import("node:fs");
    const { readFile, writeFile } = await import("node:fs/promises");
    const { TelemetryIdentityController } = await import("./telemetry-identity");
    const telemetryDir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
    const authDir = await mkdtemp(path.join(os.tmpdir(), "ao-cloud-auth-"));
    let daemonUp = true;
    const fetcher = vi.fn(async () => {
      if (!daemonUp) throw new Error("connection refused");
      return new Response(null, { status: 204 });
    });
    const ctl = new TelemetryIdentityController(telemetryDir, () => "http://127.0.0.1:4010", fetcher as never);
    // A normal signed-in hand-off: the daemon recorded user_A.
    expect(await ctl.setCloudUser("user_A")).toBe(true);
    await writeFile(path.join(telemetryDir, "telemetry_bound_user"), "user_A\n");
    await writeFile(path.join(telemetryDir, "telemetry_anonymous_id"), "anon_linked\n");
    daemonUp = false;

    const bindingAtNotify: boolean[] = [];
    mocks.ipcHandle.mockClear();
    installCloudIPC(() => authDir, () => {
      bindingAtNotify.push(existsSync(path.join(telemetryDir, "telemetry_bound_user")));
    }, () => ctl.signOut());
    const handler = mocks.ipcHandle.mock.calls.find(([channel]) => channel === "cloud:signOut")?.[1] as () => Promise<void>;
    await handler();

    expect(bindingAtNotify).toEqual([false]);
    expect((await readFile(path.join(telemetryDir, "telemetry_anonymous_id"), "utf8")).trim()).not.toBe("anon_linked");
    await rm(telemetryDir, { recursive: true, force: true });
    await rm(authDir, { recursive: true, force: true });
  });
});
