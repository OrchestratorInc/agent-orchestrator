import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
}));

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
  beginCloudSignIn,
  getCloudAccessToken,
  getCloudSession,
  handleCloudDeepLink,
  installCloudIPC,
  readAuthStore,
  showCloudSignInFailure,
  signOutCloud,
  writeAuthStore,
} from "./cloud-auth";

describe("native WorkOS authentication", () => {
  let dataDir: string;

  beforeEach(async () => {
    vi.clearAllMocks();
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

    const session = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );
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
    const account = await handleCloudDeepLink(callback, dataDir);

    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual(account);
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

    expect(accounts[0]).toEqual(accounts[1]);
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("ignores callbacks with no login pending without changing the signed-in account", async () => {
    await beginCloudSignIn(dataDir);
    const account = await handleCloudDeepLink("ao-app://callback?code=code_123&state=state_123", dataDir);
    await expect(handleCloudDeepLink(
      "ao-app://callback?code=different_code&state=state_123", dataDir,
    )).resolves.toEqual(account);
    await expect(handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=unverified_state", dataDir,
    )).resolves.toEqual(account);
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(1);
  });

  it("quietly returns to an account saved before callback deduplication was added", async () => {
    await beginCloudSignIn(dataDir);
    const callback = "ao-app://callback?code=code_123&state=state_123";
    const account = await handleCloudDeepLink(callback, dataDir);
    const store = await readAuthStore(dataDir);
    delete store.completedCallback;
    await writeAuthStore(dataDir, store);

    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual(account);
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
    const account = await handleCloudDeepLink(callback, dataDir);
    mocks.getAuthorizationUrlWithPKCE.mockResolvedValueOnce({
      url: "https://workos.example/authorize", state: "state_456", codeVerifier: "verifier_456",
    });
    await beginCloudSignIn(dataDir);

    await expect(handleCloudDeepLink(callback, dataDir)).resolves.toEqual(account);
    await handleCloudDeepLink("ao-app://callback?code=code_456&state=state_456", dataDir);
    expect(mocks.authenticateWithCode).toHaveBeenCalledTimes(2);
    expect(mocks.authenticateWithCode).toHaveBeenLastCalledWith(expect.objectContaining({
      code: "code_456", codeVerifier: "verifier_456",
    }));
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
    ).rejects.toThrow("Sign in to AO Cloud before connecting a provider.");
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
      await expect(connect()).rejects.toThrow("AO Cloud could not save the provider credential.");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });
    });

    it("does not leave a local token behind when the cloud is unreachable", async () => {
      vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("fetch failed"));
      await expect(connect()).rejects.toThrow("fetch failed");
      await expect(readFile(tokenFile(), "utf8")).rejects.toMatchObject({ code: "ENOENT" });
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
    const account = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    expect(account?.user.email).toBe("person@example.com");
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
    const account = await handleCloudDeepLink(
      "ao-app://callback?code=code_123&state=state_123",
      dataDir,
    );

    expect(account?.user.email).toBe("person@example.com");
    await expect(
      readFile(path.join(dataDir, "cloud-auth.bin")),
    ).rejects.toMatchObject({ code: "ENOENT" });
    await expect(getCloudSession(dataDir)).resolves.toMatchObject({
      user: { email: "person@example.com" },
    });
    await signOutCloud(dataDir);
  });

  it("shows a bounded error when the callback cannot be completed", async () => {
    await showCloudSignInFailure(
      new Error("The WorkOS sign-in request expired with secret details"),
    );

    expect(mocks.showMessageBox).toHaveBeenCalledWith({
      type: "error",
      title: "AO Cloud sign-in failed",
      message: "Unable to sign in to AO Cloud",
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
