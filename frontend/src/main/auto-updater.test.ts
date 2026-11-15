// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import { EventEmitter } from "node:events";
import semver from "semver";
import { CancellationToken } from "builder-util-runtime";
import nodePath from "node:path";
import { requestDesktopShutdown } from "./desktop-shutdown";

type UpdateSettings = {
  enabled: boolean;
  channel: "latest" | "nightly";
  nightlyAck: boolean;
  feature: { pr: number } | null;
  macDifferentialUpdates?: boolean;
};

type UpdateSettingsReader = ReturnType<
  typeof vi.fn<() => Promise<UpdateSettings>>
>;
type UpdaterEventHandler = (...args: any[]) => void;

type ImportOptions = {
  nativeReadyManually?: boolean;
  reconcileFeaturePin?: (
    settings: UpdateSettings,
  ) => Promise<{ settings: UpdateSettings; cleared: boolean }>;
  isPackaged?: boolean;
  rolloutReady?: boolean;
  version?: string;
};

type AutoUpdaterMock = {
  on: ReturnType<typeof vi.fn>;
  checkForUpdates: ReturnType<typeof vi.fn>;
  downloadUpdate: ReturnType<typeof vi.fn>;
  quitAndInstall: ReturnType<typeof vi.fn>;
  setFeedURL: ReturnType<typeof vi.fn>;
  channel: string;
  allowPrerelease: boolean;
  allowDowngrade: boolean;
  autoDownload: boolean;
  autoInstallOnAppQuit: boolean;
  disableDifferentialDownload: boolean;
  logger: {
    info: (message: unknown, ...args: unknown[]) => void;
    warn: (message: unknown, ...args: unknown[]) => void;
    error: (message: unknown, ...args: unknown[]) => void;
    debug: (message: unknown, ...args: unknown[]) => void;
  };
  httpExecutor: { request: ReturnType<typeof vi.fn<(options: object, token?: CancellationToken) => Promise<string | null>>> };
  downloadedUpdateHelper: { clear: ReturnType<typeof vi.fn> };
};

function createAutoUpdaterMock(): AutoUpdaterMock {
  return {
    on: vi.fn(),
    checkForUpdates: vi.fn(() => Promise.resolve()),
    downloadUpdate: vi.fn(() => Promise.resolve()),
    quitAndInstall: vi.fn(),
    setFeedURL: vi.fn(),
    channel: "",
    allowPrerelease: false,
    allowDowngrade: false,
    autoDownload: false,
    autoInstallOnAppQuit: false,
    disableDifferentialDownload: false,
    logger: {
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
      debug: vi.fn(),
    },
    httpExecutor: { request: vi.fn(async () => null) },
    downloadedUpdateHelper: { clear: vi.fn(() => Promise.resolve()) },
  };
}

let stateDir = "";
const hostPlatform = Object.getOwnPropertyDescriptor(process, "platform")!;
beforeEach(() => {
  Object.defineProperty(process, "platform", { value: "linux" });
  stateDir = mkdtempSync(nodePath.join(os.tmpdir(), "ao-updater-state-"));
});
afterEach(() => {
  Object.defineProperty(process, "platform", hostPlatform);
  rmSync(stateDir, { recursive: true, force: true, maxRetries: 10, retryDelay: 25 });
});

const importAutoUpdaterKeepingStagedFile = importAutoUpdater;
describe("macOS differential update policy", () => {
  let restorePlatform: () => void;
  beforeEach(() => { restorePlatform = stubProcess("darwin", process.execPath); });
  afterEach(() => { restorePlatform(); vi.restoreAllMocks(); });
  const nightly: UpdateSettings = { enabled: true, channel: "nightly", nightlyAck: true, feature: null, macDifferentialUpdates: true };

  it.each(["win32", "linux"] as const)("preserves %s differential policy across updater operations", async platform => {
    const restore = stubProcess(platform, process.execPath);
    try {
      const { module, autoUpdater } = await importAutoUpdater(nightly);
      expect(autoUpdater.disableDifferentialDownload).toBe(false);
      for (const disabled of [false, true]) {
        autoUpdater.disableDifferentialDownload = disabled;
        await module.setMacDifferentialUpdates(stateDir, true);
        await module.startAutoUpdates(stateDir);
        await module.checkForUpdatesNow(stateDir);
        await module.downloadUpdateNow();
        await module.setMacDifferentialUpdates(stateDir, false);
        expect(autoUpdater.disableDifferentialDownload).toBe(disabled);
      }
    } finally { restore(); }
  });

  it("keeps persisted opt-in disabled until renderer hydration", async () => {
    const { module, autoUpdater } = await importAutoUpdater(nightly);
    await module.startAutoUpdates(stateDir);
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    await module.setMacDifferentialUpdates(stateDir, true);
    expect(autoUpdater.disableDifferentialDownload).toBe(false);
  });

  it("preserves the Developer Mode mirror across stale settings writes and checks", async () => {
    const { module, autoUpdater, readUpdateSettings } = await importAutoUpdater(nightly);
    await module.setMacDifferentialUpdates(stateDir, true);
    await module.setUpdateSettings(stateDir, { ...nightly, macDifferentialUpdates: false });
    expect((await readUpdateSettings()).macDifferentialUpdates).toBe(true);
    await module.setMacDifferentialUpdates(stateDir, false);
    await module.checkForUpdatesNow(stateDir, { settings: nightly });
    expect((await readUpdateSettings()).macDifferentialUpdates).toBe(false);
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
  });

  it("re-applies policy for automatic, manual, pinned and return-home operations", async () => {
    const { module, autoUpdater } = await importAutoUpdater(nightly);
    await module.setMacDifferentialUpdates(stateDir, true);
    autoUpdater.disableDifferentialDownload = true;
    await module.startAutoUpdates(stateDir);
    expect(autoUpdater.disableDifferentialDownload).toBe(false);
    await module.setUpdateSettings(stateDir, { ...nightly, channel: "latest" });
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    await module.checkForUpdatesNow(stateDir, { settings: { ...nightly, feature: { pr: 3288 } } });
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    await module.returnToHome(stateDir);
    expect(autoUpdater.disableDifferentialDownload).toBe(false);
    autoUpdater.disableDifferentialDownload = true;
    await module.downloadUpdateNow();
    expect(autoUpdater.disableDifferentialDownload).toBe(false);
  });

  it("disables immediately while an updater operation is still in flight", async () => {
    const { module, autoUpdater, updaterEvents, telemetryMessages } = await importAutoUpdater(nightly);
    await module.setMacDifferentialUpdates(stateDir, true);
    const blocked = deferred();
    autoUpdater.checkForUpdates.mockReturnValueOnce(blocked.promise);
    const check = module.checkForUpdatesNow(stateDir);
    await flushMicrotasks();
    updaterEvents.get("update-available")?.({ version: "2.0.0" });
    const off = module.setMacDifferentialUpdates(stateDir, false);
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    expect(telemetryMessages().at(-1)?.payload).toMatchObject({ differential_eligible: true });
    blocked.resolve();
    await Promise.all([check, off]);
    await module.downloadUpdateNow();
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
  });

  it("omits unavailable progress metrics and sanitizes dependency logs", async () => {
    const { module, autoUpdater, updaterEvents, statusMessages, telemetryMessages } = await importAutoUpdater(nightly);
    const base = autoUpdater.logger;
    await module.checkForUpdatesNow(stateDir);
    autoUpdater.logger.info("Download block maps (old: https://user:secret@host/old?token=secret)");
    autoUpdater.logger.error("Cannot download differentially, fallback to full download: https://host?token=secret");
    updaterEvents.get("download-progress")?.({ percent: 10 });
    expect(statusMessages().at(-1)?.payload).not.toHaveProperty("transferred");
    expect(statusMessages().at(-1)?.payload).not.toHaveProperty("total");
    expect(statusMessages().at(-1)?.payload).not.toHaveProperty("bytesPerSecond");
    updaterEvents.get("error")?.(new Error("checksum mismatch"));
    expect(telemetryMessages().at(-1)?.payload).toMatchObject({ transfer_mode: "differential", fallback: true });
    expect(JSON.stringify(base)).not.toContain("secret");
    expect(JSON.stringify([vi.mocked(base.info).mock.calls, vi.mocked(base.error).mock.calls, vi.mocked(base.warn).mock.calls])).not.toContain("secret");
  });

  it("keeps production downloads full-only even after Developer Mode hydration", async () => {
    const production = await vi.importActual<{ default: { enabled: boolean } }>("../../scripts/mac-differential-rollout.json");
    expect(production.default.enabled).toBe(false);
    const { module, autoUpdater } = await importAutoUpdater(nightly, { rolloutReady: production.default.enabled });
    const stockLogger = autoUpdater.logger;
    await module.setMacDifferentialUpdates(stateDir, true);
    await module.checkForUpdatesNow(stateDir);
    await module.downloadUpdateNow();
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    expect(autoUpdater.logger).toBe(stockLogger);
  });

  it("starts fail-closed before settings hydration", async () => {
    const { autoUpdater } = await importAutoUpdater();

    expect(autoUpdater.disableDifferentialDownload).toBe(true);
  });

  it("reports differential fallback and real transfer progress without signed URLs", async () => {
    const { module, autoUpdater, updaterEvents, telemetryMessages, statusMessages } =
      await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
        macDifferentialUpdates: true,
      });
    await module.setMacDifferentialUpdates(stateDir, true);
    module.applyUpdaterPolicy(
      {
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
        macDifferentialUpdates: true,
      },
      "darwin",
    );
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-available")?.({
      version: "1.2.3",
      files: [
        { url: "AO-darwin-arm64.zip", size: 1000 },
        { url: "AO-darwin-x64.zip", size: 1000 },
      ],
    });
    autoUpdater.logger.info("Differential download: https://example.test/AO.zip?token=secret");
    autoUpdater.logger.error("Cannot download differentially, fallback to full download: checksum mismatch");
    updaterEvents.get("download-progress")?.({
      percent: 25,
      transferred: 250,
      total: 1000,
      bytesPerSecond: 125,
    });
    updaterEvents.get("update-downloaded")?.({ version: "1.2.3" });

    expect(statusMessages().map(message => message.payload)).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          state: "downloading",
          transferred: 250,
          total: 1000,
          bytesPerSecond: 125,
        }),
      ]),
    );
    expect(telemetryMessages().at(-1)?.payload).toMatchObject({
      event: "ao.updates.downloaded",
      transfer_mode: "differential",
      fallback: true,
      transferred_bytes: 250,
      target_bytes: 1000,
      to_version: "1.2.3",
    });
    expect(JSON.stringify(telemetryMessages())).not.toContain("secret");
  });

  it("enables only macOS Nightly Developer Mode without a feature pin", async () => {
    const { module, autoUpdater } = await importAutoUpdater();

    await module.setMacDifferentialUpdates(stateDir, true);
    module.applyUpdaterPolicy(
      {
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
        macDifferentialUpdates: true,
      },
      "darwin",
    );
    expect(autoUpdater.disableDifferentialDownload).toBe(false);

    module.applyUpdaterPolicy(
      {
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: { pr: 3288 },
        macDifferentialUpdates: true,
      },
      "darwin",
    );
    expect(autoUpdater.disableDifferentialDownload).toBe(true);
  });

  it("re-applies fail-closed policy before a manual download", async () => {
    const { module, autoUpdater } = await importAutoUpdater({
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: null,
      macDifferentialUpdates: true,
    });
    await module.checkForUpdatesNow(stateDir);
    autoUpdater.disableDifferentialDownload = false;

    await module.downloadUpdateNow();

    expect(autoUpdater.disableDifferentialDownload).toBe(true);
    expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
  });
});

async function importAutoUpdater(
  settings: UpdateSettings | UpdateSettingsReader = {
    enabled: true,
    channel: "latest",
    nightlyAck: false,
    feature: null,
  },
  options: ImportOptions = {},
) {
  vi.resetModules();
  const updaterEvents = new Map<string, UpdaterEventHandler>();
  const autoUpdater = createAutoUpdaterMock();
  const powerMonitor = new EventEmitter();
  const nativeUpdaterEvents = new Map<string, UpdaterEventHandler>();
  const nativeAutoUpdater = new EventEmitter();
  const nativeEmitterOn = nativeAutoUpdater.on.bind(nativeAutoUpdater);
  (nativeAutoUpdater as unknown as { on: (event: string, handler: UpdaterEventHandler) => unknown }).on = (
    event,
    handler,
  ) => {
    nativeUpdaterEvents.set(event, handler);
    return nativeEmitterOn(event, handler);
  };
  const startMacUpdateProgress = vi.fn(async () => ({ assertAlive: vi.fn(), fail: vi.fn(async () => undefined) }));
  vi.doMock("./mac-update-progress", () => ({ startMacUpdateProgress }));
  autoUpdater.on.mockImplementation(
    (event: string, handler: UpdaterEventHandler) => {
      updaterEvents.set(event, (...args: any[]) => {
        handler(...args);
        if (event === "update-downloaded" && !options.nativeReadyManually) nativeAutoUpdater.emit("update-downloaded");
      });
      return autoUpdater;
    },
  );
  const dialog = {
    showMessageBox: vi.fn(),
  };
  const sent: { channel: string; payload: unknown }[] = [];
  const rendererSend = vi.fn((channel: string, payload: unknown) => {
    sent.push({ channel, payload });
  });
  const BrowserWindow = {
    getAllWindows: vi.fn(() => [] as unknown[]),
  };
  const statusMessages = () => sent.filter((m) => m.channel === "updates:status");
  const telemetryMessages = () => sent.filter((m) => m.channel === "updates:telemetry");
  vi.doMock("../../scripts/mac-differential-rollout.json", () => ({ default: { enabled: options.rolloutReady ?? true } }));
  vi.doMock("./mac-differential-v2-updater", () => ({
    MacDifferentialV2Updater: class { constructor() { return autoUpdater; } },
  }));
  vi.doMock("electron-updater", () => ({ autoUpdater }));
  vi.doMock("electron", () => ({
    powerMonitor,
    autoUpdater: nativeAutoUpdater,
    app: {
      isPackaged: options.isPackaged ?? true,
      getVersion: () => options.version ?? "1.0.0",
    },
    BrowserWindow,
    dialog,
  }));
  let persisted = typeof settings === "function" ? undefined : settings;
  const readUpdateSettings =
    typeof settings === "function"
      ? settings
      : vi.fn(() => Promise.resolve(persisted!));
  const writeUpdateSettings = vi.fn<
    (_stateDir: string, settings: UpdateSettings) => Promise<void>
  >(async (_dir, next) => { persisted = next; });
  const updateUpdateSettings = vi.fn(
    async (
      _stateDir: string,
      update: (
        current: UpdateSettings,
      ) => UpdateSettings | Promise<UpdateSettings>,
    ) => {
      const current = await readUpdateSettings();
      const next = await update(current);
      if (next !== current) await writeUpdateSettings(_stateDir, next);
      return next;
    },
  );
  vi.doMock("./update-settings", () => ({
    readUpdateSettings,
    writeUpdateSettings,
    updateUpdateSettings,
    UPDATE_SETTINGS_FILE_NAME: "update-settings.json",
    macDifferentialUpdatesEnabled: ({ platform, settings }: {
      platform: NodeJS.Platform;
      settings: UpdateSettings;
    }) => platform === "darwin" && settings.channel === "nightly" && settings.feature === null && settings.macDifferentialUpdates === true,
  }));
  vi.doMock("./feature-builds", () => ({
    reconcileFeaturePin:
      options.reconcileFeaturePin ??
      ((current: UpdateSettings) =>
        Promise.resolve({ settings: current, cleared: false })),
  }));
  const module = await import("./auto-updater");
  module.setRendererSink(() => ({ send: rendererSend }));
  module.__setStagingProbesForTesting({ stagingDiskIsFull: () => false });
  return {
    powerMonitor,
    nativeAutoUpdater,
    startMacUpdateProgress,
    sent,
    rendererSend,
    statusMessages,
    telemetryMessages,
    module,
    autoUpdater,
    dialog,
    BrowserWindow,
    updaterEvents,
    nativeUpdaterEvents,
    readUpdateSettings,
    writeUpdateSettings,
    updateUpdateSettings,
  };
}

function latestInterval(setIntervalSpy: ReturnType<typeof vi.spyOn>): {
  callback: () => void;
  delay: number;
  timer: ReturnType<typeof setInterval>;
} {
  const calls = setIntervalSpy.mock.calls;
  expect(calls.length).toBeGreaterThan(0);
  const [callback, delay] = calls.at(-1) ?? [];
  expect(typeof callback).toBe("function");
  expect(typeof delay).toBe("number");
  const results = setIntervalSpy.mock.results;
  const timer = results.at(-1)?.value as ReturnType<typeof setInterval>;
  return { callback: callback as () => void, delay: delay as number, timer };
}

function intervalWithDelay(
  setIntervalSpy: ReturnType<typeof vi.spyOn>,
  delay: number,
): () => void {
  const calls = setIntervalSpy.mock.calls as Array<[() => void, number]>;
  const call = calls.find(([, candidateDelay]) => candidateDelay === delay);
  expect(call).toBeDefined();
  return call?.[0] as () => void;
}

function deferred<T = void>(): {
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
} {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

async function flushMicrotasks(turns = 16): Promise<void> {
  for (let i = 0; i < turns; i += 1) {
    await Promise.resolve();
  }
}

function releasesFeedTimeout(): Error {
  const feed = "https://github.com/AgentWrapper/agent-orchestrator/releases/latest";
  const err = new Error(
    `Cannot parse releases feed: Error: Unable to find latest version on GitHub (${feed}), please ensure a production release exists: HttpError: 504 Gateway Time-out\n` +
      `"method: GET url: ${feed}\\n\\n<html><body>Gateway timeout</body></html>\\n"\n` +
      'Headers: {\n  "set-cookie": "_gh_sess=abc123; path=/; secure; HttpOnly"\n}\n' +
      "    at createHttpError (C:\\AO\\resources\\app.asar\\node_modules\\builder-util-runtime\\out\\httpExecutor.js:53:12),\n" +
      "XML:\n<feed></feed>",
  );
  return Object.assign(err, { code: "ERR_UPDATER_INVALID_RELEASE_FEED" });
}

describe("startAutoUpdates", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("recovers a macOS ShipIt missing-file failure instead of retaining a ready update", async () => {
    const restore = stubProcess("darwin", "/usr/local/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater } = await importAutoUpdater({ enabled: false, channel: "latest", nightlyAck: false, feature: null }, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      await flushMicrotasks();
      const failure = new Error(
        "ditto: Could not lstat /Users/test/Library/Caches/dev.agent-orchestrator.desktop.ShipIt/update.abc/Agent Orchestrator.app/Contents/Resources/acp-runtime/node_modules/.bin/node-which: No such file or directory",
      );
      updaterEvents.get("error")?.(failure);
      nativeAutoUpdater.emit("error", failure);
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: expect.stringContaining("prepare the update") });
      expect(module.getUpdateStatus().staged).toBeUndefined();
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Check for updates/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" } });
      await module.checkForUpdatesNow(stateDir);
      expect(module.getUpdateStatus().state).toBe("available");
      autoUpdater.downloadUpdate.mockImplementation(async () => {
        updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      });
      const retry = module.downloadUpdateNow();
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      nativeAutoUpdater.emit("update-downloaded");
      await retry;
      expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
      await module.quitAndInstallUpdate();
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally {
      restore();
    }
  });

  it("keeps native preparation failures visible during an automatic check and retries next time", async () => {
    const restore = stubProcess("darwin", "/usr/local/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      const error = new Error("ditto: /cache/app.ShipIt/update.abc/AO.app/Contents/Resources/._app.asar__: No such file or directory");
      autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
        updaterEvents.get("checking-for-update")?.();
        updaterEvents.get("error")?.(error);
        throw error;
      });
      await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: expect.stringContaining("prepare the update") });
      expect(module.getUpdateStatus().staged).toBeUndefined();
      await new Promise((resolve) => setTimeout(resolve, 50));
      const second = await importAutoUpdaterKeepingStagedFile();
      await second.module.startAutoUpdates(stateDir);
      expect(second.module.getUpdateStatus().staged).toBeUndefined();
      expect(second.autoUpdater.autoDownload).toBe(true);
    } finally {
      restore();
    }
  });

  it("handles a reject-only native failure before AO has recorded a staged build", async () => {
    const restore = stubProcess("darwin", "/usr/local/bin/node");
    try {
      const { module, autoUpdater } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      autoUpdater.downloadUpdate.mockRejectedValue(new Error(
        "ditto: /cache/app.ShipIt/update.abc/AO.app/Contents/Resources/._app.asar__: No such file or directory",
      ));
      await module.downloadUpdateNow("failed-download");
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", requestId: "failed-download", message: expect.stringContaining("prepare the update") });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Check for updates/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore();
    }
  });

  it("does not let an in-flight escalation check restore a failed installation", async () => {
    const restore = stubProcess("darwin", "/usr/local/bin/node");
    try {
      const { module, updaterEvents, readUpdateSettings } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      const pendingSettings = deferred<UpdateSettings>();
      readUpdateSettings.mockReturnValueOnce(pendingSettings.promise);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      updaterEvents.get("error")?.(new Error(
        "ditto: /cache/app.ShipIt/update.abc/AO.app/Contents/Resources/._app.asar__: No such file or directory",
      ));
      pendingSettings.resolve({ enabled: true, channel: "latest", nightlyAck: false, feature: null });
      await flushMicrotasks();
      expect(module.getUpdateStatus().state).toBe("error");
      expect(module.getUpdateStatus().staged).toBeUndefined();
    } finally {
      restore();
    }
  });

  it("preserves replacement provenance when staging immediately follows a failure", async () => {
    const restore = stubProcess("darwin", "/usr/local/bin/node");
    try {
      const { module, updaterEvents } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      updaterEvents.get("error")?.(new Error(
        "ditto: /cache/app.ShipIt/update.abc/AO.app/Contents/Resources/._app.asar__: No such file or directory",
      ));
      updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
      await new Promise((resolve) => setTimeout(resolve, 50));
      const second = await importAutoUpdaterKeepingStagedFile();
      await second.module.startAutoUpdates(stateDir);
      expect(second.module.getUpdateStatus().staged?.version).toBe("2.2.0");
    } finally {
      restore();
    }
  });

  it.each(["download", "check", "return-home"])("allows retry after the previous %s rejects", async (operation) => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      const failure = new Error("ditto: /cache/app.ShipIt/update.abc/AO.app/Contents/Resources/._app.asar__: No such file or directory");
      autoUpdater.downloadUpdate.mockImplementation(async () => {
        updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
      });
      if (operation === "download") autoUpdater.downloadUpdate.mockRejectedValueOnce(failure);
      else autoUpdater.checkForUpdates.mockRejectedValueOnce(failure);
      const failed = operation === "download" ? module.downloadUpdateNow("failed")
        : operation === "check" ? module.checkForUpdatesNow(stateDir, { requestId: "failed" })
        : module.returnToHome(stateDir, "failed");
      await failed;
      await module.downloadUpdateNow("retry");
      expect(module.getUpdateStatus().staged?.version).toBe("2.2.0");
      expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
    } finally { restore(); }
  });

  it("does not invalidate a staged update for an unrelated missing file", async () => {
    const { module, updaterEvents } = await importAutoUpdater();
    await module.startAutoUpdates(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    await flushMicrotasks();
    updaterEvents.get("error")?.(new Error("ENOENT: no such file or directory, open /tmp/download.zip"));
    expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
  });

  it("runs the automatic updater check immediately on launch", async () => {
    const { module, autoUpdater } = await importAutoUpdater();

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(true);
    expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
  });

  it("keeps stable automatic checks on the 15-minute cadence", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, autoUpdater } = await importAutoUpdater();

    await module.startAutoUpdates(stateDir);
    const { delay } = latestInterval(setIntervalSpy);

    expect(delay).toBe(15 * 60 * 1000);
    await vi.advanceTimersByTimeAsync(delay - 1);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it("rechecks the nightly channel every 15 minutes", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, autoUpdater } = await importAutoUpdater({
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.startAutoUpdates(stateDir);
    const { delay } = latestInterval(setIntervalSpy);

    expect(delay).toBe(15 * 60 * 1000);
    await vi.advanceTimersByTimeAsync(delay - 1);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it.each(["manual", "automatic"])("uses exact-tag stable notes for %s checks", async (trigger) => {
    const original = Object.getOwnPropertyDescriptor(process, "resourcesPath");
    const resourcesPath = mkdtempSync(nodePath.join(os.tmpdir(), "ao-stable-feed-"));
    writeFileSync(nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: OrchestratorInc\nrepo: agent-orchestrator\n");
    Object.defineProperty(process, "resourcesPath", { configurable: true, value: resourcesPath });
    try {
      const { module, autoUpdater } = await importAutoUpdater();
      if (trigger === "manual") await module.checkForUpdatesNow(stateDir);
      else await module.startAutoUpdates(stateDir);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(1, {
        provider: "custom", updateProvider: expect.any(Function),
        owner: "OrchestratorInc", repo: "agent-orchestrator",
      });
      expect(autoUpdater.setFeedURL).toHaveBeenCalledTimes(1);
      expect(autoUpdater.allowPrerelease).toBe(false);
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
      for (const settings of [
        { channel: "nightly", feature: null },
        { channel: "latest", feature: { pr: 123 } },
      ] satisfies Pick<UpdateSettings, "channel" | "feature">[]) {
        module.configureFeed(settings);
        expect(autoUpdater.setFeedURL).toHaveBeenLastCalledWith({
          provider: "github", owner: "OrchestratorInc", repo: "agent-orchestrator",
        });
        expect(autoUpdater.allowPrerelease).toBe(true);
      }
    } finally {
      if (original) Object.defineProperty(process, "resourcesPath", original);
      else Reflect.deleteProperty(process, "resourcesPath");
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("manual nightly checks resolve the newest completed release without the Atom feed", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "nightly-mac.yml"
        : process.platform === "linux"
          ? "nightly-linux.yml"
          : "nightly.yml";
    const resourcesPath = mkdtempSync(
      nodePath.join(os.tmpdir(), "ao-nightly-feed-"),
    );
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      "resourcesPath",
    );
    Object.defineProperty(process, "resourcesPath", {
      configurable: true,
      value: resourcesPath,
    });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            tag_name: "v1.0.1-nightly.202608231518",
            draft: false,
            prerelease: true,
            assets: [{ name: "Agent.Orchestrator.dmg" }],
          },
          {
            tag_name: "v1.0.1-nightly.202608231517",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
          {
            tag_name: "v1.0.1-nightly.202608231350",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      const { module, autoUpdater } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });

      await module.checkForUpdatesNow(stateDir);

      expect(fetchMock).toHaveBeenCalledWith(
        "https://api.github.com/repos/Untrivial-ai/agent-orchestrator/releases?per_page=100",
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(2, {
        provider: "generic",
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.1-nightly.202608231517",
        channel: "nightly",
        useMultipleRangeRequest: false,
      });
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(3, {
        provider: "github",
        owner: "Untrivial-ai",
        repo: "agent-orchestrator",
      });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("surfaces the API-discovered nightly build when the check cannot reach the asset CDN", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "nightly-mac.yml"
        : process.platform === "linux"
          ? "nightly-linux.yml"
          : "nightly.yml";
    const resourcesPath = mkdtempSync(
      nodePath.join(os.tmpdir(), "ao-nightly-feed-"),
    );
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      "resourcesPath",
    );
    Object.defineProperty(process, "resourcesPath", {
      configurable: true,
      value: resourcesPath,
    });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            tag_name: "v1.0.1-nightly.202608231517",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      const { module, autoUpdater, statusMessages } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });
      autoUpdater.checkForUpdates.mockRejectedValue(new Error("request timed out"));

      await module.startAutoUpdates(stateDir);

      expect(autoUpdater.checkForUpdates).toHaveBeenCalled();
      expect(statusMessages().at(-1)?.payload).toMatchObject({
        state: "available",
        version: "1.0.1-nightly.202608231517",
      });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("revalidates release history without a second body and refreshes when a release changes", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "nightly-mac.yml"
        : process.platform === "linux"
          ? "nightly-linux.yml"
          : "nightly.yml";
    const resourcesPath = mkdtempSync(
      nodePath.join(os.tmpdir(), "ao-nightly-feed-"),
    );
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      "resourcesPath",
    );
    Object.defineProperty(process, "resourcesPath", {
      configurable: true,
      value: resourcesPath,
    });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            tag_name: "v1.0.1-nightly.202608231518",
            draft: false,
            prerelease: true,
            assets: [{ name: "Agent.Orchestrator.dmg" }],
          },
          {
            tag_name: "v1.0.1-nightly.202608231517",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
          {
            tag_name: "v1.0.1-nightly.202608231350",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
        ]),
        { status: 200, headers: { etag: '"release-v1"' } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      const { module, autoUpdater } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });

      await module.checkForUpdatesNow(stateDir);

      expect(fetchMock).toHaveBeenCalledWith(
        "https://api.github.com/repos/Untrivial-ai/agent-orchestrator/releases?per_page=100",
        expect.objectContaining({ signal: expect.any(AbortSignal) }),
      );
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(2, {
        provider: "generic",
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.1-nightly.202608231517",
        channel: "nightly",
        useMultipleRangeRequest: false,
      });
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 304 }));
      await module.checkForUpdatesNow(stateDir);
      expect(fetchMock).toHaveBeenLastCalledWith(
        expect.any(String),
        expect.objectContaining({ headers: expect.objectContaining({ "If-None-Match": '"release-v1"' }) }),
      );
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(5, expect.objectContaining({
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.1-nightly.202608231517",
      }));
      fetchMock.mockResolvedValueOnce(new Response(JSON.stringify([{
        tag_name: "v1.0.2-nightly.202608241000", draft: false, prerelease: true,
        assets: [{ name: platformManifest }],
      }]), { status: 200, headers: { etag: '"release-v2"' } }));
      await module.checkForUpdatesNow(stateDir);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(8, expect.objectContaining({
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.2-nightly.202608241000",
      }));
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(3, {
        provider: "github",
        owner: "Untrivial-ai",
        repo: "agent-orchestrator",
      });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("carries release notes for nightly, whose feed provider cannot", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "nightly-mac.yml"
        : process.platform === "linux"
          ? "nightly-linux.yml"
          : "nightly.yml";
    const resourcesPath = mkdtempSync(nodePath.join(os.tmpdir(), "ao-nightly-notes-"));
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(process, "resourcesPath");
    Object.defineProperty(process, "resourcesPath", { configurable: true, value: resourcesPath });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify([
            {
              tag_name: "v1.0.1-nightly.202608231517",
              draft: false,
              prerelease: true,
              body: "<ul><li>Stopped the re-stage loop</li><li>Rebuilt the Updates page</li></ul>",
              assets: [{ name: platformManifest }],
            },
          ]),
          { status: 200 },
        ),
      ),
    );

    try {
      const { module, autoUpdater, updaterEvents, statusMessages } = await importAutoUpdater({
        enabled: false,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });

      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-available")?.({ version: "1.0.1-nightly.202608231517" });

      expect(statusMessages().at(-1)?.payload).toMatchObject({
        state: "available",
        releaseNotes: "- Stopped the re-stage loop\n- Rebuilt the Updates page",
      });

      autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
        updaterEvents.get("update-available")?.({ version: "1.0.1" });
        return { isUpdateAvailable: true, updateInfo: { version: "1.0.1" } };
      });
      await module.checkForUpdatesNow(stateDir, { settings: {
        enabled: false, channel: "latest", nightlyAck: false, feature: null,
      } });
      expect(module.getUpdateStatus()).toMatchObject({ state: "available", version: "1.0.1" });
      expect(module.getUpdateStatus()).not.toHaveProperty("releaseNotes");

      autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
        updaterEvents.get("checking-for-update")?.();
        return { isUpdateAvailable: false, updateInfo: { version: "1.0.0" } };
      });
      await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus()).toMatchObject({ state: "not-available" });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("automatic nightly checks resolve the newest completed release without the Atom feed", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "nightly-mac.yml"
        : process.platform === "linux"
          ? "nightly-linux.yml"
          : "nightly.yml";
    const resourcesPath = mkdtempSync(
      nodePath.join(os.tmpdir(), "ao-nightly-feed-"),
    );
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      "resourcesPath",
    );
    Object.defineProperty(process, "resourcesPath", {
      configurable: true,
      value: resourcesPath,
    });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            tag_name: "v1.0.1-nightly.202608231518",
            draft: false,
            prerelease: true,
            assets: [{ name: "Agent.Orchestrator.dmg" }],
          },
          {
            tag_name: "v1.0.1-nightly.202608231517",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      const { module, autoUpdater } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });

      await module.startAutoUpdates(stateDir);

      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(2, {
        provider: "generic",
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.1-nightly.202608231517",
        channel: "nightly",
        useMultipleRangeRequest: false,
      });
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(3, {
        provider: "github",
        owner: "Untrivial-ai",
        repo: "agent-orchestrator",
      });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("feature checks resolve the newest completed PR release without the Atom feed", async () => {
    const platformManifest =
      process.platform === "darwin"
        ? "pr4473-mac.yml"
        : process.platform === "linux"
          ? "pr4473-linux.yml"
          : "pr4473.yml";
    const resourcesPath = mkdtempSync(
      nodePath.join(os.tmpdir(), "ao-feature-feed-"),
    );
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(
      process,
      "resourcesPath",
    );
    Object.defineProperty(process, "resourcesPath", {
      configurable: true,
      value: resourcesPath,
    });
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            tag_name: "v1.0.0-pr4473.202608271543",
            draft: false,
            prerelease: true,
            assets: [{ name: "Agent.Orchestrator.dmg" }],
          },
          {
            tag_name: "v1.0.0-pr4473.202608271542",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
          {
            tag_name: "v1.0.0-pr9999.202608271544",
            draft: false,
            prerelease: true,
            assets: [{ name: platformManifest }],
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      const { module, autoUpdater } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: { pr: 4473 },
      });

      await module.startAutoUpdates(stateDir);

      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(2, {
        provider: "generic",
        url: "https://github.com/Untrivial-ai/agent-orchestrator/releases/download/v1.0.0-pr4473.202608271542",
        channel: "pr4473",
        useMultipleRangeRequest: false,
      });
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
      expect(autoUpdater.setFeedURL).toHaveBeenNthCalledWith(3, {
        provider: "github",
        owner: "Untrivial-ai",
        repo: "agent-orchestrator",
      });
    } finally {
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("checks stable on launch and every 15 minutes when automatic downloads are disabled", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, autoUpdater } = await importAutoUpdater({
      enabled: false,
      channel: "latest",
      nightlyAck: false,
      feature: null,
    });

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    const { delay } = latestInterval(setIntervalSpy);
    expect(delay).toBe(15 * 60 * 1000);
    await vi.advanceTimersByTimeAsync(delay);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it("checks nightly every 15 minutes when automatic downloads are disabled", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, autoUpdater } = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.channel).toBe("nightly");
    expect(autoUpdater.allowPrerelease).toBe(true);
    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(latestInterval(setIntervalSpy).delay).toBe(15 * 60 * 1000);
  });

  it("does not stack periodic automatic or retirement timers across repeated startAutoUpdates calls", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module } = await importAutoUpdater();

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);

    expect(setIntervalSpy).toHaveBeenCalledTimes(2);
    expect(
      setIntervalSpy.mock.calls
        .map(([, delay]) => delay)
        .sort((a, b) => (a ?? 0) - (b ?? 0)),
    ).toEqual([15 * 60 * 1000, 30 * 60 * 1000]);
  });

  it("logs periodic check failures without UI and retries on later ticks", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, dialog, statusMessages } =
      await importAutoUpdater();
    autoUpdater.checkForUpdates
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(undefined);

    await module.startAutoUpdates(stateDir);
    const { delay } = latestInterval(setIntervalSpy);

    await vi.advanceTimersByTimeAsync(delay);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      expect.any(Error),
    );
    expect(dialog.showMessageBox).not.toHaveBeenCalled();
    expect(statusMessages()).toHaveLength(0);

    await vi.advanceTimersByTimeAsync(delay * 2);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
  });

  it("caps automatic retry delays and resets after a successful check", async () => {
    vi.useFakeTimers();
    const intervals = vi.spyOn(globalThis, "setInterval");
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockRejectedValue(new Error("offline"));
    await module.startAutoUpdates(stateDir);
    expect(latestInterval(intervals).delay).toBe(30 * 60 * 1000);
    for (const expected of [60, 120, 240, 240]) {
      await vi.advanceTimersByTimeAsync(latestInterval(intervals).delay);
      expect(latestInterval(intervals).delay).toBe(expected * 60 * 1000);
    }
    autoUpdater.checkForUpdates.mockResolvedValue(undefined);
    await vi.advanceTimersByTimeAsync(latestInterval(intervals).delay);
    expect(latestInterval(intervals).delay).toBe(15 * 60 * 1000);
  });

  it("backs off from the base interval when settings fail before a check", async () => {
    vi.useFakeTimers();
    const intervals = vi.spyOn(globalThis, "setInterval");
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const readUpdateSettings = vi.fn<() => Promise<UpdateSettings>>().mockRejectedValue(new Error("settings unavailable"));
    const { module, autoUpdater } = await importAutoUpdater(readUpdateSettings);
    await module.startAutoUpdates(stateDir);
    expect(latestInterval(intervals).delay).toBe(30 * 60 * 1000);
    for (const expected of [60, 120, 240]) {
      await vi.advanceTimersByTimeAsync(latestInterval(intervals).delay);
      expect(latestInterval(intervals).delay).toBe(expected * 60 * 1000);
    }
    expect(autoUpdater.checkForUpdates).not.toHaveBeenCalled();
  });

  it("keeps backing off when an available update repeatedly fails to download", async () => {
    vi.useFakeTimers();
    const intervals = vi.spyOn(globalThis, "setInterval");
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("update-available")?.({ version: "2.0.0" });
      return Promise.resolve({
        isUpdateAvailable: true,
        updateInfo: { version: "2.0.0" },
        downloadPromise: Promise.reject(new Error("download failed")),
      });
    });

    await module.startAutoUpdates(stateDir);
    expect(latestInterval(intervals).delay).toBe(30 * 60 * 1000);
    for (const expected of [60, 120, 240]) {
      await vi.advanceTimersByTimeAsync(latestInterval(intervals).delay);
      expect(latestInterval(intervals).delay).toBe(expected * 60 * 1000);
    }
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(4);
  });

  it("logs updater error events during automatic checks without broadcasting renderer errors", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages, telemetryMessages } =
      await importAutoUpdater();
    const err = new Error("feed failed");
    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
    expect(statusMessages()).toEqual([]);
    expect(module.getUpdateStatus()).toMatchObject({ state: "idle" });
    expect(telemetryMessages().map((m) => m.payload)).toEqual([
      {
        event: "ao.updates.failed",
        phase: "check",
        trigger: "automatic",
        error_category: "unknown",
      },
    ]);
  });

  it("restores the prior renderer status when an automatic check emits checking before an error", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("feed failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-available")?.({ version: "2.0.0" });
    expect(module.getUpdateStatus()).toMatchObject({
      state: "available",
      version: "2.0.0",
      checkedAt: expect.any(Number),
    });

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
    expect(module.getUpdateStatus()).toMatchObject({
      state: "available",
      version: "2.0.0",
      checkedAt: expect.any(Number),
    });
  });

  it("reports an automatic download failure after progress", async () => {
    vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const lateDownload = deferred();
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("download failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-available")?.({ version: "2.0.0" });
    expect(module.getUpdateStatus()).toEqual({
      state: "available",
      version: "2.0.0",
      checkedAt: expect.any(Number),
    });

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("update-available")?.({ version: "2.1.0" });
      updaterEvents.get("download-progress")?.({ percent: 42 });
      return Promise.resolve({ downloadPromise: lateDownload.promise });
    });
    const startPromise = module.startAutoUpdates(stateDir);
    await flushMicrotasks();
    expect(module.getUpdateStatus()).toEqual({
      state: "downloading",
      version: "2.1.0",
      percent: 42,
      checkedAt: expect.any(Number),
    });

    updaterEvents.get("error")?.(err);
    lateDownload.resolve();
    await startPromise;

    expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: "download failed" });

  });

  it("restores a staged update when an automatic check emits checking before an error", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T12:00:00.000Z"));
    const stagedAt = Date.now();
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("feed failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloaded",
      version: "2.1.0",
      stagedAt,
      escalated: false,
      checkedAt: expect.any(Number),
      staged: { version: "2.1.0", stagedAt, escalated: false },
    });

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloaded",
      version: "2.1.0",
      stagedAt,
      escalated: false,
      checkedAt: expect.any(Number),
      staged: { version: "2.1.0", stagedAt, escalated: false },
    });
  });

  it("tells the renderer once automatic checks have failed three times over", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();
    const err = new Error(
      'Cannot find nightly-mac.yml in the latest release artifacts: HttpError: 404 "method: GET url: https://example.invalid/nightly-mac.yml"',
    );
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(statusMessages()).toEqual([]);
    expect(module.getUpdateStatus().checksFailing).toBeUndefined();

    await module.startAutoUpdates(stateDir);

    expect(statusMessages().map((message) => message.payload)).toEqual([
      expect.objectContaining({ state: "idle", checksFailing: true }),
    ]);
    expect(module.getUpdateStatus()).toEqual(
      expect.objectContaining({ state: "idle", checksFailing: true }),
    );
    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
  });

  it("announces a failing streak once, not once per failure", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("error")?.(new Error("HttpError: 404 nightly-mac.yml"));
      return Promise.resolve();
    });

    for (let attempt = 0; attempt < 6; attempt += 1) {
      await module.startAutoUpdates(stateDir);
    }

    expect(statusMessages()).toHaveLength(1);
  });

  it("clears the failing-check streak as soon as a check reaches an answer", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("error")?.(new Error("HttpError: 404 nightly-mac.yml"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus().checksFailing).toBe(true);

    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("update-not-available")?.();
      return Promise.resolve();
    });
    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus()).toEqual(
      expect.objectContaining({ state: "not-available" }),
    );
    expect(module.getUpdateStatus().checksFailing).toBeUndefined();
  });

  it("clears a remembered macOS build once automatic checks exhaust the threshold", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
        version: "2.1.0", stagedAt: Date.now(), channel: "latest",
      }));
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater(
        { enabled: true, channel: "latest", nightlyAck: false, feature: null },
      );
      autoUpdater.checkForUpdates.mockImplementation(() => {
        updaterEvents.get("error")?.(new Error("HttpError: 404 latest-mac.yml"));
        return Promise.resolve();
      });
      await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
      await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
      await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus().staged).toBeUndefined();
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(existsSync(nodePath.join(stateDir, "staged-update.json"))).toBe(false);
    } finally { restore(); }
  });

  it("clears a remembered build on non-darwin platforms too", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
      version: "2.1.0", stagedAt: Date.now(), channel: "latest",
    }));
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater(
      { enabled: true, channel: "latest", nightlyAck: false, feature: null },
    );
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("error")?.(new Error("HttpError: 404 latest.yml"));
      return Promise.resolve();
    });
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus().staged).toBeUndefined();
  });

  it("keeps a current-process staged build even when checks fail", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater(
        { enabled: true, channel: "latest", nightlyAck: false, feature: null },
      );
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      autoUpdater.checkForUpdates.mockImplementation(() => {
        updaterEvents.get("error")?.(new Error("HttpError: 404 latest-mac.yml"));
        return Promise.resolve();
      });
      for (let i = 0; i < 4; i += 1) await module.startAutoUpdates(stateDir);
      expect(module.getUpdateStatus().staged?.version).toBe("2.1.0");
    } finally { restore(); }
  });

  it("does not overwrite a newer staged escalation when an automatic check fails", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const stagedAt = new Date("2026-07-17T12:00:00.000Z").getTime();
    vi.setSystemTime(stagedAt);
    const automaticCheck = deferred();
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("feed failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    await Promise.resolve();
    await Promise.resolve();
    const { callback: runEscalation } = latestInterval(setIntervalSpy);

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      return automaticCheck.promise;
    });
    const startPromise = module.startAutoUpdates(stateDir);
    await Promise.resolve();
    await Promise.resolve();

    vi.setSystemTime(stagedAt + 49 * 60 * 60 * 1000);
    runEscalation();
    await Promise.resolve();
    await Promise.resolve();
    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloaded",
      version: "2.1.0",
      stagedAt,
      escalated: true,
      checkedAt: expect.any(Number),
      staged: { version: "2.1.0", stagedAt, escalated: true },
    });

    updaterEvents.get("error")?.(err);
    automaticCheck.resolve();
    await startPromise;

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloaded",
      version: "2.1.0",
      stagedAt,
      escalated: true,
      checkedAt: expect.any(Number),
      staged: { version: "2.1.0", stagedAt, escalated: true },
    });
  });

  it("reports the download failure and retains valid staged metadata", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const stagedAt = new Date("2026-07-17T12:00:00.000Z").getTime();
    vi.setSystemTime(stagedAt);
    const automaticDownload = deferred();
    vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("download failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    await Promise.resolve();
    await Promise.resolve();
    const { callback: runEscalation } = latestInterval(setIntervalSpy);

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      return Promise.resolve({ downloadPromise: automaticDownload.promise });
    });
    const startPromise = module.startAutoUpdates(stateDir);
    await Promise.resolve();
    await Promise.resolve();

    vi.setSystemTime(stagedAt + 49 * 60 * 60 * 1000);
    runEscalation();
    await Promise.resolve();
    await Promise.resolve();
    updaterEvents.get("update-available")?.({ version: "2.2.0" });
    updaterEvents.get("download-progress")?.({ percent: 64 });
    expect(module.getUpdateStatus()).toEqual({
      state: "downloading",
      version: "2.2.0",
      percent: 64,
      checkedAt: expect.any(Number),
      staged: { version: "2.1.0", stagedAt, escalated: true },
    });

    updaterEvents.get("error")?.(err);
    automaticDownload.resolve();
    await startPromise;

    expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: "download failed" });
    expect(module.getUpdateStatus().staged).toEqual({ version: "2.1.0", stagedAt, escalated: true });

  });

  it("keeps automatic download errors silent after checkForUpdates resolves", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const lateDownload = deferred();
    const { module, autoUpdater, updaterEvents, statusMessages, telemetryMessages } =
      await importAutoUpdater();
    const err = new Error("download failed");
    autoUpdater.checkForUpdates.mockResolvedValueOnce({
      downloadPromise: lateDownload.promise,
    });

    const startPromise = module.startAutoUpdates(stateDir);
    await Promise.resolve();
    await Promise.resolve();
    let startSettled = false;
    void startPromise.then(() => {
      startSettled = true;
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(startSettled).toBe(false);
    updaterEvents.get("error")?.(err);

    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      err,
    );
    expect(statusMessages()).toEqual([]);
    expect(telemetryMessages().map((m) => m.payload)).toEqual([
      {
        event: "ao.updates.failed",
        phase: "check",
        trigger: "automatic",
        error_category: "unknown",
      },
    ]);
    lateDownload.resolve();
    await startPromise;
  });

  it("keeps manual download errors visible when requested during an automatic check", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const automaticCheck = deferred();
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error("manual download failed");
    autoUpdater.checkForUpdates.mockReturnValueOnce(automaticCheck.promise);
    autoUpdater.downloadUpdate.mockImplementationOnce(() => {
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    const startPromise = module.startAutoUpdates(stateDir);
    await Promise.resolve();
    await Promise.resolve();
    const downloadPromise = module.downloadUpdateNow();
    await Promise.resolve();
    await Promise.resolve();

    automaticCheck.resolve();
    await Promise.all([startPromise, downloadPromise]);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "manual download failed",
    });
  });

  it("keeps manual updater error events visible to the renderer", async () => {
    const { module, rendererSend, updaterEvents } = await importAutoUpdater();
    const err = new Error("manual feed failed");

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("error")?.(err);

    expect(rendererSend).toHaveBeenCalledWith("updates:status", expect.anything());
    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "manual feed failed",
      checkedAt: expect.any(Number),
    });
  });

  it("broadcasts friendly error on manifest 404 event during manual check", async () => {
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    const { module, updaterEvents } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("error")?.(err);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message:
        "Couldn't check for updates — the update information was not found on the server.",
      errorInfo: { kind: "not-found", phase: "check" },
      checkedAt: expect.any(Number),
    });
  });

  it("broadcasts friendly error on manifest 404 event during manual download", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );
    autoUpdater.downloadUpdate.mockImplementationOnce(() => {
      updaterEvents.get("error")?.(err);
      return Promise.resolve();
    });

    await module.downloadUpdateNow();

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "Download failed — the update file was not found on the server.",
      errorInfo: { kind: "not-found", phase: "download" },
    });
  });

  it("broadcasts friendly error on rejected checkForUpdatesNow with manifest 404", async () => {
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    const { module, autoUpdater } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );
    autoUpdater.checkForUpdates.mockRejectedValueOnce(err);

    await module.checkForUpdatesNow(stateDir);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message:
        "Couldn't check for updates — the update information was not found on the server.",
      errorInfo: { kind: "not-found", phase: "check" },
    });
  });

  it("broadcasts friendly error on rejected downloadUpdateNow with manifest 404", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );
    autoUpdater.downloadUpdate.mockRejectedValueOnce(err);

    await module.downloadUpdateNow();

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "Download failed — the update file was not found on the server.",
      errorInfo: { kind: "not-found", phase: "download" },
    });
  });

  it("restores staged build on manifest 404 event during manual check", async () => {
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    const { module, updaterEvents } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    const checkedAt = module.getUpdateStatus().checkedAt;
    updaterEvents.get("error")?.(err);
    expect(module.getUpdateStatus().checkedAt).toBe(checkedAt);
    expect(module.getUpdateStatus().checkError).toBe(
      "Couldn't check for updates — the update information was not found on the server.",
    );

    expect(module.getUpdateStatus()).toEqual(
      expect.objectContaining({
        state: "downloaded",
        version: "2.1.0",
      }),
    );
  });

  it("restores staged build on rejected checkForUpdatesNow with manifest 404", async () => {
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    autoUpdater.checkForUpdates.mockRejectedValueOnce(err);
    await module.checkForUpdatesNow(stateDir);

    expect(module.getUpdateStatus()).toEqual(
      expect.objectContaining({
        state: "downloaded",
        version: "2.1.0",
      }),
    );
  });

  it("restores an earlier staged status immediately after the owned manual-check failure", async () => {
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();
    const err = new Error(
      'Cannot find latest-mac.yml in the latest release artifacts (https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml):\nHttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/latest-mac.yml"',
    );
    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      return Promise.resolve();
    });
    await module.checkForUpdatesNow(stateDir, {
      requestId: "earlier-download",
    });

    autoUpdater.checkForUpdates.mockRejectedValueOnce(err);
    await module.checkForUpdatesNow(stateDir, {
      requestId: "manual-update",
    });

    expect(statusMessages().slice(-2).map((message) => message.payload)).toEqual([
      expect.objectContaining({
        state: "error",
        requestId: "manual-update",
      }),
      expect.objectContaining({
        state: "downloaded",
        version: "2.1.0",
        requestId: "earlier-download",
      }),
    ]);
  });

  it("still surfaces non-manifest 404 errors", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, updaterEvents } = await importAutoUpdater();
    const err = new Error(
      'HttpError: 404 "method: GET url: https://github.com/AgentWrapper/agent-orchestrator/releases/download/v0.10.1/some-file.png"',
    );

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("error")?.(err);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "Couldn't check for updates — the update information was not found on the server.",
      errorInfo: { kind: "not-found", phase: "check" },
      checkedAt: expect.any(Number),
    });
  });

  it("shows one sentence for a raw releases-feed failure and logs the original (#5755)", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater } = await importAutoUpdater();
    const err = releasesFeedTimeout();
    autoUpdater.checkForUpdates.mockRejectedValueOnce(err);

    await module.checkForUpdatesNow(stateDir, { requestId: "manual-update" });

    expect(module.getUpdateStatus()).toMatchObject({
      state: "error",
      message: "Couldn't check for updates — the update server timed out. Try again in a few minutes.",
      requestId: "manual-update",
    });
    expect(JSON.stringify(module.getUpdateStatus())).not.toMatch(/set-cookie|app\.asar|<html/i);
    expect(consoleError).toHaveBeenCalledWith("manual update check failed:", err);
  });

  it("logs a manual failure once when the updater also emits it", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const err = releasesFeedTimeout();
    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(err);
      return Promise.reject(err);
    });

    await module.checkForUpdatesNow(stateDir);

    expect(consoleError.mock.calls.filter((call) => call.includes(err))).toEqual([["manual update check failed:", err]]);
  });

  it("keeps a raw automatic-check failure out of checkError (#5755)", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(releasesFeedTimeout());
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus().checkError).toBe(
      "Couldn't check for updates — the update server timed out. Try again in a few minutes.",
    );
    expect(module.getUpdateStatus().checkErrorInfo).toEqual({ kind: "timeout", phase: "check" });
  });

  it("keeps a raw net:: string out of checkError (#5755)", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("net::ERR_INTERNET_DISCONNECTED"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus().checkError).toBe("Couldn't check for updates — the update server couldn't be reached. Check your connection and try again.");
  });

  it("shows one sentence when a manual download fails on the network (#5755)", async () => {
    const restore = stubProcess("linux", "/usr/bin/node");
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      const { module, autoUpdater } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      autoUpdater.downloadUpdate.mockRejectedValue(new Error("net::ERR_CONNECTION_RESET"));

      await module.downloadUpdateNow("failed-download");

      expect(module.getUpdateStatus()).toMatchObject({
        state: "error",
        requestId: "failed-download",
        message: "Download failed — the update server couldn't be reached. Check your connection and try again.",
      });
    } finally { restore(); }
  });

  it("flags net errors on a rejected manual check", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockRejectedValueOnce(new Error("net::ERR_FAILED"));

    await module.checkForUpdatesNow(stateDir);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "Couldn't check for updates — the update server couldn't be reached. Check your connection and try again.",
      errorInfo: { kind: "unreachable", phase: "check" },
      netError: true,
    });
  });

  it("flags net errors on a net:: error event during a manual check", async () => {
    const { module, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "Couldn't check for updates — the update server couldn't be reached. Check your connection and try again.",
      errorInfo: { kind: "unreachable", phase: "check" },
      netError: true,
      checkedAt: expect.any(Number),
    });
  });

  it("stops showing checking when an error event fires before the check promise settles", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => new Promise(() => undefined));

    void module.checkForUpdatesNow(stateDir, { requestId: "manual-update-1" });
    await flushMicrotasks();
    expect(module.getUpdateStatus()).toMatchObject({ state: "checking" });

    updaterEvents.get("error")?.(new Error("net::ERR_SSL_PROTOCOL_ERROR"));

    expect(module.getUpdateStatus()).toMatchObject({
      state: "error",
      message: "Couldn't check for updates — the update server couldn't be reached. Check your connection and try again.",
      errorInfo: { kind: "unreachable", phase: "check" },
      netError: true,
      requestId: "manual-update-1",
    });
  });

  it("keeps non-net manual check errors verbatim", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockRejectedValueOnce(new Error("boom"));

    await module.checkForUpdatesNow(stateDir);

    expect(module.getUpdateStatus()).toEqual({
      state: "error",
      message: "boom",
    });
  });

  it("nudges a restart after three consecutive net:: automatic-check failures", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({ state: "idle" });

    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      staleCheckNudge: true,
      checksFailing: true,
    });
  });

  it("does not nudge for non-net automatic-check failures", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("HttpError: 500"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus().staleCheckNudge).toBeUndefined();
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      checksFailing: true,
    });
  });

  it("does not nudge when a non-net failure breaks the net:: streak", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    const netFail = () => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));
      return Promise.resolve();
    };
    const httpFail = () => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("HttpError: 500"));
      return Promise.resolve();
    };

    autoUpdater.checkForUpdates.mockImplementation(netFail);
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    autoUpdater.checkForUpdates.mockImplementation(httpFail);
    await module.startAutoUpdates(stateDir);
    autoUpdater.checkForUpdates.mockImplementation(netFail);
    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus().staleCheckNudge).toBeUndefined();
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      checksFailing: true,
    });
  });

  it("counts one failure when an automatic check both emits error and rejects", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));
      return Promise.reject(new Error("net::ERR_FAILED"));
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({ state: "idle" });

    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      staleCheckNudge: true,
      checksFailing: true,
    });
  });

  it("surfaces the nudge when automatic checks reject without an error event", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      return Promise.reject(new Error("net::ERR_FAILED"));
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({ state: "idle" });

    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      staleCheckNudge: true,
      checksFailing: true,
    });
  });

  it("stamps the nudge on getUpdateStatus even when no broadcast carried it", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);

    expect(statusMessages().map((message) => message.payload)).toEqual([
      expect.objectContaining({
        state: "idle",
        staleCheckNudge: true,
        checksFailing: true,
      }),
    ]);
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      staleCheckNudge: true,
      checksFailing: true,
    });
  });

  it("clears the nudge once a check succeeds again", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(new Error("net::ERR_FAILED"));
      return Promise.resolve();
    });

    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    await module.startAutoUpdates(stateDir);
    expect(module.getUpdateStatus()).toMatchObject({
      state: "idle",
      staleCheckNudge: true,
      checksFailing: true,
    });

    autoUpdater.checkForUpdates.mockImplementation(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("update-not-available")?.();
      return Promise.resolve();
    });
    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus()).toMatchObject({ state: "not-available", checkedAt: expect.any(Number) });
  });

  it("logs settings failures during automatic checks and retries on later ticks", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const readUpdateSettings = vi
      .fn<() => Promise<UpdateSettings>>()
      .mockRejectedValueOnce(new Error("settings locked"))
      .mockResolvedValue({
        enabled: true,
        channel: "latest",
        nightlyAck: false,
        feature: null,
      });
    const { module, autoUpdater } = await importAutoUpdater(readUpdateSettings);

    await expect(module.startAutoUpdates(stateDir)).resolves.toBeUndefined();
    expect(autoUpdater.checkForUpdates).not.toHaveBeenCalled();
    expect(consoleErrorSpy).toHaveBeenCalledWith(
      "auto-update check failed:",
      expect.any(Error),
    );
    const readsBeforeRetry = readUpdateSettings.mock.calls.length;
    expect(latestInterval(setIntervalSpy).delay).toBe(30 * 60 * 1000);
    latestInterval(setIntervalSpy).callback();
    await flushMicrotasks();

    expect(readUpdateSettings.mock.calls.length).toBe(readsBeforeRetry + 1);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
  });

  it("restores automatic download behavior on every automatic retry after a manual check", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, autoUpdater } = await importAutoUpdater();

    await module.startAutoUpdates(stateDir);
    const { delay } = latestInterval(setIntervalSpy);
    await module.checkForUpdatesNow(stateDir);
    expect(autoUpdater.autoDownload).toBe(false);

    await vi.advanceTimersByTimeAsync(delay);

    expect(autoUpdater.autoDownload).toBe(true);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
  });

  it("waits for an in-flight manual check before a periodic automatic check restores autoDownload", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const manualCheck = deferred();
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates
      .mockResolvedValueOnce(undefined)
      .mockReturnValueOnce(manualCheck.promise)
      .mockResolvedValueOnce(undefined);

    await module.startAutoUpdates(stateDir);
    const { delay } = latestInterval(setIntervalSpy);
    const manualPromise = module.checkForUpdatesNow(stateDir);
    await flushMicrotasks();
    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(delay);
    await flushMicrotasks();
    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);

    manualCheck.resolve();
    await manualPromise;
    await flushMicrotasks();

    expect(autoUpdater.autoDownload).toBe(true);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
  });

  it("preserves concurrent settings changes while clearing the same retired feature pin", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const retirementLookup = deferred();
    let current: UpdateSettings = {
      enabled: false,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 2709 },
    };
    const readUpdateSettings = vi.fn(() => Promise.resolve(current));
    const reconcileFeaturePin = vi
      .fn<
        (
          settings: UpdateSettings,
        ) => Promise<{ settings: UpdateSettings; cleared: boolean }>
      >()
      .mockResolvedValueOnce({ settings: current, cleared: false })
      .mockImplementationOnce(async (snapshot) => {
        await retirementLookup.promise;
        return { settings: { ...snapshot, feature: null }, cleared: true };
      });
    const { module, updateUpdateSettings } = await importAutoUpdater(
      readUpdateSettings,
      { reconcileFeaturePin },
    );
    updateUpdateSettings.mockImplementation(
      async (
        _stateDir: string,
        update: (
          settings: UpdateSettings,
        ) => UpdateSettings | Promise<UpdateSettings>,
      ) => {
        current = await update(current);
        return current;
      },
    );

    await module.startAutoUpdates(stateDir);
    intervalWithDelay(setIntervalSpy, 30 * 60 * 1000)();
    await flushMicrotasks();
    current = {
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: { pr: 2709 },
    };
    retirementLookup.resolve();
    await flushMicrotasks();

    expect(current).toEqual({
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });
  });

  it("does not clear a newly selected feature after an older pin retires", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const retirementLookup = deferred();
    let current: UpdateSettings = {
      enabled: false,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 2709 },
    };
    const readUpdateSettings = vi.fn(() => Promise.resolve(current));
    const reconcileFeaturePin = vi
      .fn<
        (
          settings: UpdateSettings,
        ) => Promise<{ settings: UpdateSettings; cleared: boolean }>
      >()
      .mockResolvedValueOnce({ settings: current, cleared: false })
      .mockImplementationOnce(async (snapshot) => {
        await retirementLookup.promise;
        return { settings: { ...snapshot, feature: null }, cleared: true };
      });
    const { module, updateUpdateSettings } = await importAutoUpdater(
      readUpdateSettings,
      { reconcileFeaturePin },
    );
    updateUpdateSettings.mockImplementation(
      async (
        _stateDir: string,
        update: (
          settings: UpdateSettings,
        ) => UpdateSettings | Promise<UpdateSettings>,
      ) => {
        current = await update(current);
        return current;
      },
    );

    await module.startAutoUpdates(stateDir);
    intervalWithDelay(setIntervalSpy, 30 * 60 * 1000)();
    await flushMicrotasks();
    current = {
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: { pr: 2710 },
    };
    retirementLookup.resolve();
    await flushMicrotasks();

    expect(current).toEqual({
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: { pr: 2710 },
    });
  });

  it("coalesces retirement ticks queued behind a long updater operation", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const automaticCheck = deferred();
    const settings: UpdateSettings = {
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 2709 },
    };
    const reconcileFeaturePin = vi.fn((current: UpdateSettings) =>
      Promise.resolve({ settings: current, cleared: false }),
    );
    const { module, autoUpdater } = await importAutoUpdater(settings, {
      reconcileFeaturePin,
    });
    autoUpdater.checkForUpdates.mockReturnValueOnce(automaticCheck.promise);

    const startPromise = module.startAutoUpdates(stateDir);
    await flushMicrotasks();
    expect(reconcileFeaturePin).toHaveBeenCalledTimes(1);

    const runRetirementPoll = intervalWithDelay(setIntervalSpy, 30 * 60 * 1000);
    runRetirementPoll();
    runRetirementPoll();
    runRetirementPoll();
    await flushMicrotasks();
    expect(reconcileFeaturePin).toHaveBeenCalledTimes(1);

    automaticCheck.resolve();
    await startPromise;
    await flushMicrotasks();
    expect(reconcileFeaturePin).toHaveBeenCalledTimes(2);

    runRetirementPoll();
    await flushMicrotasks();
    expect(reconcileFeaturePin).toHaveBeenCalledTimes(3);
  });

  it("applies feature settings and owns its check after an in-flight automatic check", async () => {
    const automaticCheck = deferred();
    const featureSettings: UpdateSettings = {
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 2709 },
    };
    const { module, autoUpdater, updaterEvents, writeUpdateSettings } =
      await importAutoUpdater();
    autoUpdater.checkForUpdates
      .mockReturnValueOnce(automaticCheck.promise)
      .mockImplementationOnce(() => {
        expect(writeUpdateSettings).toHaveBeenCalledWith(
          stateDir,
          { ...featureSettings, macDifferentialUpdates: false },
        );
        expect(autoUpdater.channel).toBe("pr2709");
        updaterEvents.get("update-available")?.({ version: "2.0.0-pr2709.1" });
        return Promise.resolve();
      });

    const startPromise = module.startAutoUpdates(stateDir);
    await flushMicrotasks();
    const featureCheck = module.checkForUpdatesNow(stateDir, {
      settings: featureSettings,
      requestId: "feature-2709",
    });
    await flushMicrotasks();

    updaterEvents.get("update-available")?.({ version: "1.9.0" });
    expect(module.getUpdateStatus()).toEqual({
      state: "downloading",
      version: "1.9.0",
      checkedAt: expect.any(Number),
    });

    automaticCheck.resolve();
    await Promise.all([startPromise, featureCheck]);

    expect(module.getUpdateStatus()).toEqual({
      state: "available",
      version: "2.0.0-pr2709.1",
      requestId: "feature-2709",
      checkedAt: expect.any(Number),
    });
  });

  it("keeps feature request ownership through downloaded escalation rebroadcasts", async () => {
    vi.useFakeTimers();
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0-pr2709.1" });
      return Promise.resolve();
    });

    await module.checkForUpdatesNow(stateDir, { requestId: "feature-2709" });
    await flushMicrotasks();

    expect(module.getUpdateStatus()).toEqual(
      expect.objectContaining({
        state: "downloaded",
        version: "2.0.0-pr2709.1",
        requestId: "feature-2709",
      }),
    );
  });

  it("reconciles the automatic scheduler when settings change at runtime", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");
    let current: UpdateSettings = {
      enabled: false,
      channel: "latest",
      nightlyAck: false,
      feature: null,
    };
    const readUpdateSettings = vi.fn(() => Promise.resolve(current));
    const { module, autoUpdater, writeUpdateSettings } =
      await importAutoUpdater(readUpdateSettings);
    writeUpdateSettings.mockImplementation(
      async (_stateDir: string, next: UpdateSettings) => {
        current = next;
      },
    );

    await module.startAutoUpdates(stateDir);
    expect(setIntervalSpy).toHaveBeenCalledTimes(2);

    await module.setUpdateSettings(stateDir, { ...current, enabled: true });
    expect(setIntervalSpy.mock.calls.map(([, delay]) => delay)).toContain(
      15 * 60 * 1000,
    );

    await module.setUpdateSettings(stateDir, {
      ...current,
      channel: "nightly",
      nightlyAck: true,
    });
    expect(latestInterval(setIntervalSpy).delay).toBe(15 * 60 * 1000);

    await module.setUpdateSettings(stateDir, { ...current, enabled: false });
    expect(latestInterval(setIntervalSpy).delay).toBe(15 * 60 * 1000);
    expect(setIntervalSpy).toHaveBeenCalledTimes(2);
    expect(clearIntervalSpy).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(15 * 60 * 1000);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(autoUpdater.autoDownload).toBe(false);
  });

  it("does not let a stale disabled check clear a concurrently enabled scheduler", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const disabledRead = deferred<UpdateSettings>();
    let current: UpdateSettings = {
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: null,
    };
    const readUpdateSettings = vi
      .fn<() => Promise<UpdateSettings>>()
      .mockResolvedValueOnce(current)
      .mockReturnValueOnce(disabledRead.promise)
      .mockImplementation(() => Promise.resolve(current));
    const { module, autoUpdater, writeUpdateSettings } =
      await importAutoUpdater(readUpdateSettings);
    writeUpdateSettings.mockImplementation(
      async (_stateDir: string, next: UpdateSettings) => {
        current = next;
      },
    );

    await module.startAutoUpdates(stateDir);
    intervalWithDelay(setIntervalSpy, 15 * 60 * 1000)();
    await flushMicrotasks();
    const enable = module.setUpdateSettings(stateDir, {
      ...current,
      enabled: true,
    });
    disabledRead.resolve({ ...current, enabled: false });
    await enable;
    await flushMicrotasks();

    await vi.advanceTimersByTimeAsync(15 * 60 * 1000);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
  });

  it("coalesces automatic-check ticks while an automatic check is still running", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const slowCheck = deferred();
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates
      .mockResolvedValueOnce(undefined)
      .mockReturnValueOnce(slowCheck.promise)
      .mockResolvedValueOnce(undefined);

    await module.startAutoUpdates(stateDir);
    const runAutoCheck = intervalWithDelay(setIntervalSpy, 15 * 60 * 1000);
    runAutoCheck();
    await flushMicrotasks();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);

    runAutoCheck();
    runAutoCheck();
    runAutoCheck();
    await flushMicrotasks();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);

    slowCheck.resolve();
    await flushMicrotasks();
    runAutoCheck();
    await flushMicrotasks();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(3);
  });

  it("unrefs the periodic timer when the runtime supports it", async () => {
    const unref = vi.fn();
    const setIntervalStub = vi.fn((_callback: () => void, _delay?: number) => ({
      unref,
    }));
    vi.stubGlobal("setInterval", setIntervalStub);
    const { module } = await importAutoUpdater();

    await module.startAutoUpdates(stateDir);

    expect(unref).toHaveBeenCalledTimes(2);
  });

  it("suspends auto-download while a build is already staged", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const autoDownloadPerCheck: boolean[] = [];
    autoUpdater.checkForUpdates.mockImplementation(() => {
      autoDownloadPerCheck.push(autoUpdater.autoDownload);
      updaterEvents.get("update-available")?.({ version: "2.1.0" });
      return Promise.resolve({
        isUpdateAvailable: true,
        updateInfo: { version: "2.1.0" },
      });
    });

    await module.startAutoUpdates(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    await module.startAutoUpdates(stateDir);

    expect(autoDownloadPerCheck).toEqual([true, false]);
    expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
  });

  it("still auto-downloads a build newer than the staged one", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockImplementation(() =>
      Promise.resolve({
        isUpdateAvailable: true,
        updateInfo: { version: "2.2.0" },
      }),
    );

    await module.startAutoUpdates(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
  });

  it("keeps the original staged time when the same build is re-staged", async () => {
    vi.useFakeTimers();
    const stagedAt = new Date("2026-07-17T12:00:00.000Z").getTime();
    vi.setSystemTime(stagedAt);
    const { module, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(module.getUpdateStatus().stagedAt).toBe(stagedAt);

    vi.setSystemTime(stagedAt + 60 * 60 * 1000);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(module.getUpdateStatus().stagedAt).toBe(stagedAt);

    updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
    expect(module.getUpdateStatus().stagedAt).toBe(stagedAt + 60 * 60 * 1000);
  });

  it("re-arms the escalation timer only for a newly staged build", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    const afterFirst = setIntervalSpy.mock.calls.length;

    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(setIntervalSpy.mock.calls.length).toBe(afterFirst);

    updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
    expect(setIntervalSpy.mock.calls.length).toBeGreaterThan(afterFirst);
  });

  it.each([false, true])("settles an automatic check without a terminal event (available %s)", async (available) => {
    const h = await importAutoUpdater({ enabled: false, channel: "nightly", nightlyAck: true, feature: null });
    h.autoUpdater.checkForUpdates.mockImplementation(async () => {
      h.updaterEvents.get("checking-for-update")?.();
      return { isUpdateAvailable: available, updateInfo: { version: "2.0.0-nightly.1" } };
    });
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe(available ? "available" : "not-available");
  });

  it.each(["manual", "automatic", "return-home"] as const)("aborts a hung %s check before letting a queued retry run", async (kind) => {
    vi.useFakeTimers();
    const h = await importAutoUpdater();
    let requestAborted = false;
    let finishAborting!: () => void;
    const originalRequest = h.autoUpdater.httpExecutor.request;
    originalRequest.mockImplementation(async (_options, token) => {
      try {
        return await token!.createPromise<string>(() => {});
      } finally {
        requestAborted = true;
        await new Promise<void>((resolve) => { finishAborting = resolve; });
      }
    });
    h.autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
      h.updaterEvents.get("checking-for-update")?.();
      await h.autoUpdater.httpExecutor.request({});
    }).mockResolvedValue({ isUpdateAvailable: false, updateInfo: { version: "1.0.0" } });
    const first = kind === "manual" ? h.module.checkForUpdatesNow(stateDir, { requestId: "hung" })
      : kind === "automatic" ? h.module.startAutoUpdates(stateDir) : h.module.returnToHome(stateDir, "hung");
    await vi.advanceTimersByTimeAsync(0);
    const retry = h.module.checkForUpdatesNow(stateDir, { requestId: "retry" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(requestAborted).toBe(true);
    if (kind === "automatic") {
      expect(h.module.getUpdateStatus().state).toBe("checking");
    } else {
      expect(h.module.getUpdateStatus()).toMatchObject({ state: "error", message: expect.stringContaining("timed out") });
    }
    expect(h.autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    finishAborting();
    await first;
    await retry;
    expect(h.autoUpdater.httpExecutor.request).toBe(originalRequest);
    expect(h.autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "not-available", requestId: "retry" });
  });

  it("clears the check deadline before an automatic download continues", async () => {
    vi.useFakeTimers();
    const h = await importAutoUpdater();
    const originalRequest = h.autoUpdater.httpExecutor.request;
    let finishDownload!: () => void;
    h.autoUpdater.checkForUpdates.mockResolvedValue({
      isUpdateAvailable: true, updateInfo: { version: "2.0.0" },
      downloadPromise: new Promise<void>((resolve) => { finishDownload = resolve; }),
    });
    const checking = h.module.startAutoUpdates(stateDir);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.autoUpdater.httpExecutor.request).toBe(originalRequest);
    finishDownload();
    await checking;
  });

  const deferredDownload = () => {
    let finish!: () => void;
    const downloadPromise = new Promise<void>((resolve) => { finish = resolve; });
    return { finish, result: {
      isUpdateAvailable: true,
      updateInfo: { version: "2.0.0" },
      downloadPromise,
    } };
  };
  const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

  it("owns a forced download on the automatic path with auto-download off", async () => {
    const h = await importAutoUpdater({
      enabled: false, channel: "latest", nightlyAck: false, feature: null,
    });
    const { finish, result } = deferredDownload();
    h.autoUpdater.checkForUpdates.mockResolvedValue(result);

    let settled = false;
    const checking = h.module.startAutoUpdates(stateDir).then(() => { settled = true; });
    await settle();

    expect(h.autoUpdater.checkForUpdates).toHaveBeenCalled();
    expect(settled).toBe(false);

    finish();
    await checking;
    expect(settled).toBe(true);
  });

  it("holds a manual check open until a forced download finishes", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    const { finish, result } = deferredDownload();
    autoUpdater.checkForUpdates.mockResolvedValue(result);

    let settled = false;
    const checking = module.checkForUpdatesNow(stateDir).then(() => { settled = true; });
    await settle();
    expect(settled).toBe(false);

    finish();
    await checking;
    expect(settled).toBe(true);
  });

  it("holds return-home open until a forced download finishes", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    const { finish, result } = deferredDownload();
    autoUpdater.checkForUpdates.mockResolvedValue(result);

    let settled = false;
    const returning = module.returnToHome(stateDir).then(() => { settled = true; });
    await settle();
    expect(settled).toBe(false);

    finish();
    await returning;
    expect(settled).toBe(true);
  });

  it("blocks the next queued operation until a forced download finishes", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    const { finish, result } = deferredDownload();
    autoUpdater.checkForUpdates
      .mockResolvedValueOnce(result)
      .mockResolvedValue({ isUpdateAvailable: false, updateInfo: { version: "2.0.0" } });

    const first = module.checkForUpdatesNow(stateDir, { requestId: "one" });
    await settle();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);

    const second = module.checkForUpdatesNow(stateDir, { requestId: "two" });
    await settle();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);

    finish();
    await Promise.all([first, second]);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it("settles a manual check that resolves without emitting any event", async () => {
    const { module, autoUpdater, statusMessages } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockResolvedValue({
      isUpdateAvailable: false,
      updateInfo: { version: "1.0.0" },
    });

    await module.checkForUpdatesNow(stateDir, { requestId: "manual-update-1" });

    expect(module.getUpdateStatus().state).toBe("not-available");
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "not-available",
      checkedAt: expect.any(Number),
    });
  });

  it("downloads an event-less manual offer when automatic downloads are enabled when the feed has a build", async () => {
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockResolvedValue({
      isUpdateAvailable: true,
      updateInfo: { version: "2.5.0" },
    });

    await module.checkForUpdatesNow(stateDir, { requestId: "manual-update-1" });

    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloading",
      version: "2.5.0",
    });
  });

  it("settles an event-less manual check back to the staged build", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });

    autoUpdater.checkForUpdates.mockResolvedValue({
      isUpdateAvailable: false,
      updateInfo: { version: "2.1.0" },
    });
    await module.checkForUpdatesNow(stateDir, { requestId: "manual-update-2" });

    expect(module.getUpdateStatus()).toMatchObject({
      state: "downloaded",
      version: "2.1.0",
    });
  });

  it("remembers a staged build's provenance across a restart", async () => {
    const first = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });
    await first.module.checkForUpdatesNow(stateDir);
    first.updaterEvents.get("update-downloaded")?.({
      version: "2.1.0-nightly.1",
      releaseNotes: "Run &lt;ao update&gt; safely\nImproved update reliability",
    });
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(JSON.parse(readFileSync(nodePath.join(stateDir, "staged-update.json"), "utf8")))
      .toMatchObject({
        version: "2.1.0-nightly.1",
        releaseNotes: "Run <ao update> safely\nImproved update reliability",
      });

    const second = await importAutoUpdaterKeepingStagedFile({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });
    await second.module.startAutoUpdates(stateDir);
    expect(second.module.getUpdateStatus()).toMatchObject({
      releaseNotes: "Run <ao update> safely\nImproved update reliability",
      staged: { version: "2.1.0-nightly.1" },
    });

    const autoDownloadAtCheck: boolean[] = [];
    second.autoUpdater.checkForUpdates.mockImplementation(() => {
      autoDownloadAtCheck.push(second.autoUpdater.autoDownload);
      return Promise.resolve({ isUpdateAvailable: true, updateInfo: { version: "2.0.0" } });
    });
    await second.module.checkForUpdatesNow(stateDir, {
      settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
    });
    expect(autoDownloadAtCheck).toEqual([true]);
  });

  it("backfills notes when an older staged record rediscovers the same nightly", async () => {
    const version = "1.0.1-nightly.202608231517";
    const stagedAt = Date.now() - 60_000;
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
      version,
      stagedAt,
      channel: "nightly",
    }));
    const resourcesPath = mkdtempSync(nodePath.join(os.tmpdir(), "ao-staged-nightly-notes-"));
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(process, "resourcesPath");
    Object.defineProperty(process, "resourcesPath", { configurable: true, value: resourcesPath });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify([{
          tag_name: `v${version}`,
          draft: false,
          prerelease: true,
          body: "<ul><li>Fixed the restart dialog</li><li>Improved update reliability</li></ul>",
          assets: [{ name: "nightly-linux.yml" }],
        }]), { status: 200 }),
      ),
    );

    try {
      const harness = await importAutoUpdaterKeepingStagedFile({
        enabled: false,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });
      await harness.module.startAutoUpdates(stateDir);

      expect(harness.module.getUpdateStatus()).toMatchObject({
        releaseNotes: "- Fixed the restart dialog\n- Improved update reliability",
        staged: { version, stagedAt },
      });
      expect(harness.autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(JSON.parse(readFileSync(nodePath.join(stateDir, "staged-update.json"), "utf8")))
        .toMatchObject({
          version,
          stagedAt,
          releaseNotes: "- Fixed the restart dialog\n- Improved update reliability",
        });
    } finally {
      vi.unstubAllGlobals();
      if (originalResourcesPath) {
        Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      } else {
        Reflect.deleteProperty(process, "resourcesPath");
      }
      rmSync(resourcesPath, { recursive: true, force: true });
    }
  });

  it("keeps staged notes on the staged stamp when a later check offers a newer build", async () => {
    const { module, updaterEvents, statusMessages } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({
      version: "2.0.0",
      releaseNotes: "Staged release notes",
    });

    updaterEvents.get("update-available")?.({
      version: "2.1.0",
      releaseNotes: "Newer offered release notes",
    });
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "available",
      version: "2.1.0",
      releaseNotes: "Newer offered release notes",
      staged: { version: "2.0.0", releaseNotes: "Staged release notes" },
    });

    updaterEvents.get("checking-for-update")?.();
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "checking",
      releaseNotes: "Staged release notes",
      staged: { version: "2.0.0", releaseNotes: "Staged release notes" },
    });
    expect(module.getUpdateStatus()).toMatchObject({
      state: "checking",
      releaseNotes: "Staged release notes",
      staged: { version: "2.0.0", releaseNotes: "Staged release notes" },
    });
  });

  it("keeps staged records without release notes backward compatible", async () => {
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
      version: "2.1.0",
      stagedAt: Date.now(),
      channel: "latest",
    }));

    const harness = await importAutoUpdaterKeepingStagedFile();
    await harness.module.startAutoUpdates(stateDir);

    expect(harness.module.getUpdateStatus()).toMatchObject({
      staged: { version: "2.1.0" },
    });
    expect(harness.module.getUpdateStatus()).not.toHaveProperty("releaseNotes");
  });

  it("drops persisted provenance once that build is the running version", async () => {
    const first = await importAutoUpdater();
    await first.module.checkForUpdatesNow(stateDir);
    first.updaterEvents.get("update-downloaded")?.({ version: "1.0.0" });
    await new Promise((resolve) => setTimeout(resolve, 50));

    const second = await importAutoUpdaterKeepingStagedFile();
    await second.module.startAutoUpdates(stateDir);
    expect(second.module.getUpdateStatus().staged).toBeUndefined();
  });

  it("drops an older staged build from the running channel", async () => {
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
      version: "0.12.13-nightly.202609070711",
      stagedAt: Date.now(),
      channel: "nightly",
    }));

    const harness = await importAutoUpdaterKeepingStagedFile(
      { enabled: false, channel: "nightly", nightlyAck: true, feature: null },
      { version: "0.12.13-nightly.202609070850" },
    );
    await harness.module.startAutoUpdates(stateDir);

    expect(harness.module.getUpdateStatus().staged).toBeUndefined();
  });

  it("drops an older staged build from another channel", async () => {
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({
      version: "0.12.10-nightly.1",
      stagedAt: Date.now(),
      channel: "nightly",
    }));

    const harness = await importAutoUpdaterKeepingStagedFile(
      { enabled: false, channel: "nightly", nightlyAck: true, feature: null },
      { version: "0.12.11" },
    );
    await harness.module.startAutoUpdates(stateDir);

    expect(harness.module.getUpdateStatus().staged).toBeUndefined();
    expect(harness.autoUpdater.autoInstallOnAppQuit).toBe(false);
    await expect(harness.module.quitAndInstallUpdate()).rejects.toThrow(/Check for updates/);
  });

  it("cancels a download that stops advancing and offers a retry", async () => {
    vi.useFakeTimers();
    const { module, autoUpdater, updaterEvents, statusMessages } = await importAutoUpdater();
    const cancel = vi.fn();
    autoUpdater.downloadUpdate.mockImplementation((token: { cancel?: () => void } | undefined) => {
      if (token) token.cancel = cancel;
      return new Promise(() => undefined);
    });

    void module.downloadUpdateNow("manual-download-1");
    await flushMicrotasks();
    updaterEvents.get("download-progress")?.({ percent: 37 });
    expect(statusMessages().at(-1)?.payload).toMatchObject({ state: "downloading", percent: 37 });

    await vi.advanceTimersByTimeAsync(2 * 60 * 1000 - 1);
    expect(cancel).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "error",
      message: "Download stopped responding. Try again.",
    });
  });

  it("keeps a slow but advancing download alive", async () => {
    vi.useFakeTimers();
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    const cancel = vi.fn();
    autoUpdater.downloadUpdate.mockImplementation((token: { cancel?: () => void } | undefined) => {
      if (token) token.cancel = cancel;
      return new Promise(() => undefined);
    });

    void module.downloadUpdateNow();
    await flushMicrotasks();
    for (const percent of [10, 20, 30, 40]) {
      updaterEvents.get("download-progress")?.({ percent });
      await vi.advanceTimersByTimeAsync(90 * 1000);
    }
    expect(cancel).not.toHaveBeenCalled();
  });

  it("does not replace the stall message with a cancellation error", async () => {
    vi.useFakeTimers();
    const { module, autoUpdater, updaterEvents, statusMessages } = await importAutoUpdater();
    autoUpdater.downloadUpdate.mockImplementation((token: { cancel?: () => void } | undefined) => {
      if (token) token.cancel = () => undefined;
      return new Promise(() => undefined);
    });

    void module.downloadUpdateNow();
    await flushMicrotasks();
    updaterEvents.get("download-progress")?.({ percent: 12 });
    await vi.advanceTimersByTimeAsync(2 * 60 * 1000);

    updaterEvents.get("error")?.(new Error("Cancelled"));
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      message: "Download stopped responding. Try again.",
    });
  });

  it("stamps the staged build onto every status, including transient ones", async () => {
    vi.useFakeTimers();
    const stagedAt = new Date("2026-07-17T12:00:00.000Z").getTime();
    vi.setSystemTime(stagedAt);
    const { module, updaterEvents, statusMessages } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("checking-for-update")?.();

    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "checking",
      staged: { version: "2.1.0", stagedAt, escalated: false },
    });
  });

  it("forces the replacement download when a channel switch strands a staged build", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });
    expect(module.getUpdateStatus().state).toBe("downloaded");

    const autoDownloadAtCheck: boolean[] = [];
    autoUpdater.checkForUpdates.mockImplementation(() => {
      autoDownloadAtCheck.push(autoUpdater.autoDownload);
      return Promise.resolve({
        isUpdateAvailable: true,
        updateInfo: { version: "2.0.0" },
      });
    });

    await module.checkForUpdatesNow(stateDir, {
      settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
      requestId: "channel-update-1",
    });

    expect(autoDownloadAtCheck).toEqual([true]);
    expect(module.getUpdateStatus().state).not.toBe("downloaded");
    expect(module.getUpdateStatus().staged).toBeUndefined();
  });

  it("leaves auto-download off for a manual check on the staged build's own channel", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });

    const autoDownloadAtCheck: boolean[] = [];
    autoUpdater.checkForUpdates.mockImplementation(() => {
      autoDownloadAtCheck.push(autoUpdater.autoDownload);
      return Promise.resolve({ isUpdateAvailable: false, updateInfo: { version: "2.1.0-nightly.1" } });
    });

    await module.checkForUpdatesNow(stateDir, {
      settings: { enabled: false, channel: "nightly", nightlyAck: true, feature: null },
      requestId: "manual-update-1",
    });

    expect(autoDownloadAtCheck).toEqual([false]);
    expect(module.getUpdateStatus().state).toBe("downloaded");
  });

  it("supersedes a pinned PR build when returning to the home channel", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
      enabled: false,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 4729 },
    });

    await module.checkForUpdatesNow(stateDir);
    expect(autoUpdater.channel).toBe("pr4729");
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0-pr4729.1" });

    const autoDownloadAtCheck: boolean[] = [];
    autoUpdater.checkForUpdates.mockImplementation(() => {
      autoDownloadAtCheck.push(autoUpdater.autoDownload);
      return Promise.resolve({ isUpdateAvailable: true, updateInfo: { version: "2.0.0" } });
    });

    await module.returnToHome(stateDir, "feature-update-1");

    expect(autoUpdater.channel).toBe("latest");
    expect(autoDownloadAtCheck).toEqual([true]);
    expect(module.getUpdateStatus().staged).toBeUndefined();
  });

  it("keeps the escalation timer off once a stranded build is discarded", async () => {
    vi.useFakeTimers();
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const { module, updaterEvents } = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });
    const armed = setIntervalSpy.mock.results.length;
    expect(armed).toBeGreaterThan(0);

    await module.checkForUpdatesNow(stateDir, {
      settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
    });

    expect(module.getUpdateStatus().staged).toBeUndefined();
    expect(module.getUpdateStatus().stagedAt).toBeUndefined();
  });
});

describe("returnToHome", () => {
  it("clears only the feature pin, preserves home channel/prefs, and checks", async () => {
    const { module, autoUpdater, updateUpdateSettings } =
      await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: { pr: 2270 },
      });

    await module.returnToHome(stateDir, "req-1");

    expect(updateUpdateSettings).toHaveBeenCalledTimes(1);
    const clear = updateUpdateSettings.mock.calls[0]?.[1] as (
      c: UpdateSettings,
    ) => UpdateSettings;
    expect(
      clear({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: { pr: 2270 },
      }),
    ).toEqual({
      enabled: true,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });
    expect(autoUpdater.channel).toBe("nightly");
    expect(autoUpdater.allowPrerelease).toBe(true);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
  });

  it("checks the home channel even when nothing is pinned", async () => {
    const { module, autoUpdater, updateUpdateSettings } =
      await importAutoUpdater({
        enabled: true,
        channel: "latest",
        nightlyAck: false,
        feature: null,
      });

    await module.returnToHome(stateDir);

    const clear = updateUpdateSettings.mock.calls[0]?.[1] as (
      c: UpdateSettings,
    ) => UpdateSettings;
    const current: UpdateSettings = {
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: null,
    };
    expect(clear(current)).toBe(current);
    expect(autoUpdater.channel).toBe("latest");
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
  });

  it("names a failed return-home check as a failed return (#5755)", async () => {
    const { module, autoUpdater } = await importAutoUpdater({
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: { pr: 7 },
    });
    autoUpdater.checkForUpdates.mockRejectedValueOnce(
      Object.assign(new Error("503 Service Unavailable\nHeaders: {}"), { name: "HttpError", statusCode: 503 }),
    );

    await module.returnToHome(stateDir, "req-return");

    expect(module.getUpdateStatus()).toMatchObject({
      state: "error",
      requestId: "req-return",
      message: "Couldn't return to your update channel — the update server isn't available right now. Try again in a few minutes.",
      errorInfo: { kind: "unavailable", phase: "return" },
    });
  });

  it("reports unsupported in dev (unpackaged) without touching settings", async () => {
    const { module, autoUpdater, updateUpdateSettings } =
      await importAutoUpdater(
        {
          enabled: true,
          channel: "latest",
          nightlyAck: false,
          feature: { pr: 2270 },
        },
        { isPackaged: false },
      );

    await module.returnToHome(stateDir, "req-2");

    expect(updateUpdateSettings).not.toHaveBeenCalled();
    expect(autoUpdater.checkForUpdates).not.toHaveBeenCalled();
  });
});

function stubProcess(platform: NodeJS.Platform, execPath: string): () => void {
  const originalPlatform = Object.getOwnPropertyDescriptor(process, "platform")!;
  const originalExecPath = Object.getOwnPropertyDescriptor(process, "execPath")!;
  Object.defineProperty(process, "platform", { value: platform });
  Object.defineProperty(process, "execPath", { value: execPath });
  return () => {
    Object.defineProperty(process, "platform", originalPlatform);
    Object.defineProperty(process, "execPath", originalExecPath);
  };
}

function makeBundle(name = "Agent Orchestrator.app"): { root: string; bundle: string; execPath: string } {
  const root = mkdtempSync(nodePath.join(os.tmpdir(), "ao-updater-perm-"));
  const bundle = nodePath.join(root, name);
  mkdirSync(nodePath.join(bundle, "Contents", "MacOS"), { recursive: true });
  return {
    root,
    bundle,
    execPath: nodePath.join(bundle, "Contents", "MacOS", "agent-orchestrator"),
  };
}

const TRANSLOCATED_EXEC_PATH =
  "/private/var/folders/hg/vkmz93d1T/T/AppTranslocation/0AC4-11EE/d/Agent Orchestrator.app/Contents/MacOS/agent-orchestrator";

describe("quitAndInstallUpdate", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  async function requestQuit(preserveAgents: () => boolean) {
    const requestShutdown = vi.fn(async (_port: number, _body: { stopAgents: boolean; appRunId: string }) => ({ ok: true, status: 202 }));
    await requestDesktopShutdown({
      keepAlive: false,
      appRunId: "run-1",
      status: { state: "ready", pid: 123, port: 3002 },
      preserveAgents,
      readRunInfo: async () => ({ pid: 123, port: 3002, startedAtMs: 0, owner: "app", appRunId: "run-1" }),
      requestShutdown,
    });
    expect(requestShutdown).toHaveBeenCalledOnce();
    return requestShutdown.mock.calls[0][1];
  }

  it.each(["darwin", "win32", "linux"] as const)("preserves agents during update quit and stops them after cancellation on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      let shutdown: ReturnType<typeof requestQuit> | undefined;
      autoUpdater.quitAndInstall.mockImplementation(() => {
        shutdown = requestQuit(module.isUpdateRestartRequested);
      });
      await module.quitAndInstallUpdate();
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledOnce();
      expect(await shutdown).toEqual({ stopAgents: false, appRunId: "run-1" });
      module.clearUpdateRestartRequest();
      expect(await requestQuit(module.isUpdateRestartRequested)).toEqual({ stopAgents: true, appRunId: "run-1" });
      await module.quitAndInstallUpdate();
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(2);
      expect(await shutdown).toEqual({ stopAgents: false, appRunId: "run-1" });
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("stops agents on an ordinary quit after a synchronous install failure on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      autoUpdater.quitAndInstall.mockImplementationOnce(() => { throw new Error("installer failed"); });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow("installer failed");
      expect(await requestQuit(module.isUpdateRestartRequested)).toEqual({ stopAgents: true, appRunId: "run-1" });
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("stops agents on an ordinary quit after an asynchronous install failure on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      const { module, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      const failed = vi.fn();
      module.setUpdateRestartFailureHandler(failed);
      await module.quitAndInstallUpdate();
      updaterEvents.get("error")?.(new Error("installer failed"));
      expect(failed).toHaveBeenCalledOnce();
      expect(await requestQuit(module.isUpdateRestartRequested)).toEqual({ stopAgents: true, appRunId: "run-1" });
    } finally { restore(); }
  });

  it("keeps native staging ready when Finder renamed the running app", async () => {
    const bundle = makeBundle("Agent Orchestrator 2.app");
    const restore = stubProcess("darwin", bundle.execPath);
    vi.spyOn(os, "homedir").mockReturnValue(bundle.root);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const shipIt = nodePath.join(bundle.root, "Library", "Caches", "dev.agent-orchestrator.desktop.ShipIt");
    const extracted = nodePath.join(shipIt, "update.fixture", "Agent Orchestrator.app", "Contents");
    mkdirSync(extracted, { recursive: true });
    writeFileSync(nodePath.join(shipIt, "ShipItState.plist"), "fixture");
    writeFileSync(nodePath.join(extracted, "Info.plist"), "fixture");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      const periodicCheck = intervalWithDelay(setIntervalSpy, 15 * 60 * 1000);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      expect(module.getUpdateStatus().staged?.ready).toBeUndefined();

      periodicCheck();
      await vi.waitFor(() => expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2));
      expect(module.getUpdateStatus().staged?.ready).toBeUndefined();
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();

      startMacUpdateProgress.mockResolvedValueOnce({ assertAlive: vi.fn(), fail: vi.fn(async () => undefined) });
      await module.quitAndInstallUpdate("2.1.0");
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally {
      restore();
      rmSync(bundle.root, { recursive: true, force: true });
    }
  });

  it("drops an in-process macOS stage whose native files disappeared and checks again", async () => {
    const bundle = makeBundle();
    const restore = stubProcess("darwin", bundle.execPath);
    vi.spyOn(os, "homedir").mockReturnValue(bundle.root);
    const shipIt = nodePath.join(bundle.root, "Library", "Caches", "dev.agent-orchestrator.desktop.ShipIt");
    const extracted = nodePath.join(shipIt, "update.fixture", "Agent Orchestrator.app", "Contents");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      expect(module.getUpdateStatus().staged?.ready).toBe(false);
      mkdirSync(extracted, { recursive: true });
      writeFileSync(nodePath.join(shipIt, "ShipItState.plist"), "fixture");
      writeFileSync(nodePath.join(extracted, "Info.plist"), "fixture");
      expect(module.getUpdateStatus().staged?.ready).toBeUndefined();

      rmSync(shipIt, { recursive: true });
      expect(module.getUpdateStatus().staged?.ready).toBe(false);
      autoUpdater.checkForUpdates.mockImplementation(async () => ({
        isUpdateAvailable: true,
        updateInfo: { version: "2.2.0" },
        downloadPromise: autoUpdater.autoDownload ? Promise.resolve() : undefined,
      }));

      await expect(module.quitAndInstallUpdate("2.1.0")).rejects.toThrow(/staged update files are missing/);
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
      expect(autoUpdater.autoDownload).toBe(true);
      expect(module.getUpdateStatus().staged).toBeUndefined();
    } finally {
      restore();
      rmSync(bundle.root, { recursive: true, force: true });
    }
  });

  it("releases a pending install when native staging disappears before the helper responds", async () => {
    const bundle = makeBundle();
    const restore = stubProcess("darwin", bundle.execPath);
    vi.spyOn(os, "homedir").mockReturnValue(bundle.root);
    const shipIt = nodePath.join(bundle.root, "Library", "Caches", "dev.agent-orchestrator.desktop.ShipIt");
    const extracted = nodePath.join(shipIt, "update.fixture", "Agent Orchestrator.app", "Contents");
    mkdirSync(extracted, { recursive: true });
    writeFileSync(nodePath.join(shipIt, "ShipItState.plist"), "fixture");
    writeFileSync(nodePath.join(extracted, "Info.plist"), "fixture");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const progress = { assertAlive: vi.fn(), fail: vi.fn(async () => undefined) };
      const handshake = deferred<typeof progress>();
      startMacUpdateProgress.mockReturnValueOnce(handshake.promise);
      const install = module.quitAndInstallUpdate("2.1.0");
      const abandoned = expect(install).rejects.toThrow(/staged update files are missing/);
      await vi.waitFor(() => expect(startMacUpdateProgress).toHaveBeenCalledOnce());

      rmSync(shipIt, { recursive: true });
      await expect(module.quitAndInstallUpdate("2.1.0")).rejects.toThrow(/staged update files are missing/);
      await abandoned;
      await flushMicrotasks();
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(module.getUpdateStatus().staged).toBeUndefined();

      handshake.resolve(progress);
      await flushMicrotasks();
      expect(progress.assertAlive).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore();
      rmSync(bundle.root, { recursive: true, force: true });
    }
  });

  it("releases a pending install on the next periodic check without another click", async () => {
    const bundle = makeBundle();
    const restore = stubProcess("darwin", bundle.execPath);
    vi.spyOn(os, "homedir").mockReturnValue(bundle.root);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval");
    const shipIt = nodePath.join(bundle.root, "Library", "Caches", "dev.agent-orchestrator.desktop.ShipIt");
    const extracted = nodePath.join(shipIt, "update.fixture", "Agent Orchestrator.app", "Contents");
    mkdirSync(extracted, { recursive: true });
    writeFileSync(nodePath.join(shipIt, "ShipItState.plist"), "fixture");
    writeFileSync(nodePath.join(extracted, "Info.plist"), "fixture");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      const periodicCheck = intervalWithDelay(setIntervalSpy, 15 * 60 * 1000);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const handshake = deferred<Awaited<ReturnType<typeof startMacUpdateProgress>>>();
      startMacUpdateProgress.mockReturnValueOnce(handshake.promise);
      const install = module.quitAndInstallUpdate("2.1.0");
      const abandoned = expect(install).rejects.toThrow(/staged update files are missing/);
      await vi.waitFor(() => expect(startMacUpdateProgress).toHaveBeenCalledOnce());

      periodicCheck();
      await flushMicrotasks();
      expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
      rmSync(shipIt, { recursive: true });
      periodicCheck();
      await abandoned;
      await vi.waitFor(() => expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2));
      expect(module.getUpdateStatus().staged).toBeUndefined();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();

      handshake.resolve({ assertAlive: vi.fn(), fail: vi.fn(async () => undefined) });
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore();
      rmSync(bundle.root, { recursive: true, force: true });
    }
  });

  it("clears a retained restart request when its native staging disappears", async () => {
    const bundle = makeBundle();
    const restore = stubProcess("darwin", bundle.execPath);
    vi.spyOn(os, "homedir").mockReturnValue(bundle.root);
    const shipIt = nodePath.join(bundle.root, "Library", "Caches", "dev.agent-orchestrator.desktop.ShipIt");
    const extracted = nodePath.join(shipIt, "update.fixture", "Agent Orchestrator.app", "Contents");
    mkdirSync(extracted, { recursive: true });
    writeFileSync(nodePath.join(shipIt, "ShipItState.plist"), "fixture");
    writeFileSync(nodePath.join(extracted, "Info.plist"), "fixture");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const progress = { assertAlive: vi.fn(), fail: vi.fn(async () => undefined) };
      startMacUpdateProgress.mockResolvedValueOnce(progress);
      const restoreWindow = vi.fn();
      module.setUpdateRestartFailureHandler(restoreWindow);
      await module.quitAndInstallUpdate("2.1.0");
      expect(module.isUpdateRestartRequested()).toBe(true);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);

      rmSync(shipIt, { recursive: true });
      await expect(module.quitAndInstallUpdate("2.1.0")).rejects.toThrow(/staged update files are missing/);
      expect(module.isUpdateRestartRequested()).toBe(false);
      expect(progress.fail).toHaveBeenCalledWith(expect.stringContaining("staged update files are missing"));
      expect(restoreWindow).toHaveBeenCalledOnce();
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally {
      restore();
      rmSync(bundle.root, { recursive: true, force: true });
    }
  });

  it("requires new confirmation before downloading a changed remembered target", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, updaterEvents, startMacUpdateProgress } = await importAutoUpdater({ enabled: false, channel: "latest", nightlyAck: false, feature: null });
      await module.startAutoUpdates(stateDir);
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.2.0", releaseNotes: "New release B" } });
      autoUpdater.downloadUpdate.mockImplementation(async () => {
        updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
        return [];
      });
      await expect(module.quitAndInstallUpdate("2.1.0")).resolves.toEqual({
        state: "confirmation-required", version: "2.2.0", releaseNotes: "New release B",
      });
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      expect(startMacUpdateProgress).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      await module.quitAndInstallUpdate("2.2.0");
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally { restore(); }
  });

  it("does not install a newer build already staged after the dialog opened", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, startMacUpdateProgress } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.2.0", releaseNotes: "Staged release B" });
      await expect(module.quitAndInstallUpdate("2.1.0")).resolves.toMatchObject({ state: "confirmation-required", version: "2.2.0", releaseNotes: "Staged release B" });
      expect(startMacUpdateProgress).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      await module.quitAndInstallUpdate("2.2.0");
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally { restore(); }
  });

  it("returns a new remembered target for confirmation despite an earlier native error", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater({ enabled: false, channel: "latest", nightlyAck: false, feature: null });
      await module.startAutoUpdates(stateDir);
      nativeAutoUpdater.emit("error", new Error("earlier signature rejected"));
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.2.0" } });
      await expect(module.quitAndInstallUpdate("2.1.0")).resolves.toMatchObject({ state: "confirmation-required", version: "2.2.0" });
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      expect(startMacUpdateProgress).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it("requires confirmation again if the feed changes a second time", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater } = await importAutoUpdater({ enabled: false, channel: "latest", nightlyAck: false, feature: null });
      await module.startAutoUpdates(stateDir);
      for (const [confirmed, offered] of [["2.1.0", "2.2.0"], ["2.2.0", "2.3.0"]]) {
        autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: offered } });
        await expect(module.quitAndInstallUpdate(confirmed)).resolves.toMatchObject({ state: "confirmation-required", version: offered });
      }
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it("prepares a remembered macOS update again before requesting restart", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      const downloaded = deferred();
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" } });
      autoUpdater.downloadUpdate.mockImplementation(async () => {
        updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
        await downloaded.promise;
      });
      const install = module.quitAndInstallUpdate();
      module.clearUpdateRestartRequest();
      const duplicate = module.quitAndInstallUpdate();
      await flushMicrotasks();
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
      const afterDownloadedEvent = module.quitAndInstallUpdate();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      downloaded.resolve();
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      nativeAutoUpdater.emit("update-downloaded");
      await Promise.all([install, duplicate, afterDownloadedEvent]);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledWith(false, true);
    } finally {
      restore();
    }
  });

  it.each(["no longer available", "download failed"])("keeps AO open when restart preparation is %s", async (failure) => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: failure === "download failed", updateInfo: { version: "2.1.0" } });
      autoUpdater.downloadUpdate.mockRejectedValue(new Error("download failed"));
      await expect(module.quitAndInstallUpdate()).rejects.toThrow();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(module.getUpdateStatus().state).toBe("error");
    } finally {
      restore();
    }
  });

  it("rejects a translocated installation without quitting", async () => {
    const restore = stubProcess("darwin", TRANSLOCATED_EXEC_PATH);
    try {
      const { module, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Applications/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it.each(["bundle", "parent"])("rejects an unwritable %s without quitting", async (location) => {
    const { root, bundle, execPath } = makeBundle();
    chmodSync(location === "bundle" ? bundle : root, 0o555);
    const restore = stubProcess("darwin", execPath);
    try {
      const { module, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/writ/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore(); chmodSync(root, 0o755); chmodSync(bundle, 0o755);
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("does not quit without a staged update even in a writable app", async () => {
    const { root, execPath } = makeBundle();
    const restore = stubProcess("darwin", execPath);
    try {
      const { module, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Check for updates/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); rmSync(root, { recursive: true, force: true }); }
  });

  it.each(["error", "timeout"])("does not quit when native preparation ends with %s", async (outcome) => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    vi.useFakeTimers();
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      const install = module.quitAndInstallUpdate();
      const assertion = expect(install).rejects.toThrow();
      await flushMicrotasks();
      if (outcome === "error") nativeAutoUpdater.emit("error", new Error("signature rejected"));
      else await vi.advanceTimersByTimeAsync(180_000);
      await assertion;
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(startMacUpdateProgress).not.toHaveBeenCalled();
      expect(nativeAutoUpdater.listenerCount("update-downloaded")).toBe(1);
      expect(nativeAutoUpdater.listenerCount("error")).toBe(1);
    } finally { vi.useRealTimers(); restore(); }
  });

  it("recovers a remembered native transfer on late success without another download", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    vi.useFakeTimers();
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
      await module.startAutoUpdates(stateDir);
      const transfer = deferred();
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" } });
      autoUpdater.downloadUpdate.mockImplementation(() => {
        updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
        return transfer.promise;
      });
      const assertion = expect(module.quitAndInstallUpdate()).rejects.toThrow(/Relaunch Orchestrator/);
      await flushMicrotasks();
      await vi.advanceTimersByTimeAsync(180_000);
      await assertion;
      nativeAutoUpdater.emit("update-downloaded");
      transfer.resolve();
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      await module.quitAndInstallUpdate("2.1.0");
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
    } finally { vi.useRealTimers(); restore(); }
  });

  it("abandons an install on native failure while the progress helper is starting", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const progress = { assertAlive: vi.fn(), fail: vi.fn(async () => undefined) };
      const handshake = deferred<typeof progress>();
      startMacUpdateProgress.mockReturnValueOnce(handshake.promise);
      const install = module.quitAndInstallUpdate("2.1.0");
      const assertion = expect(install).rejects.toThrow("signature rejected");
      await vi.waitFor(() => expect(startMacUpdateProgress).toHaveBeenCalledOnce());
      nativeAutoUpdater.emit("error", new Error("signature rejected"));
      await assertion;
      handshake.resolve(progress);
      await flushMicrotasks();
      expect(progress.fail).toHaveBeenCalledWith(expect.stringContaining("cancelled"));
      expect(progress.assertAlive).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(module.isUpdateRestartRequested()).toBe(false);
    } finally { restore(); }
  });

  it("ignores duplicate downloads during native preparation and accepts a later replacement", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      autoUpdater.downloadUpdate
        .mockImplementationOnce(async () => { updaterEvents.get("update-downloaded")?.({ version: "2.1.0" }); })
        .mockImplementationOnce(async () => { updaterEvents.get("update-downloaded")?.({ version: "2.2.0" }); });
      const first = module.downloadUpdateNow();
      await flushMicrotasks();
      const second = module.downloadUpdateNow();
      await flushMicrotasks();
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
      nativeAutoUpdater.emit("update-downloaded");
      await first;
      await second;
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
      autoUpdater.autoDownload = false;
      updaterEvents.get("update-available")?.({ version: "2.2.0" });
      const replacement = module.downloadUpdateNow();
      await flushMicrotasks();
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(2);
      const install = module.quitAndInstallUpdate();
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      nativeAutoUpdater.emit("update-downloaded");
      await Promise.all([replacement, install]);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally { restore(); }
  });

  it("serializes a stale-channel replacement even when automatic downloads are disabled", async () => {
    writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "nightly" }));
    const { module, autoUpdater } = await importAutoUpdater({ enabled: false, channel: "latest", nightlyAck: false, feature: null });
    const transfer = deferred();
    autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" }, downloadPromise: transfer.promise, cancellationToken: new CancellationToken() });
    const first = module.startAutoUpdates(stateDir);
    await vi.waitFor(() => expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1));
    expect(autoUpdater.autoDownload).toBe(true);
    const second = module.checkForUpdatesNow(stateDir);
    await flushMicrotasks();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    transfer.resolve();
    await Promise.all([first, second]);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it.each(["check", "return-home"])("keeps provider-owned downloads serialized during %s", async (operation) => {
    const { module, autoUpdater } = await importAutoUpdater();
    await module.startAutoUpdates(stateDir);
    const transfer = deferred();
    autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" }, downloadPromise: transfer.promise, cancellationToken: new CancellationToken() });
    const before = autoUpdater.checkForUpdates.mock.calls.length;
    const first = operation === "check" ? module.checkForUpdatesNow(stateDir) : module.returnToHome(stateDir);
    await flushMicrotasks();
    const second = module.checkForUpdatesNow(stateDir);
    await flushMicrotasks();
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(before + 1);
    transfer.resolve();
    await Promise.all([first, second]);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(before + 2);
  });

  it("recovers the window and helper when the native quit handoff fails asynchronously", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const failed = vi.fn();
      module.setUpdateRestartFailureHandler(failed);
      await module.quitAndInstallUpdate();
      expect(module.isUpdateRestartRequested()).toBe(true);
      nativeAutoUpdater.emit("error", new Error("could not persist the installer request"));
      expect(module.isUpdateRestartRequested()).toBe(false);
      expect(failed).toHaveBeenCalledOnce();
      const progress = await startMacUpdateProgress.mock.results[0].value;
      expect(progress.fail).toHaveBeenCalledWith("could not persist the installer request");
    } finally { restore(); }
  });

  it("rewrites a raw native failure that ends a requested restart (#5755)", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      module.setUpdateRestartFailureHandler(vi.fn());
      await module.quitAndInstallUpdate();
      nativeAutoUpdater.emit("error", new Error("The request timed out."));
      const expected = "Couldn't install the update — the update server timed out. Try again in a few minutes.";
      const progress = await startMacUpdateProgress.mock.results[0].value;
      expect(progress.fail).toHaveBeenCalledWith(expected);
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: expected, errorInfo: { kind: "timeout", phase: "install" } });
    } finally { restore(); }
  });

  it("rewrites a raw native failure that rejects the install while the progress helper is starting (#5755)", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      const progress = { assertAlive: vi.fn(), fail: vi.fn(async () => undefined) };
      const handshake = deferred<typeof progress>();
      startMacUpdateProgress.mockReturnValueOnce(handshake.promise);
      const expected = "Couldn't install the update — the update server timed out. Try again in a few minutes.";
      const assertion = expect(module.quitAndInstallUpdate("2.1.0")).rejects.toThrow(expected);
      await vi.waitFor(() => expect(startMacUpdateProgress).toHaveBeenCalledOnce());
      nativeAutoUpdater.emit("error", new Error("The request timed out."));
      await assertion;
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: expected, errorInfo: { kind: "timeout", phase: "install" } });
      handshake.resolve(progress);
      await flushMicrotasks();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it("requires a visible helper before quitting an already native-ready update", async () => {
    const restore = stubProcess("darwin", "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents, nativeAutoUpdater, startMacUpdateProgress } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.startAutoUpdates(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
      nativeAutoUpdater.emit("update-downloaded");
      startMacUpdateProgress.mockRejectedValueOnce(new Error("helper unavailable"));
      await expect(module.quitAndInstallUpdate()).rejects.toThrow("helper unavailable");
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("rejects an install without a staged build on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      const { module, autoUpdater } = await importAutoUpdater();
      await module.checkForUpdatesNow(stateDir);
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/not ready/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      expect(await requestQuit(module.isUpdateRestartRequested)).toEqual({ stopAgents: true, appRunId: "run-1" });
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("re-downloads a remembered build before install on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: true, updateInfo: { version: "2.1.0" } });
      autoUpdater.downloadUpdate.mockImplementation(async () => {
        updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
        return [];
      });
      await module.quitAndInstallUpdate();
      expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally { restore(); }
  });

  it.each(["darwin", "win32", "linux"] as const)("rejects a remembered-build install with one sentence, not the feed dump, on %s (#5755)", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      const err = releasesFeedTimeout();
      autoUpdater.checkForUpdates.mockRejectedValue(err);

      const rejection = await module.quitAndInstallUpdate().then(() => undefined, (error: unknown) => error);

      expect(rejection).toBeInstanceOf(Error);
      expect((rejection as Error).message).toBe(
        "Couldn't install the update — the update server timed out. Try again in a few minutes.",
      );
      expect(consoleError).toHaveBeenCalledWith("failed to prepare update for restart:", err);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("tells Settings a remembered-build restart failed as an install on %s (#5755)", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      const err = releasesFeedTimeout();
      autoUpdater.checkForUpdates.mockImplementation(() => {
        updaterEvents.get("error")?.(err);
        return Promise.reject(err);
      });

      await expect(module.quitAndInstallUpdate()).rejects.toThrow(
        "Couldn't install the update — the update server timed out. Try again in a few minutes.",
      );
      expect(module.getUpdateStatus()).toMatchObject({
        state: "error",
        message: "Couldn't install the update — the update server timed out. Try again in a few minutes.",
      });
    } finally { restore(); }
  });

  it.each(["win32", "linux"] as const)("throws when a remembered build is no longer available on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      writeFileSync(nodePath.join(stateDir, "staged-update.json"), JSON.stringify({ version: "2.1.0", stagedAt: Date.now(), channel: "latest" }));
      const { module, autoUpdater } = await importAutoUpdater();
      await module.startAutoUpdates(stateDir);
      autoUpdater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: false, updateInfo: { version: "2.1.0" } });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/no longer available/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });

  it("never blocks off macOS, even for translocation-looking paths", async () => {
    const restore = stubProcess("win32", TRANSLOCATED_EXEC_PATH);
    try {
      const { module, autoUpdater, dialog, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });

      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      await module.quitAndInstallUpdate();

      expect(dialog.showMessageBox).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledWith(false, true);
    } finally {
      restore();
    }
  });

  it("does nothing when the app is not packaged", async () => {
    const restore = stubProcess("darwin", TRANSLOCATED_EXEC_PATH);
    try {
      const { module, autoUpdater, dialog } = await importAutoUpdater({
        enabled: true,
        channel: "latest",
        nightlyAck: false,
        feature: null,
      }, { isPackaged: false });

      module.quitAndInstallUpdate();

      expect(dialog.showMessageBox).not.toHaveBeenCalled();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore();
    }
  });
});

describe("install-on-quit policy", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("disables install-on-quit when the location cannot be installed to", async () => {
    const restore = stubProcess("darwin", TRANSLOCATED_EXEC_PATH);
    try {
      const { module, autoUpdater } = await importAutoUpdater();

      await module.startAutoUpdates("/tmp/ao-state");

      expect(autoUpdater.autoInstallOnAppQuit).toBe(false);
    } finally {
      restore();
    }
  });

  it.each(["darwin", "win32", "linux"] as const)("blocks explicit install while a stale build awaits replacement on %s", async (platform) => {
    const restore = stubProcess(platform, "/usr/bin/node");
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
        enabled: false,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });
      expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
      await module.checkForUpdatesNow(stateDir, {
        settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
      });
      expect(autoUpdater.autoInstallOnAppQuit).toBe(false);
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Check for updates/);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally {
      restore();
    }
  });

  it("re-enables install-on-quit once the replacement is staged", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
      enabled: false,
      channel: "nightly",
      nightlyAck: true,
      feature: null,
    });

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });
    await module.checkForUpdatesNow(stateDir, {
      settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
    });
    expect(autoUpdater.autoInstallOnAppQuit).toBe(false);

    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
  });

  it("keeps install-on-quit off for an uninstallable location even once replaced", async () => {
    const restore = stubProcess("darwin", TRANSLOCATED_EXEC_PATH);
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
        enabled: false,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });

      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.1.0-nightly.1" });
      await module.checkForUpdatesNow(stateDir, {
        settings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
      });
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });

      expect(autoUpdater.autoInstallOnAppQuit).toBe(false);
    } finally {
      restore();
    }
  });

  it("leaves install-on-quit on for an installable location", async () => {
    const { root, execPath } = makeBundle();
    const restore = stubProcess("darwin", execPath);
    try {
      const { module, autoUpdater } = await importAutoUpdater();

      await module.startAutoUpdates("/tmp/ao-state");

      expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
    } finally {
      restore();
      rmSync(root, { recursive: true, force: true });
    }
  });
});

describe("channel downgrade safety", () => {
  const nightly = { enabled: false, channel: "nightly", nightlyAck: true, feature: null } as const;
  const stable = { enabled: false, channel: "latest", nightlyAck: false, feature: null } as const;
  const running = "0.12.11-nightly.202609051608";

  function serveVersion(harness: Awaited<ReturnType<typeof importAutoUpdater>>, version: string, installed = running) {
    harness.autoUpdater.checkForUpdates.mockImplementation(async () => {
      const available = semver.gt(version, installed) ||
        (harness.autoUpdater.allowDowngrade && semver.lt(version, installed));
      harness.updaterEvents.get(available ? "update-available" : "update-not-available")?.({ version });
      return { isUpdateAvailable: available, updateInfo: { version } };
    });
  }

  it.each([
    { settings: nightly, installed: "0.13.0", offered: "0.13.1-nightly.202609221741" },
    { settings: stable, installed: "0.13.1-nightly.202609221741", offered: "0.13.1" },
  ])("allows $installed to switch to newer $offered", async ({ settings, installed, offered }) => {
    const h = await importAutoUpdater(settings, { version: installed });
    serveVersion(h, offered, installed);
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "available", version: offered });
    expect(h.autoUpdater.allowDowngrade).toBe(false);
  });

  it("waits when Nightly is older than installed Stable", async () => {
    const h = await importAutoUpdater(nightly, { version: "0.13.0" });
    serveVersion(h, "0.13.0-nightly.202609141844", "0.13.0");
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
    expect(h.autoUpdater.allowDowngrade).toBe(false);
  });

  it.each([false, true])("waits for a newer Stable on startup and later checks (automatic download %s)", async (enabled) => {
    const h = await importAutoUpdater({ ...stable, enabled }, { version: running });
    serveVersion(h, "0.12.10");
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "unsupported", message: expect.stringContaining("older than the installed build") });
    expect(h.autoUpdater.autoDownload).toBe(enabled);
    await h.module.checkForUpdatesNow(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
    expect(h.autoUpdater.allowDowngrade).toBe(false);
    expect(h.autoUpdater.downloadUpdate).not.toHaveBeenCalled();
  });

  it("retains a saved channel switch after its first check fails", async () => {
    const h = await importAutoUpdater(nightly, { version: running });
    h.writeUpdateSettings.mockImplementation(async (_dir, settings) => {
      h.readUpdateSettings.mockResolvedValue(settings);
    });
    h.autoUpdater.checkForUpdates.mockRejectedValueOnce(new Error("network unavailable"));
    await h.module.checkForUpdatesNow(stateDir, { settings: stable });
    expect(h.module.getUpdateStatus().state).toBe("error");
    serveVersion(h, "0.12.10");
    await h.module.checkForUpdatesNow(stateDir);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "unsupported", checkedAt: expect.any(Number) });
    expect(h.module.getUpdateStatus().checkError).toBeUndefined();
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
  });

  it("stops allowing downgrades after relaunching into the selected Stable channel", async () => {
    const h = await importAutoUpdater(stable, { version: "0.12.10" });
    serveVersion(h, "0.12.9", "0.12.10");
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("not-available");
    expect(h.autoUpdater.allowDowngrade).toBe(false);
  });

  it.each([
    { settings: stable, installed: "0.12.11", offered: "0.12.10" },
    { settings: { ...stable, feature: { pr: 4900 } }, installed: "0.12.11-pr4900.2", offered: "0.12.11-pr4900.1" },
  ])("rejects routine older releases on $installed", async ({ settings, installed, offered }) => {
    const h = await importAutoUpdater(settings, { version: installed });
    serveVersion(h, offered, installed);
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("not-available");
    await h.module.checkForUpdatesNow(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("not-available");
  });

  it("does not redownload a staged candidate after rejecting an older manifest", async () => {
    const h = await importAutoUpdater({ ...nightly, enabled: true }, { version: running });
    await h.module.checkForUpdatesNow(stateDir);
    h.updaterEvents.get("update-downloaded")?.({ version: "0.12.11-nightly.202609061608" });
    serveVersion(h, "0.12.11-nightly.202609041608");
    await h.module.startAutoUpdates(stateDir);
    expect(h.autoUpdater.autoDownload).toBe(false);
    expect(h.autoUpdater.downloadUpdate).not.toHaveBeenCalled();
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "downloaded", version: "0.12.11-nightly.202609061608" });
  });

  it.each([
    { settings: stable, installed: "0.12.10", offered: "0.12.11" },
    { settings: nightly, installed: running, offered: "0.12.11-nightly.202609061608" },
  ])("offers newer releases on $installed without downgrade permission", async ({ settings, installed, offered }) => {
    const h = await importAutoUpdater(settings, { version: installed });
    serveVersion(h, offered, installed);
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "available", version: offered });
    await h.module.checkForUpdatesNow(stateDir);
    expect(h.module.getUpdateStatus()).toMatchObject({ state: "available", version: offered });
    expect(h.autoUpdater.allowDowngrade).toBe(false);
  });

  it("keeps an explicit channel switch pending until its release is newer", async () => {
    const h = await importAutoUpdater(stable, { version: running });
    serveVersion(h, "0.12.10");
    await h.module.setUpdateSettings(stateDir, stable);
    await h.module.checkForUpdatesNow(stateDir, { settings: stable });
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
    expect(h.autoUpdater.allowDowngrade).toBe(false);
  });

  it("returns an installed feature build home after its pin is retired", async () => {
    const installed = "0.12.11-pr4900.2";
    const h = await importAutoUpdater(stable, { version: installed });
    serveVersion(h, "0.12.10", installed);
    await h.module.startAutoUpdates(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
  });

  it("waits for a newer home release when returning from nightly", async () => {
    const h = await importAutoUpdater(stable, { version: running });
    serveVersion(h, "0.12.10");
    await h.module.returnToHome(stateDir);
    expect(h.module.getUpdateStatus().state).toBe("unsupported");
  });

  it("does not permit a same-channel settings check to downgrade nightly", async () => {
    const h = await importAutoUpdater(nightly, { version: running });
    serveVersion(h, "0.12.11-nightly.202609041608");
    await h.module.checkForUpdatesNow(stateDir, { settings: nightly });
    expect(h.module.getUpdateStatus().state).toBe("not-available");
  });

  it("keeps first-run nightly on nightly when automatic downloads are declined", async () => {
    const h = await importAutoUpdater(stable, { version: running });
    h.dialog.showMessageBox.mockResolvedValue({ response: 1 });
    await h.module.ensureUpdatePrefs(stateDir);
    expect(h.writeUpdateSettings).toHaveBeenCalledWith(stateDir, nightly);
  });

  it("preselects nightly for the first-run channel choice on a nightly install", async () => {
    const h = await importAutoUpdater(stable, { version: running });
    h.dialog.showMessageBox
      .mockResolvedValueOnce({ response: 0 })
      .mockResolvedValueOnce({ response: 1 })
      .mockResolvedValueOnce({ response: 0 });
    await h.module.ensureUpdatePrefs(stateDir);
    expect(h.dialog.showMessageBox.mock.calls[1]?.[0]).toMatchObject({ defaultId: 1, cancelId: 1 });
    expect(h.writeUpdateSettings).toHaveBeenCalledWith(stateDir, { ...nightly, enabled: true });
  });

  it("preserves existing explicit stable preferences on a nightly install", async () => {
    const h = await importAutoUpdater(stable, { version: running });
    writeFileSync(nodePath.join(stateDir, "update-settings.json"), JSON.stringify(stable));
    await h.module.ensureUpdatePrefs(stateDir);
    expect(h.dialog.showMessageBox).not.toHaveBeenCalled();
    expect(h.writeUpdateSettings).not.toHaveBeenCalled();
  });
});

describe("staged install rejection", () => {
  const rejection = new Error(
    "Code signature at URL file:///Users/x/Library/Caches/dev.agent-orchestrator.desktop.ShipIt/" +
      "update.M9ZvE0X/Agent%20Orchestrator.app/ did not pass validation: " +
      "code failed to satisfy specified code requirement(s)",
  );

  it("keeps the verified download on a first failure and re-stages instead", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);

    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    expect(module.getUpdateStatus().staged).toBeUndefined();
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "error",
      message: expect.stringContaining("prepare it again"),
    });
    consoleErrorSpy.mockRestore();
  });

  it("discards the download once the same build fails a second time", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);

    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "error",
      message: expect.stringContaining("stopped retrying on its own"),
    });

    await module.checkForUpdatesNow(stateDir);
    expect(autoUpdater.downloadedUpdateHelper.clear).toHaveBeenCalledTimes(1);
    consoleErrorSpy.mockRestore();
  });

  const failTwice = (
    updaterEvents: Map<string, (...args: unknown[]) => unknown>,
  ) => {
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);
  };

  it("stops automatically re-downloading a build that failed twice", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    failTwice(updaterEvents);

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(false);
    consoleErrorSpy.mockRestore();
  });

  it("gives a different build its own recovery attempts", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    failTwice(updaterEvents);
    updaterEvents.get("update-available")?.({ version: "2.2.0" });

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(true);
    consoleErrorSpy.mockRestore();
  });

  const nativeBusyError = Object.assign(
    new Error("The command is disabled and cannot be executed"),
    { code: 1, domain: "RACCommandErrorDomain" },
  );

  it("does not report a refused native handoff as an update failure", async () => {
    const consoleInfoSpy = vi
      .spyOn(console, "info")
      .mockImplementation(() => undefined);
    const { module, updaterEvents, statusMessages } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    const beforeRefusal = statusMessages().at(-1)?.payload;

    updaterEvents.get("error")?.(nativeBusyError);

    expect(statusMessages().at(-1)?.payload).toEqual(beforeRefusal);
    expect(module.getUpdateStatus().state).not.toBe("error");
    consoleInfoSpy.mockRestore();
  });

  it("settles a manual check whose native handoff was refused", async () => {
    const consoleInfoSpy = vi
      .spyOn(console, "info")
      .mockImplementation(() => undefined);
    const { module, autoUpdater } = await importAutoUpdater();
    autoUpdater.checkForUpdates.mockRejectedValue(nativeBusyError);

    await module.checkForUpdatesNow(stateDir, { requestId: "manual-1" });

    expect(module.getUpdateStatus().state).toBe("not-available");
    consoleInfoSpy.mockRestore();
  });

  it("restores the budget when the user checks again", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    failTwice(updaterEvents);
    await module.checkForUpdatesNow(stateDir);

    await module.startAutoUpdates(stateDir);

    expect(autoUpdater.autoDownload).toBe(true);
    consoleErrorSpy.mockRestore();
  });

  it("keeps the actionable message when one rejection is delivered twice", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const consoleDebugSpy = vi
      .spyOn(console, "debug")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents, statusMessages } =
      await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);
    const afterFirstDelivery = statusMessages().at(-1)?.payload;

    updaterEvents.get("error")?.(rejection);

    expect(statusMessages().at(-1)?.payload).toEqual(afterFirstDelivery);
    expect(statusMessages().at(-1)?.payload).toMatchObject({
      state: "error",
      message: expect.stringContaining("prepare it again"),
    });
    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    consoleDebugSpy.mockRestore();
    consoleErrorSpy.mockRestore();
  });

  it("does not carry a previous build's failure over to a new one", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(rejection);
    updaterEvents.get("update-downloaded")?.({ version: "2.2.0" });
    updaterEvents.get("error")?.(rejection);

    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });

  it("surfaces the failure even on an automatic check", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });

    autoUpdater.checkForUpdates.mockImplementationOnce(() => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("error")?.(rejection);
      return Promise.resolve();
    });
    await module.startAutoUpdates(stateDir);

    expect(module.getUpdateStatus().state).toBe("error");
    expect(module.getUpdateStatus().staged).toBeUndefined();
    consoleErrorSpy.mockRestore();
  });

  it("also handles the 'not signed at all' wording", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(
      new Error(
        "Code signature at URL file:///Users/graycup/Library/Caches/" +
          "dev.agent-orchestrator.desktop.ShipIt/update.lmaIGgc/Agent%20Orchestrator.app/ " +
          "did not pass validation: code object is not signed at all",
      ),
    );

    expect(module.getUpdateStatus().staged).toBeUndefined();
    expect(module.getUpdateStatus().state).toBe("error");
    consoleErrorSpy.mockRestore();
  });

  it("also handles 'failed to get static code for bundle'", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(
      new Error(
        "Failed to get static code for bundle file:///Users/x/Library/Caches/" +
          "dev.agent-orchestrator.desktop.ShipIt/update.M9ZvE0X/Agent%20Orchestrator.app/",
      ),
    );

    expect(module.getUpdateStatus().staged).toBeUndefined();
    expect(module.getUpdateStatus().state).toBe("error");
    consoleErrorSpy.mockRestore();
  });

  it("ignores an unrelated error that only mentions the ShipIt path", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(
      new Error(
        "EACCES: permission denied, open '/Users/x/Library/Caches/" +
          "dev.agent-orchestrator.desktop.ShipIt/ShipItState.plist'",
      ),
    );

    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    expect(module.getUpdateStatus().staged).toBeDefined();
    consoleErrorSpy.mockRestore();
  });

  it("leaves an ordinary download failure alone", async () => {
    const consoleErrorSpy = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("error")?.(new Error("net::ERR_CONNECTION_RESET"));

    expect(autoUpdater.downloadedUpdateHelper.clear).not.toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });
});

describe("e2e staging sentinel", () => {
  const originalSentinel = process.env.AO_E2E_UPDATE_SENTINEL;
  afterEach(() => {
    if (originalSentinel === undefined) delete process.env.AO_E2E_UPDATE_SENTINEL;
    else process.env.AO_E2E_UPDATE_SENTINEL = originalSentinel;
  });

  it("writes the sentinel from the NATIVE updater's update-downloaded", async () => {
    const sentinel = nodePath.join(stateDir, "sentinel.json");
    process.env.AO_E2E_UPDATE_SENTINEL = sentinel;
    const { module, nativeUpdaterEvents, updaterEvents } =
      await importAutoUpdater(undefined, { nativeReadyManually: true });

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(existsSync(sentinel)).toBe(false);

    nativeUpdaterEvents.get("update-downloaded")?.({}, "notes", "2.1.0");
    expect(JSON.parse(readFileSync(sentinel, "utf8"))).toEqual({
      stagedAt: expect.any(Number),
      releaseName: "2.1.0",
    });
  });

  it("registers no sentinel writer when the env var is unset", async () => {
    delete process.env.AO_E2E_UPDATE_SENTINEL;
    const sentinel = nodePath.join(stateDir, "sentinel.json");
    const { module, nativeUpdaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    process.env.AO_E2E_UPDATE_SENTINEL = sentinel;
    nativeUpdaterEvents.get("update-downloaded")?.({}, "notes", "2.1.0");
    delete process.env.AO_E2E_UPDATE_SENTINEL;
    expect(existsSync(sentinel)).toBe(false);
  });
});

describe("renderer delivery does not depend on the window registry", () => {
  it("pushes status and telemetry through the shell sink, never through BrowserWindow", async () => {
    const { module, autoUpdater, updaterEvents, rendererSend, BrowserWindow, statusMessages, telemetryMessages } =
      await importAutoUpdater();

    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    updaterEvents.get("error")?.(new Error("net::ERR_CONNECTION_RESET"));

    expect(BrowserWindow.getAllWindows).not.toHaveBeenCalled();
    expect(statusMessages().length).toBeGreaterThan(0);
    expect(telemetryMessages().length).toBeGreaterThan(0);
    expect(rendererSend).toHaveBeenCalledWith("updates:status", expect.anything());
    expect(rendererSend).toHaveBeenCalledWith("updates:telemetry", expect.anything());
    expect(autoUpdater.checkForUpdates).toHaveBeenCalled();
  });

  it("resolves the sink per send, so a recreated shell still receives pushes", async () => {
    const { module, updaterEvents } = await importAutoUpdater();
    const first: unknown[] = [];
    const second: unknown[] = [];
    let current = first;
    module.setRendererSink(() => ({ send: (_c: string, p: unknown) => current.push(p) }));

    await module.checkForUpdatesNow(stateDir);
    expect(first.length).toBeGreaterThan(0);

    current = second;
    updaterEvents.get("update-downloaded")?.({ version: "2.1.0" });
    expect(second.length).toBeGreaterThan(0);
  });

  it("does not throw when no sink is installed yet", async () => {
    const { module } = await importAutoUpdater();
    module.setRendererSink(() => null);
    await expect(module.checkForUpdatesNow(stateDir)).resolves.toBeUndefined();
  });
});

describe("live download-to-install flow", () => {
  it("publishes starting immediately and refuses duplicate download requests", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-available")?.({ version: "2.0.0" });
    let finish!: () => void;
    autoUpdater.downloadUpdate.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
    const download = module.downloadUpdateNow();
    expect(module.getUpdateStatus()).toMatchObject({ state: "downloading", version: "2.0.0" });
    expect(module.getUpdateStatus().percent).toBeUndefined();
    await module.downloadUpdateNow();
    expect(autoUpdater.downloadUpdate).toHaveBeenCalledTimes(1);
    updaterEvents.get("download-progress")?.({ percent: 42.8, transferred: 428, total: 1000 });
    expect(module.getUpdateStatus()).toMatchObject({ state: "downloading", percent: 42, transferred: 428, total: 1000 });
    finish();
    await download;
  });

  it("does not offer installation at 100% or before native macOS readiness", async () => {
    const restore = stubProcess("darwin", process.execPath);
    try {
      const { module, updaterEvents, nativeUpdaterEvents, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("download-progress")?.({ percent: 100, transferred: 1000, total: 1000 });
      expect(module.getUpdateStatus().state).toBe("preparing");
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      expect(module.getUpdateStatus()).toMatchObject({ state: "preparing", staged: { ready: false } });
      const install = module.quitAndInstallUpdate();
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
      nativeUpdaterEvents.get("update-downloaded")?.({}, "notes", "2.0.0");
      expect(module.getUpdateStatus().state).toBe("downloaded");
      await install;
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    } finally { restore(); }
  });

  it("never advances the successful check timestamp for a failure", async () => {
    const { module, updaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-not-available")?.();
    const checkedAt = module.getUpdateStatus().checkedAt;
    updaterEvents.get("error")?.(new Error("offline"));
    expect(module.getUpdateStatus()).toMatchObject({ state: "error", checkedAt });
  });
});

it("recovers a timed-out native preparation when its native success arrives late", async () => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, nativeUpdaterEvents, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(3 * 60_000);
    expect(module.getUpdateStatus()).toMatchObject({ state: "error", staged: { ready: false } });
    expect(module.getUpdateStatus().message).toContain("Relaunch Orchestrator");
    await expect(module.quitAndInstallUpdate()).rejects.toThrow(/Relaunch Orchestrator/);
    expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    nativeUpdaterEvents.get("update-downloaded")?.({}, "notes", "2.0.0");
    expect(module.getUpdateStatus().state).toBe("downloaded");
    await module.quitAndInstallUpdate("2.0.0");
    expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
    expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
    const records = readFileSync(nodePath.join(stateDir, "logs", "updater.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
    expect(records).toEqual(expect.arrayContaining([
      expect.objectContaining({ event: "native-preparation-started", version: "2.0.0", initialBytes: 1, runningVersion: "1.0.0" }),
      expect.objectContaining({ event: "native-preparation-sample", bytes: 1, elapsedMs: 10_000 }),
      expect.objectContaining({ event: "native-preparation-watchdog", reason: "inactivity", elapsedMs: 180_000, inactiveMs: 180_000 }),
      expect.objectContaining({ event: "operation-blocked", requestedOperation: "manual-install" }),
      expect.objectContaining({ event: "native-update-downloaded", accepted: true, preparationBlocked: true }),
    ]));
  } finally { restore(); vi.useRealTimers(); }
});

it.each(["plateau", "no-signal", "growing"])("excludes a 16-minute suspend from native preparation deadlines (%s)", async signal => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, nativeAutoUpdater, powerMonitor, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => signal === "no-signal" ? undefined : signal === "growing" ? Date.now() : 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(2_000);
    powerMonitor.emit("suspend");
    vi.setSystemTime(Date.now() + 16 * 60_000);
    powerMonitor.emit("resume");
    await vi.advanceTimersByTimeAsync(8_000);
    expect(module.getUpdateStatus().state).toBe("preparing");
    await vi.advanceTimersByTimeAsync(1_000);
    nativeAutoUpdater.emit("update-downloaded", {}, "notes", "2.0.0");
    expect(module.getUpdateStatus().state).toBe("downloaded");
    await module.quitAndInstallUpdate("2.0.0");
    expect(autoUpdater.quitAndInstall).toHaveBeenCalledTimes(1);
  } finally { restore(); vi.useRealTimers(); }
});

it("still times out active work after resume", async () => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, powerMonitor } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    const probe = vi.fn(() => 1);
    module.__setStagingProbesForTesting({ readStagingBytes: probe });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(170_000);
    powerMonitor.emit("suspend");
    vi.setSystemTime(Date.now() + 16 * 60_000);
    powerMonitor.emit("resume");
    powerMonitor.emit("resume");
    await vi.advanceTimersByTimeAsync(80_000);
    expect(module.getUpdateStatus().state).toBe("preparing");
    await vi.advanceTimersByTimeAsync(10_000);
    expect(module.getUpdateStatus().state).toBe("error");
  } finally { restore(); vi.useRealTimers(); }
});

it.each(["no-events", "missing-suspend", "missing-resume"])("survives a 16-minute timer gap with %s", async events => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, nativeAutoUpdater, powerMonitor, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(2_000);
    if (events === "missing-resume") powerMonitor.emit("suspend");
    vi.setSystemTime(Date.now() + 16 * 60_000);
    if (events === "missing-suspend") powerMonitor.emit("resume");
    await vi.advanceTimersByTimeAsync(8_000);
    expect(module.getUpdateStatus().state).toBe("preparing");
    await vi.advanceTimersByTimeAsync(1_000);
    nativeAutoUpdater.emit("update-downloaded", {}, "notes", "2.0.0");
    await module.quitAndInstallUpdate("2.0.0");
    expect(autoUpdater.quitAndInstall).toHaveBeenCalledOnce();
    const records = readFileSync(nodePath.join(stateDir, "logs", "updater.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
    expect(records).toContainEqual(expect.objectContaining(events === "missing-resume"
      ? { event: "native-preparation-resumed", source: "watchdog-tick" }
      : { event: "native-preparation-clock-gap", excludedMs: 16 * 60_000 }));
  } finally { restore(); vi.useRealTimers(); }
});

it.each(["stale-suspend", "no-events", "backwards-clock"])("keeps real stalls bounded with %s", async events => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, powerMonitor } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    if (events === "stale-suspend") powerMonitor.emit("suspend");
    else vi.setSystemTime(Date.now() + (events === "backwards-clock" ? -1 : 1) * 16 * 60_000);
    await vi.advanceTimersByTimeAsync(190_000);
    expect(module.getUpdateStatus().state).toBe("error");
  } finally { restore(); vi.useRealTimers(); }
});

it.each([true, false])("retains the absolute active-time cap across repeated suspends (power events: %s)", async powerEvents => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, powerMonitor } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    let bytes = 0;
    module.__setStagingProbesForTesting({ readStagingBytes: () => ++bytes });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    for (let sleep = 0; sleep < 2; sleep++) {
      await vi.advanceTimersByTimeAsync(7 * 60_000);
      if (powerEvents) powerMonitor.emit("suspend");
      vi.setSystemTime(Date.now() + 16 * 60_000);
      if (powerEvents) powerMonitor.emit("resume");
      expect(module.getUpdateStatus().state).toBe("preparing");
    }
    await vi.advanceTimersByTimeAsync(50_000);
    expect(module.getUpdateStatus().state).toBe("preparing");
    await vi.advanceTimersByTimeAsync(10_000);
    expect(module.getUpdateStatus().state).toBe("error");
  } finally { restore(); vi.useRealTimers(); }
});

it.each(["wrong-version", "native-error", "channel-change"])("does not recover a timeout after %s", async invalidation => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, nativeAutoUpdater, autoUpdater } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(180_000);
    if (invalidation === "native-error") nativeAutoUpdater.emit("error", new Error("codesign rejected"));
    if (invalidation === "channel-change") module.configureFeed({ channel: "nightly", feature: null });
    nativeAutoUpdater.emit("update-downloaded", {}, "notes", invalidation === "wrong-version" ? "3.0.0" : "2.0.0");
    expect(module.getUpdateStatus().state).toBe("error");
    await expect(module.quitAndInstallUpdate()).rejects.toThrow();
    expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
  } finally { restore(); vi.useRealTimers(); }
});

it("persists the original native verification error before rewriting its user message", async () => {
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents, nativeUpdaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    nativeUpdaterEvents.get("error")?.(Object.assign(new Error("codesign: designated requirement rejected"), { code: -1 }));
    const records = readFileSync(nodePath.join(stateDir, "logs", "updater.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
    expect(records).toEqual(expect.arrayContaining([
      expect.objectContaining({ event: "native-error", targetVersion: "2.0.0", userMessage: expect.stringContaining("Couldn't finish preparing"), error: expect.objectContaining({ message: "codesign: designated requirement rejected", code: "-1" }) }),
    ]));
    expect(records.some(record => record.event === "native-preparation-watchdog")).toBe(false);
  } finally { restore(); }
});

it.each(["no-signal-cap", "absolute-cap"])("records the %s watchdog branch", async reason => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    const startedAt = Date.now();
    module.__setStagingProbesForTesting({ readStagingBytes: () => reason === "no-signal-cap" ? undefined : Date.now() - startedAt });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync((reason === "no-signal-cap" ? 6 : 15) * 60_000);
    expect(module.getUpdateStatus()).toMatchObject({ state: "error", staged: { ready: false } });
    const records = readFileSync(nodePath.join(stateDir, "logs", "updater.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
    expect(records).toEqual(expect.arrayContaining([expect.objectContaining({ event: "native-preparation-watchdog", reason })]));
  } finally { restore(); vi.useRealTimers(); }
});

it("gives the signature-verification plateau a grace before calling a stall", async () => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const { module, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({ readStagingBytes: () => 1 });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(2 * 60_000);
    expect(module.getUpdateStatus().state).not.toBe("error");
    await vi.advanceTimersByTimeAsync(2 * 60_000);
    expect(module.getUpdateStatus()).toMatchObject({ state: "error", staged: { ready: false } });
  } finally { restore(); vi.useRealTimers(); }
});

it("keeps a slow stage alive past the no-signal cap once staging bytes start growing", async () => {
  vi.useFakeTimers();
  const restore = stubProcess("darwin", process.execPath);
  try {
    const start = Date.now();
    const { module, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({
      readStagingBytes: () => {
        const elapsed = Date.now() - start;
        if (elapsed < 60_000) return undefined;
        return Math.floor(elapsed / 1000);
      },
    });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(module.getUpdateStatus().state).not.toBe("error");
  } finally { restore(); vi.useRealTimers(); }
});

it("sizes the staging disk requirement from the downloaded archive", async () => {
  const restore = stubProcess("darwin", process.execPath);
  try {
    const required: number[] = [];
    const { module, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({
      stagingDiskIsFull: (bytes: number) => { required.push(bytes); return false; },
    });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({
      version: "2.0.0",
      files: [{ url: "AO.zip", size: 300 * 1024 * 1024 }],
    });
    expect(required.at(-1)).toBe(3 * 300 * 1024 * 1024);
  } finally { restore(); }
});

it("falls back to the 2 GiB cap when the archive size is unknown", async () => {
  const restore = stubProcess("darwin", process.execPath);
  try {
    const required: number[] = [];
    const { module, updaterEvents } = await importAutoUpdater(undefined, { nativeReadyManually: true });
    module.__setStagingProbesForTesting({
      stagingDiskIsFull: (bytes: number) => { required.push(bytes); return false; },
    });
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
    expect(required.at(-1)).toBe(2 * 1024 * 1024 * 1024);
  } finally { restore(); }
});

describe("disk preflight refusal (#6607)", () => {
  const DISK_MESSAGE = "Not enough disk space to install the update. Free up space, then try again.";
  const offer = (
    autoUpdater: AutoUpdaterMock,
    updaterEvents: Map<string, UpdaterEventHandler>,
    version: string,
    releaseNotes?: string,
  ) => {
    autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
      updaterEvents.get("checking-for-update")?.();
      updaterEvents.get("update-available")?.({ version, releaseNotes });
      return { isUpdateAvailable: true, updateInfo: { version, releaseNotes } };
    });
  };

  it("keeps checking after a refusal and names the newer build without downloading it", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const restore = stubProcess("darwin", process.execPath);
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      module.__setStagingProbesForTesting({ stagingDiskIsFull: () => true });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: DISK_MESSAGE, staged: { version: "2.0.0", ready: false } });
      expect(autoUpdater.autoInstallOnAppQuit).toBe(false);

      vi.setSystemTime(Date.now() + 60 * 60_000);
      offer(autoUpdater, updaterEvents, "2.1.0");
      await expect(module.checkForUpdatesNow(stateDir)).resolves.toBeUndefined();

      const status = module.getUpdateStatus();
      expect(status).toMatchObject({ state: "error", message: DISK_MESSAGE, version: "2.1.0" });
      expect(status.checkedAt).toBe(Date.now());
      expect(autoUpdater.autoDownload).toBe(false);
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      await module.downloadUpdateNow();
      expect(autoUpdater.downloadUpdate).not.toHaveBeenCalled();
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: DISK_MESSAGE });
    } finally { restore(); vi.useRealTimers(); }
  });

  it("stages again in the same process once space is freed", async () => {
    const restore = stubProcess("darwin", process.execPath);
    try {
      let diskFull = true;
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      module.__setStagingProbesForTesting({ stagingDiskIsFull: () => diskFull });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      expect(module.getUpdateStatus().state).toBe("error");

      diskFull = false;
      autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
        expect(autoUpdater.autoDownload).toBe(true);
        updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
        return { isUpdateAvailable: true, updateInfo: { version: "2.0.0" } };
      });
      await module.checkForUpdatesNow(stateDir);
      expect(module.getUpdateStatus()).toMatchObject({ state: "downloaded", version: "2.0.0" });
      expect(module.getUpdateStatus().staged?.ready).toBeUndefined();
      expect(autoUpdater.autoInstallOnAppQuit).toBe(true);
    } finally { restore(); }
  });

  it("keeps the refusal when the automatic nightly check falls back to API discovery", async () => {
    const restore = stubProcess("darwin", process.execPath);
    const resourcesPath = mkdtempSync(nodePath.join(os.tmpdir(), "ao-nightly-disk-"));
    writeFileSync(
      nodePath.join(resourcesPath, "app-update.yml"),
      "provider: github\nowner: Untrivial-ai\nrepo: agent-orchestrator\n",
    );
    const originalResourcesPath = Object.getOwnPropertyDescriptor(process, "resourcesPath");
    Object.defineProperty(process, "resourcesPath", { configurable: true, value: resourcesPath });
    vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => new Response(
      JSON.stringify([{
        tag_name: "v1.0.1-nightly.202608231517",
        draft: false,
        prerelease: true,
        assets: [{ name: "nightly-mac.yml" }],
      }]),
      { status: 200 },
    )));
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater({
        enabled: true,
        channel: "nightly",
        nightlyAck: true,
        feature: null,
      });
      module.__setStagingProbesForTesting({ stagingDiskIsFull: () => true });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "1.0.1-nightly.202608231350" });
      expect(module.getUpdateStatus()).toMatchObject({ state: "error", message: DISK_MESSAGE });

      autoUpdater.checkForUpdates.mockRejectedValue(new Error("request timed out"));
      await module.startAutoUpdates(stateDir);

      expect(module.getUpdateStatus()).toMatchObject({
        state: "error",
        message: DISK_MESSAGE,
        version: "1.0.1-nightly.202608231517",
      });
    } finally {
      if (originalResourcesPath) Object.defineProperty(process, "resourcesPath", originalResourcesPath);
      else Reflect.deleteProperty(process, "resourcesPath");
      rmSync(resourcesPath, { recursive: true, force: true });
      restore();
    }
  });

  it("still refuses an explicit restart while the disk is full", async () => {
    const restore = stubProcess("darwin", process.execPath);
    try {
      const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
      module.__setStagingProbesForTesting({ stagingDiskIsFull: () => true });
      await module.checkForUpdatesNow(stateDir);
      updaterEvents.get("update-downloaded")?.({ version: "2.0.0" });
      await expect(module.quitAndInstallUpdate()).rejects.toThrow(DISK_MESSAGE);
      expect(autoUpdater.quitAndInstall).not.toHaveBeenCalled();
    } finally { restore(); }
  });
});

describe("release notes while a newer build replaces a staged one (#6607)", () => {
  it("shows the downloading build's notes and keeps the staged notes on the stamp", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0", releaseNotes: "Notes for 2.0.0" });
    expect(module.getUpdateStatus()).toMatchObject({ releaseNotes: "Notes for 2.0.0", staged: { releaseNotes: "Notes for 2.0.0" } });

    let notesWhileDownloading: unknown;
    let stagedNotesWhileDownloading: unknown;
    autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
      updaterEvents.get("update-available")?.({ version: "2.1.0", releaseNotes: "Notes for 2.1.0" });
      return { isUpdateAvailable: true, updateInfo: { version: "2.1.0", releaseNotes: "Notes for 2.1.0" } };
    });
    autoUpdater.downloadUpdate.mockImplementationOnce(async () => {
      updaterEvents.get("download-progress")?.({ percent: 40, transferred: 40, total: 100 });
      const status = module.getUpdateStatus();
      expect(status).toMatchObject({ state: "downloading", version: "2.1.0" });
      notesWhileDownloading = status.releaseNotes;
      stagedNotesWhileDownloading = status.staged?.releaseNotes;
    });
    await module.checkForUpdatesNow(stateDir);

    expect(autoUpdater.downloadUpdate).toHaveBeenCalled();
    expect(notesWhileDownloading).toBe("Notes for 2.1.0");
    expect(stagedNotesWhileDownloading).toBe("Notes for 2.0.0");
  });

  it("never shows the staged build's notes for a newer build with none of its own", async () => {
    const { module, autoUpdater, updaterEvents } = await importAutoUpdater();
    await module.checkForUpdatesNow(stateDir);
    updaterEvents.get("update-downloaded")?.({ version: "2.0.0", releaseNotes: "Notes for 2.0.0" });

    let notesWhileDownloading: unknown = "unset";
    autoUpdater.checkForUpdates.mockImplementationOnce(async () => {
      updaterEvents.get("update-available")?.({ version: "2.1.0" });
      return { isUpdateAvailable: true, updateInfo: { version: "2.1.0" } };
    });
    autoUpdater.downloadUpdate.mockImplementationOnce(async () => {
      updaterEvents.get("download-progress")?.({ percent: 40, transferred: 40, total: 100 });
      notesWhileDownloading = module.getUpdateStatus().releaseNotes;
    });
    await module.checkForUpdatesNow(stateDir);
    expect(notesWhileDownloading).toBeUndefined();
  });
});


it("keeps expected update outages out of handled captures and reports genuine faults", async () => {
  const { module } = await importAutoUpdater();
  const { setMainErrorSink, resetMainErrorCaptureForTests } = await import("./main-error-capture");
  const sink = vi.fn(); setMainErrorSink(sink);
  try {
    for (const error of [new Error("ETIMEDOUT"), new Error("ENOTFOUND"), ...[429, 404, 503].map(statusCode => Object.assign(new Error("updater request failed"), { statusCode }))]) module.reportUpdateFailure(error, "check");
    expect(sink).not.toHaveBeenCalled();
    module.reportUpdateFailure(Object.assign(new Error("private signature details"), { code: "ERR_UPDATER_INVALID_SIGNATURE" }), "install", "stage");
    expect(sink).toHaveBeenCalledWith(expect.objectContaining({ category: "update", reason: "stage", detail: "kind:invalid-signature" }));
    module.reportUpdateFailure(new Error("unexpected staging failure"), "install");
    expect(sink).toHaveBeenCalledTimes(2);
  } finally { resetMainErrorCaptureForTests(); }
});
