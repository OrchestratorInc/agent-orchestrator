import { autoUpdater as stockAutoUpdater } from "electron-updater";
import { StableGitHubProvider } from "./stable-github-provider";
import { MacDifferentialV2Updater } from "./mac-differential-v2-updater";
import macV2TrustedKeys from "../../scripts/mac-differential-v2-trust.json";
import macDifferentialRollout from "../../scripts/mac-differential-rollout.json";
import { CancellationToken } from "builder-util-runtime";
import { app, dialog, powerMonitor, autoUpdater as nativeAutoUpdater } from "electron";
import { startMacUpdateProgress } from "./mac-update-progress";
import { markUpdateRelaunch } from "./update-relaunch-flag";
import { createUpdateDiagnostics } from "./update-diagnostics";
import { accessSync, constants as fsConstants, existsSync, lstatSync, readFileSync, readdirSync, statfsSync, writeFileSync } from "node:fs";
import { mkdir, unlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import semver from "semver";
import { AO_BUNDLE_ID } from "./stale-app-copies";
import type { RequestOptions } from "node:http";
import {
  readUpdateSettings,
  macDifferentialUpdatesEnabled,
  updateUpdateSettings,
  writeUpdateSettings,
  UPDATE_SETTINGS_FILE_NAME,
  type UpdateChannel,
  type UpdateSettings,
  type UpdateStatus,
  type UpdateInstallResult,
} from "./update-settings";
import { reconcileFeaturePin } from "./feature-builds";
import { captureMainError, type MainErrorReason } from "./main-error-capture";
import { evaluateEscalation } from "./escalation-evaluator";
import {
  boundPlainReleaseNotes,
  isNetErrorMessage,
  normalizeReleaseNotes,
  updateFailureOutcome,
  updateFailureCategory,
  type UpdateOutcome,
  type UpdatePhase,
  type UpdateTrigger,
} from "../shared/update-telemetry";
import {
  describeUpdateFailure,
  isManifest404Error,
  looksLikeRawUpdateError,
  updateErrorText,
  userFacingUpdateError,
  type UpdateErrorPhase,
  type UpdateFailure,
} from "../shared/update-error-message";

const autoUpdater = process.platform === "darwin" && macDifferentialRollout.enabled === true
  ? new MacDifferentialV2Updater({ trustedKeys: macV2TrustedKeys })
  : stockAutoUpdater;

const FAIL_CLOSED_UPDATE_SETTINGS: UpdateSettings = {
  enabled: false,
  channel: "latest",
  nightlyAck: false,
  feature: null,
  macDifferentialUpdates: false,
};
let lastAppliedUpdateSettings: UpdateSettings = FAIL_CLOSED_UPDATE_SETTINGS;
let developerModeHydrated = false;
let developerModeRequested = false;
let differentialEligible = false;
let pendingTargetBytes: number | undefined;
let offeredUpdateVersion: string | undefined;
let offeredMacFiles: Array<{ url: string; size?: number }> = [];

function selectMacTargetBytes(arm64: boolean): void {
  const hasArm64 = offeredMacFiles.some(file => file.url.includes("arm64"));
  const file = offeredMacFiles.find(file =>
    /\.zip(?:$|[?#])/i.test(file.url) && file.url.includes("arm64") === (arm64 && hasArm64));
  pendingTargetBytes = typeof file?.size === "number" && Number.isFinite(file.size) && file.size >= 0
    ? file.size : undefined;
}
let transferObservation = {
  eligible: false,
  attemptedDifferential: false,
  fallback: false,
  transferred: undefined as number | undefined,
};
let updaterLoggerWired = false;

if (process.platform === "darwin") autoUpdater.disableDifferentialDownload = true;

export function applyUpdaterPolicy(
  settings: UpdateSettings,
  platform: NodeJS.Platform = process.platform,
): void {
  lastAppliedUpdateSettings = settings;
  const eligible = macDifferentialRollout.enabled === true && developerModeHydrated && macDifferentialUpdatesEnabled({ platform, settings });
  differentialEligible = eligible;
  if (platform === "darwin") autoUpdater.disableDifferentialDownload = !eligible;
  console.info("[auto-updater] mac differential policy", {
    eligible,
    platform,
    channel: settings.channel,
    featurePinned: settings.feature !== null,
    developerMode: settings.macDifferentialUpdates === true,
  });
}

function wireUpdaterLogger(): void {
  if (updaterLoggerWired || process.platform !== "darwin" || macDifferentialRollout.enabled !== true) return;
  updaterLoggerWired = true;
  const base = autoUpdater.logger ?? console;
  const observe = (level: "info" | "warn" | "error" | "debug", first: unknown) => {
    const message = typeof first === "string" ? first : "";
    if (message === "Checked for macOS Rosetta environment (isRosetta=true)" ||
        message === "Checked 'uname -a': arm64=true") {
      selectMacTargetBytes(true);
    }
    if (message.startsWith("Download block maps") || message.startsWith("Differential download:")) {
      transferObservation.attemptedDifferential = true;
      base.info("[auto-updater] differential transfer attempted");
    } else if (/(?:fall(?:ing)? back|fallback) to full download/i.test(message)) {
      transferObservation.fallback = true;
      base.warn("[auto-updater] differential transfer fell back to full download");
    } else if (level === "warn" || level === "error") {
      base[level](`[auto-updater] ${updateFailureCategory(message)}`);
    }
  };
  autoUpdater.logger = {
    info: (first: unknown) => observe("info", first),
    warn: (first: unknown) => observe("warn", first),
    error: (first: unknown) => observe("error", first),
    debug: (first: unknown) => observe("debug", first),
  };
}

async function reconcileAndPersist(
  stateDir: string,
  settings: UpdateSettings,
): Promise<UpdateSettings> {
  const checkedPr = settings.feature?.pr;
  const rec = await reconcileFeaturePin(settings);
  if (!rec.cleared || checkedPr === undefined) return rec.settings;

  let cleared = false;
  const latest = await updateUpdateSettings(stateDir, (current) => {
    if (current.feature?.pr !== checkedPr) return current;
    cleared = true;
    return { ...current, feature: null };
  });
  if (cleared) {
    console.info(
      "[feature-builds] pinned PR retired; cleared pin, falling back to home channel",
    );
  }
  return latest;
}

export function configureFeed(
  settings: Pick<UpdateSettings, "channel" | "feature">,
): void {
  if (settings.channel === "latest" && !settings.feature) {
    directFeedReleaseNotes = undefined;
    directFeedDiscoveredAvailable = undefined;
  }
  const coordinates = readAppUpdateYml();
  if (coordinates) {
    autoUpdater.setFeedURL(settings.channel === "latest" && !settings.feature
      ? { provider: "custom", updateProvider: StableGitHubProvider, ...coordinates }
      : { provider: "github", ...coordinates });
  }
  if (settings.feature !== null && settings.feature !== undefined) {
    autoUpdater.channel = `pr${settings.feature.pr}`;
    autoUpdater.allowPrerelease = true;
    autoUpdater.allowDowngrade = false;
    return;
  }

  const channel: UpdateChannel = settings.channel;
  autoUpdater.channel = channel;
  autoUpdater.allowPrerelease = channel === "nightly";
  autoUpdater.allowDowngrade = false;
}

let lastStatus: UpdateStatus = { state: "idle" };
let independentStatusRevision = 0;
let eventsWired = false;
let lastCheckError: UpdateFailure | undefined;

let stagedVersion: string | undefined;
let stagedReleaseNotes: string | undefined;
let stagedInCurrentProcess = false;
let macRestartPreparation: Promise<UpdateInstallResult> | undefined;
let updateRestartRequested = false;
let restartFailureHandler: (() => void) | undefined;
let macRestartProgress: Awaited<ReturnType<typeof startMacUpdateProgress>> | undefined;
let nativeReadyVersion: string | undefined;
let nativePreparationError: Error | undefined;
const STAGE_POLL_INTERVAL_MS = 10_000;
const STAGE_TIMER_GAP_MS = 3 * STAGE_POLL_INTERVAL_MS;
const STAGE_INACTIVITY_TIMEOUT_MS = 90_000;
const STAGE_VERIFY_GRACE_MS = 3 * 60_000;
const STAGE_NO_SIGNAL_CAP_MS = 6 * 60_000;
const STAGE_ABSOLUTE_CAP_MS = 15 * 60_000;
const STAGE_ARCHIVE_EXPANSION_FACTOR = 3;
const STAGE_FREE_BYTES_FLOOR = 512 * 1024 * 1024;
const STAGE_FREE_BYTES_CAP = 2 * 1024 * 1024 * 1024;

function requiredFreeBytesToStage(archiveBytes: number | undefined): number {
  if (!archiveBytes || archiveBytes <= 0) return STAGE_FREE_BYTES_CAP;
  const derived = archiveBytes * STAGE_ARCHIVE_EXPANSION_FACTOR;
  return Math.min(STAGE_FREE_BYTES_CAP, Math.max(STAGE_FREE_BYTES_FLOOR, derived));
}
const STAGE_STALL_MESSAGE = "Couldn't finish preparing the update. Orchestrator stayed open. Relaunch Orchestrator to try again.";
const STAGE_DISK_MESSAGE = "Not enough disk space to install the update. Free up space, then try again.";
let nativePreparationBlocked: Error | undefined;
let stagingDiskShortfall: { requiredBytes: number; error: Error } | undefined;
let rejectNativeOperation: ((error: Error) => void) | undefined;
let nativePreparation: { version: string; promise: Promise<void>; resume(): void; finish(error?: Error): void } | undefined;
let timedOutNativePreparation: { version: string; error: Error } | undefined;
let preparationSuspendedAt: number | undefined;
let preparationSuspendedMs = 0;

function preparationNow(): number {
  return (preparationSuspendedAt ?? Date.now()) - preparationSuspendedMs;
}

function macShipItDir(): string | undefined {
  if (process.platform !== "darwin") return undefined;
  try {
    const dir = path.join(os.homedir(), "Library", "Caches", `${AO_BUNDLE_ID}.ShipIt`);
    return existsSync(dir) ? dir : undefined;
  } catch { return undefined; }
}

function shipItStagingBytes(dir: string | undefined): number | undefined {
  if (dir === undefined) return undefined;
  let total = 0;
  let seen = 0;
  let readErrors = 0;
  let firstReadError: unknown;
  const budget = 20_000;
  const walk = (d: string): void => {
    let names: string[];
    try { names = readdirSync(d); } catch (error) {
      readErrors += 1;
      firstReadError ??= error;
      return;
    }
    for (const name of names) {
      if (seen >= budget) return;
      seen += 1;
      const full = path.join(d, name);
      let st;
      try { st = lstatSync(full); } catch (error) {
        readErrors += 1;
        firstReadError ??= error;
        continue;
      }
      if (st.isSymbolicLink()) continue;
      if (st.isDirectory()) walk(full);
      else total += st.size;
    }
  };
  walk(dir);
  recordUpdateDiagnostic("native-staging-probe", { entries: seen, bytes: total, readErrors, budgetReached: seen >= budget }, firstReadError);
  return seen === 0 ? undefined : total;
}

function insufficientDiskForStaging(requiredBytes: number): boolean {
  if (process.platform !== "darwin") return false;
  try {
    const target = macShipItDir() ?? path.join(os.homedir(), "Library", "Caches");
    const { bavail, bsize } = statfsSync(target);
    return Number(bavail) * Number(bsize) < requiredBytes;
  } catch { return false; }
}

function shortStagingMessage(err: unknown): string {
  const raw = updateErrorText(err);
  if (/no space left on device/i.test(raw)) return STAGE_DISK_MESSAGE;
  if (/ditto:|pkzip|code ?signature|codesign|failed to (?:extract|unzip)/i.test(raw)) return STAGE_STALL_MESSAGE;
  return raw;
}

function blockNativePreparation(message: string): void {
  nativePreparationBlocked = new Error(message);
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = false;
  activeDownloadCancellation?.cancel();
}

function reprobeStagingDisk(): void {
  if (stagingDiskShortfall === undefined || stagingDiskIsFull(stagingDiskShortfall.requiredBytes)) return;
  if (nativePreparationError === stagingDiskShortfall.error) nativePreparationError = undefined;
  stagingDiskShortfall = undefined;
  stagedInCurrentProcess = false;
}

let readStagingBytes: (dir: string | undefined) => number | undefined = shipItStagingBytes;
let stagingDiskIsFull: (requiredBytes: number) => boolean = insufficientDiskForStaging;
function nativeStagingFilesPresent(): boolean {
  const bundle = path.resolve(process.execPath, "..", "..", "..");
  if (!app.isPackaged || !bundle.endsWith(".app")) return true;
  const directory = macShipItDir();
  if (!directory || !existsSync(path.join(directory, "ShipItState.plist"))) return false;
  try {
    return readdirSync(directory, { withFileTypes: true }).some(update => {
      if (!update.isDirectory() || !/^update\.[^/]+$/.test(update.name)) return false;
      try {
        return readdirSync(path.join(directory, update.name), { withFileTypes: true }).some(candidate =>
          candidate.isDirectory() && candidate.name.endsWith(".app") &&
          existsSync(path.join(directory, update.name, candidate.name, "Contents", "Info.plist")));
      } catch { return false; }
    });
  } catch { return false; }
}
export function __setStagingProbesForTesting(probes: {
  readStagingBytes?: (dir: string | undefined) => number | undefined;
  stagingDiskIsFull?: (requiredBytes: number) => boolean;
}): void {
  if (probes.readStagingBytes) readStagingBytes = probes.readStagingBytes;
  if (probes.stagingDiskIsFull) stagingDiskIsFull = probes.stagingDiskIsFull;
}

function beginNativePreparation(version: string, archiveBytes?: number): void {
  if (nativePreparationBlocked) {
    recordUpdateDiagnostic("native-preparation-refused", { version, reason: "blocked" }, nativePreparationBlocked);
    return;
  }
  if (nativePreparation) {
    recordUpdateDiagnostic("native-preparation-refused", { version, reason: "already-preparing", preparingVersion: nativePreparation.version });
    nativePreparationBlocked = new Error(STAGE_STALL_MESSAGE);
    nativePreparation.finish(nativePreparationBlocked);
    return;
  }
  const requiredBytes = requiredFreeBytesToStage(archiveBytes);
  if (stagingDiskIsFull(requiredBytes)) {
    recordUpdateDiagnostic("native-preparation-disk-full", { version, archiveBytes, requiredFreeBytes: requiredBytes, ...stagingDiagnosticSnapshot() });
    const error = new Error(STAGE_DISK_MESSAGE);
    stagingDiskShortfall = { requiredBytes, error };
    autoUpdater.autoDownload = false;
    autoUpdater.autoInstallOnAppQuit = false;
    nativeReadyVersion = undefined;
    nativePreparationError = error;
    broadcast(stagedDownloadedStatus());
    return;
  }
  stagingDiskShortfall = undefined;
  nativeReadyVersion = undefined;
  nativePreparationError = undefined;
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  void promise.catch(() => undefined);
  const preparation = {
    version, promise,
    resume() {
      stagingDir = macShipItDir();
      lastProgressAt = preparationNow();
      lastTickAt = Date.now();
    },
    finish(error?: Error) {
      if (nativePreparation !== preparation) return;
      recordUpdateDiagnostic("native-preparation-finished", { version, elapsedMs: preparationNow() - startedAt, succeeded: error === undefined }, error);
      clearInterval(watchdog);
      nativePreparation = undefined;
      nativePreparationError = error;
      if (error) {
        nativeReadyVersion = undefined;
        rejectNativeOperation?.(error);
        reject(error);
      } else { nativeReadyVersion = version; resolve(); }
    },
  };
  let stagingDir = macShipItDir();
  const startedAt = preparationNow();
  let lastTickAt = Date.now();
  let lastBytes = readStagingBytes(stagingDir);
  let lastProgressAt = startedAt;
  recordUpdateDiagnostic("native-preparation-started", {
    version, archiveBytes, initialBytes: lastBytes ?? null,
    inactivityTimeoutMs: STAGE_INACTIVITY_TIMEOUT_MS, verifyGraceMs: STAGE_VERIFY_GRACE_MS,
    noSignalCapMs: STAGE_NO_SIGNAL_CAP_MS, absoluteCapMs: STAGE_ABSOLUTE_CAP_MS,
    ...stagingDiagnosticSnapshot(),
  });
  const trip = (reason: string): void => {
    recordUpdateDiagnostic("native-preparation-watchdog", {
      version, reason, elapsedMs: preparationNow() - startedAt,
      inactiveMs: preparationNow() - lastProgressAt, lastProgressBytes: lastBytes ?? null,
      ...stagingDiagnosticSnapshot(),
    });
    console.error(`native update preparation stalled after ${Math.round((preparationNow() - startedAt) / 1000)}s with no staging progress`);
    captureMainError({ category: "update", reason: "stage", detail: "kind:stalled" });
    blockNativePreparation(STAGE_STALL_MESSAGE);
    timedOutNativePreparation = { version, error: nativePreparationBlocked! };
    preparation.finish(nativePreparationBlocked);
    broadcast(stagedDownloadedStatus());
  };
  const watchdog = setInterval(() => {
    const wallNow = Date.now();
    const tickGapMs = wallNow - lastTickAt;
    lastTickAt = wallNow;
    if (preparationSuspendedAt !== undefined) {
      const suspendedMs = Math.max(0, wallNow - preparationSuspendedAt);
      preparationSuspendedMs += suspendedMs;
      preparationSuspendedAt = undefined;
      preparation.resume();
      recordUpdateDiagnostic("native-preparation-resumed", { suspendedMs, source: "watchdog-tick" });
    } else if (tickGapMs > STAGE_TIMER_GAP_MS || tickGapMs < 0) {
      const excludedMs = tickGapMs < 0 ? tickGapMs : tickGapMs - STAGE_POLL_INTERVAL_MS;
      preparationSuspendedMs += excludedMs;
      preparation.resume();
      recordUpdateDiagnostic("native-preparation-clock-gap", { tickGapMs, excludedMs });
    }
    const now = preparationNow();
    if (stagingDir === undefined) stagingDir = macShipItDir();
    const bytes = readStagingBytes(stagingDir);
    if (bytes !== undefined && (lastBytes === undefined || bytes > lastBytes)) {
      lastBytes = bytes;
      lastProgressAt = now;
    }
    const haveSignal = bytes !== undefined;
    recordUpdateDiagnostic("native-preparation-sample", {
      version, elapsedMs: now - startedAt, inactiveMs: now - lastProgressAt,
      bytes: bytes ?? null, lastProgressBytes: lastBytes ?? null,
      ...stagingDiagnosticSnapshot(),
    });
    const plateauStalled =
      haveSignal &&
      now - lastProgressAt >= STAGE_INACTIVITY_TIMEOUT_MS &&
      now - startedAt >= STAGE_VERIFY_GRACE_MS;
    if (
      now - startedAt >= STAGE_ABSOLUTE_CAP_MS ||
      plateauStalled ||
      (!haveSignal && now - startedAt >= STAGE_NO_SIGNAL_CAP_MS)
    ) {
      trip(now - startedAt >= STAGE_ABSOLUTE_CAP_MS ? "absolute-cap" : plateauStalled ? "inactivity" : "no-signal-cap");
    }
  }, STAGE_POLL_INTERVAL_MS);
  watchdog.unref?.();
  nativePreparation = preparation;
}

function isNativeInstallReady(): boolean {
  return process.platform !== "darwin" || (stagedInCurrentProcess && nativeReadyVersion !== undefined && nativeReadyVersion === stagedVersion);
}

function nativeStagingWasLost(): boolean {
  return process.platform === "darwin" && isNativeInstallReady() &&
    !nativeStagingFilesPresent();
}

function discardLostNativeStaging(): boolean {
  if (!nativeStagingWasLost()) return false;
  recordUpdateDiagnostic("native-staging-lost", { version: stagedVersion });
  const failure = new Error("The staged update files are missing. Orchestrator is checking for a fresh update. Confirm installation again when it is ready.");
  const restartWasRequested = updateRestartRequested;
  discardStagedBuild();
  updateRestartRequested = false;
  macRestartPreparation = undefined;
  if (activeUpdaterOperation === "manual-install") rejectNativeOperation?.(failure);
  void macRestartProgress?.fail(failure.message).catch(() => undefined);
  macRestartProgress = undefined;
  if (restartWasRequested) restartFailureHandler?.();
  applyInstallOnQuitPolicy();
  broadcastUpdaterStatus({ state: "checking" });
  return true;
}
let offeredReleaseNotes: string | undefined;
let directFeedReleaseNotes: string | undefined;
let directFeedDiscoveredAvailable: { version: string; notes?: string } | undefined;
let stagedChannel: string | undefined;
let stagedAtMs: number | undefined;
let stagedEscalated = false;
let stagedRequestId: string | undefined;
let escalationTimer: ReturnType<typeof setInterval> | undefined;
let escalationStateDir: string | undefined;
let updateDiagnostics: { stateDir: string; record: ReturnType<typeof createUpdateDiagnostics> } | undefined;

export function recordUpdateDiagnostic(
  event: string,
  fields: Record<string, string | number | boolean | null | undefined> = {},
  error?: unknown,
): void {
  try {
    if (!escalationStateDir) return;
    if (updateDiagnostics?.stateDir !== escalationStateDir) {
      updateDiagnostics = { stateDir: escalationStateDir, record: createUpdateDiagnostics(escalationStateDir, app.getVersion()) };
      updateDiagnostics.record("diagnostics-started");
    }
    updateDiagnostics.record(event, {
      operation: activeUpdaterOperation, requestId: activeUpdaterRequestId,
      phase: activeUpdaterPhase, targetVersion: nativePreparation?.version ?? pendingUpdateVersion ?? stagedVersion,
      channel: autoUpdater.channel, stagedInCurrentProcess,
      nativeReadyVersion, preparationBlocked: nativePreparationBlocked !== undefined,
      status: lastStatus.state, restartRequested: updateRestartRequested,
      ...fields,
    }, error);
  } catch { }
}

function stagingDiagnosticSnapshot(): Record<string, number | boolean | undefined> {
  try {
    const directory = macShipItDir();
    const { bavail, bsize } = statfsSync(directory ?? path.join(os.homedir(), "Library", "Caches"));
    const updates: number[] = [];
    if (directory !== undefined) {
      for (const name of readdirSync(directory).filter(name => name.startsWith("update.")).slice(0, 20)) {
        try {
          const stat = lstatSync(path.join(directory, name));
          if (stat.isDirectory()) updates.push(stat.birthtimeMs);
        } catch { }
      }
    }
    return {
      freeBytes: Number(bavail) * Number(bsize), stagingDirectoryPresent: directory !== undefined,
      updateDirectoryCount: updates.length,
      newestUpdateDirectoryBirthtimeMs: updates.length ? Math.max(...updates) : undefined,
    };
  } catch { return { snapshotReadable: false }; }
}
const AUTOMATIC_UPDATE_CHECK_INTERVAL_MS = 15 * 60 * 1000;
const AUTOMATIC_UPDATE_RETRY_CAP_MS = 4 * 60 * 60 * 1000;
let automaticRetryFailures = 0;
let automaticUpdateTimer: ReturnType<typeof setInterval> | undefined;
let automaticUpdateTimerIntervalMs: number | undefined;
type UpdaterOperation =
  | "automatic-check"
  | "manual-check"
  | "manual-download"
  | "manual-install"
  | "settings-write"
  | "return-home"
  | "cache-clear";
let activeUpdaterOperation: UpdaterOperation | undefined;
let activeUpdaterRequestId: string | undefined;
let automaticCheckPreviousStatus:
  { status: UpdateStatus; independentRevision: number } | undefined;
let updaterOperationQueue: Promise<void> = Promise.resolve();
let automaticCheckInFlight = false;
const STALE_CHECK_NUDGE_THRESHOLD = 3;
let consecutiveAutomaticNetFailures = 0;
const FAILING_CHECK_THRESHOLD = 3;
let consecutiveAutomaticCheckFailures = 0;
let automaticCheckFailureCounted = false;
let failingChecksPublished = false;
let automaticCheckNetFailureCounted = false;
let activeUpdaterPhase: UpdatePhase = "check";
let pendingUpdateVersion: string | undefined;
const DOWNLOAD_STALL_TIMEOUT_MS = 2 * 60 * 1000;
let downloadStallTimer: ReturnType<typeof setTimeout> | undefined;
let activeDownloadCancellation: CancellationToken | undefined;
let downloadStalled = false;
let manualDownloadPending = false;

function clearDownloadStallWatchdog(): void {
  if (downloadStallTimer !== undefined) {
    clearTimeout(downloadStallTimer);
    downloadStallTimer = undefined;
  }
  activeDownloadCancellation = undefined;
}

function armDownloadStallWatchdog(): void {
  if (downloadStallTimer !== undefined) clearTimeout(downloadStallTimer);
  downloadStallTimer = setTimeout(() => {
    downloadStallTimer = undefined;
    downloadStalled = true;
    console.error("update download stalled; cancelling");
    recordUpdateDiagnostic("download-watchdog", { version: pendingUpdateVersion });
    activeDownloadCancellation?.cancel();
    activeDownloadCancellation = undefined;
    emitUpdateOutcome(
      updateFailureOutcome("download stalled", "download", activeUpdateTrigger(), pendingUpdateVersion),
    );
    broadcast(
      withActiveRequest({
        state: "error",
        message: "Download stopped responding. Try again.",
        ...(pendingUpdateVersion === undefined ? {} : { version: pendingUpdateVersion }),
      }),
    );
  }, DOWNLOAD_STALL_TIMEOUT_MS);
  downloadStallTimer.unref?.();
}

let lastCheckedAtMs: number | undefined;

type RendererSink = { send: (channel: string, payload: unknown) => void };
let resolveRendererSink: () => RendererSink | null | undefined = () => undefined;

export function setRendererSink(resolve: () => RendererSink | null | undefined): void {
  resolveRendererSink = resolve;
}

function sendToRenderer(channel: string, payload: unknown): void {
  resolveRendererSink()?.send(channel, payload);
}

function emitUpdateOutcome(outcome: UpdateOutcome): void {
  if (outcome.phase === "download" && process.platform === "darwin") {
    outcome = {
      ...outcome,
      differential_eligible: transferObservation.eligible,
      transfer_mode: transferObservation.attemptedDifferential ? "differential" : "full",
      fallback: transferObservation.fallback,
      ...(transferObservation.transferred === undefined ? {} : { transferred_bytes: transferObservation.transferred }),
      ...(pendingTargetBytes === undefined ? {} : { target_bytes: pendingTargetBytes }),
    };
  }
  sendToRenderer("updates:telemetry", outcome);
}

function activeUpdateTrigger(): UpdateTrigger {
  return activeUpdaterOperation === "automatic-check" ? "automatic" : "manual";
}

function emitUpdateFailure(err: unknown): void {
  recordUpdateDiagnostic("update-failure", {}, err);
  const message = updateErrorText(err);
  emitUpdateOutcome(
    updateFailureOutcome(message, activeUpdaterPhase, activeUpdateTrigger(), pendingUpdateVersion),
  );
}

function broadcast(
  status: UpdateStatus,
  owner: "independent" | "automatic-operation" = "independent",
): void {
  const safeStatus: UpdateStatus =
    status.state === "error" && status.message !== undefined && looksLikeRawUpdateError(status.message)
      ? { ...status, ...updateErrorFields(status.message, failedUpdatePhase()) }
      : status;
  const statusWithCheckTime: UpdateStatus =
    lastCheckedAtMs === undefined || safeStatus.checkedAt !== undefined
      ? safeStatus
      : { ...safeStatus, checkedAt: lastCheckedAtMs };
  const { releaseNotes: _ignored, ...statusWithoutNotes } = statusWithCheckTime;
  const stamped: UpdateStatus = {
    ...statusWithoutNotes,
    ...stagedStamp(),
    ...releaseNotesFor(safeStatus),
    ...checkErrorFields(),
    ...(consecutiveAutomaticNetFailures >= STALE_CHECK_NUDGE_THRESHOLD
      ? { staleCheckNudge: true }
      : {}),
    ...(consecutiveAutomaticCheckFailures >= FAILING_CHECK_THRESHOLD
      ? { checksFailing: true }
      : {}),
  };
  if (owner === "independent") {
    independentStatusRevision += 1;
    if (
      activeUpdaterOperation === "automatic-check" &&
      automaticCheckPreviousStatus !== undefined
    ) {
      automaticCheckPreviousStatus = {
        status: stamped,
        independentRevision: independentStatusRevision,
      };
    }
  }
  lastStatus = stamped;
  sendToRenderer("updates:status", stamped);
}

function withActiveRequest(status: UpdateStatus): UpdateStatus {
  return activeUpdaterRequestId === undefined
    ? status
    : { ...status, requestId: activeUpdaterRequestId };
}

function broadcastUpdaterStatus(status: UpdateStatus): void {
  const ownedStatus = withActiveRequest(status);
  broadcast(
    ownedStatus,
    activeUpdaterOperation === "automatic-check"
      ? "automatic-operation"
      : "independent",
  );
}

function broadcastCompletedCheck(
  status: UpdateStatus,
  checkSucceeded = status.state !== "error",
): void {
  if (checkSucceeded) {
    lastCheckedAtMs = Date.now();
    lastCheckError = undefined;
  }
  broadcastUpdaterStatus(status);
}

function readAppUpdateYml(): { owner: string; repo: string } | undefined {
  if (!app.isPackaged) return undefined;
  try {
    const yml = readFileSync(
      path.join(process.resourcesPath, "app-update.yml"),
      "utf8",
    );
    const owner = /^owner:\s*(.+)$/m.exec(yml)?.[1]?.trim();
    const repo = /^repo:\s*(.+)$/m.exec(yml)?.[1]?.trim();
    return owner && repo ? { owner, repo } : undefined;
  } catch {
    return undefined;
  }
}

interface GitHubReleaseSummary {
  tag_name: string;
  draft: boolean;
  prerelease: boolean;
  body?: string | null;
  assets?: Array<{ name?: string }>;
}

let releaseDiscoveryCache: { url: string; etag: string; releases: GitHubReleaseSummary[] } | undefined;

async function fetchLatestCompletedPrereleaseTag(
  owner: string,
  repo: string,
  channel: string,
): Promise<{ tag: string; body?: string } | undefined> {
  try {
    const url = `https://api.github.com/repos/${owner}/${repo}/releases?per_page=100`;
    const cached = releaseDiscoveryCache?.url === url ? releaseDiscoveryCache : undefined;
    const response = await fetch(
      url,
      {
        cache: "no-store",
        headers: {
          Accept: "application/vnd.github+json",
          "X-GitHub-Api-Version": "2022-11-28",
          "User-Agent": `ao-desktop/${app.getVersion()}`,
          ...(cached ? { "If-None-Match": cached.etag } : {}),
        },
        signal: AbortSignal.timeout(10000),
      },
    );
    if (response.status !== 304 && !response.ok) return undefined;
    const releases = response.status === 304 ? cached?.releases : (await response.json()) as GitHubReleaseSummary[];
    if (!Array.isArray(releases)) return undefined;
    if (response.status !== 304) {
      const etag = response.headers.get("etag");
      releaseDiscoveryCache = etag ? { url, etag, releases } : undefined;
    }
    const manifestName = `${channel}${platformSuffix()}.yml`;
    const newest = releases
      .filter((release) => {
        const parsed = semver.valid(release.tag_name);
        return (
          !release.draft &&
          release.prerelease &&
          parsed !== null &&
          semver.prerelease(parsed)?.[0] === channel &&
          release.assets?.some((asset) => asset.name === manifestName) === true
        );
      })
      .sort((left, right) => semver.rcompare(left.tag_name, right.tag_name))[0];
    if (newest === undefined) return undefined;
    return {
      tag: newest.tag_name,
      ...(typeof newest.body === "string" ? { body: newest.body } : {}),
    };
  } catch {
    return undefined;
  }
}

function directPrereleaseChannel(
  settings: Pick<UpdateSettings, "channel" | "feature">,
): string | undefined {
  if (settings.feature) return `pr${settings.feature.pr}`;
  return settings.channel === "nightly" ? "nightly" : undefined;
}

async function configureDirectPrereleaseFeed(
  settings: UpdateSettings,
): Promise<(() => void) | undefined> {
  directFeedDiscoveredAvailable = undefined;
  const channel = directPrereleaseChannel(settings);
  if (!channel) return undefined;
  const coordinates = await readAppUpdateYml();
  if (!coordinates) return undefined;
  const release = await fetchLatestCompletedPrereleaseTag(
    coordinates.owner,
    coordinates.repo,
    channel,
  );
  if (!release) return undefined;
  const { tag } = release;
  const runningVersion = app.getVersion();
  if (
    semver.valid(runningVersion) !== null &&
    semver.prerelease(runningVersion)?.[0] === channel &&
    semver.lt(tag, runningVersion)
  ) {
    return undefined;
  }

  directFeedReleaseNotes = normalizeReleaseNotes(release.body);
  if (
    semver.valid(tag) !== null &&
    (semver.valid(runningVersion) === null || semver.gt(tag, runningVersion))
  ) {
    directFeedDiscoveredAvailable = {
      version: semver.valid(tag)!,
      ...(directFeedReleaseNotes !== undefined ? { notes: directFeedReleaseNotes } : {}),
    };
  }
  autoUpdater.setFeedURL({
    provider: "generic",
    url: `https://github.com/${coordinates.owner}/${coordinates.repo}/releases/download/${tag}`,
    channel,
    useMultipleRangeRequest: false,
  });
  return () => {
    autoUpdater.setFeedURL({
      provider: "github",
      owner: coordinates.owner,
      repo: coordinates.repo,
    });
  };
}

function platformSuffix(): string {
  if (process.platform === "darwin") return "-mac";
  if (process.platform === "linux") return "-linux";
  return "";
}

async function fetchLatestStableVersion(
  owner: string,
  repo: string,
): Promise<string | undefined> {
  const url = `https://github.com/${owner}/${repo}/releases/latest/download/latest${platformSuffix()}.yml`;
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(10000) });
    if (!res.ok) return undefined;
    return (
      /^version:\s*(.+)$/m.exec(await res.text())?.[1]?.trim() || undefined
    );
  } catch {
    return undefined;
  }
}

async function fetchNightlyImportant(
  owner: string,
  repo: string,
  version: string,
): Promise<boolean> {
  const url = `https://github.com/${owner}/${repo}/releases/download/v${version}/nightly${platformSuffix()}.yml`;
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(10000) });
    if (!res.ok) return false;
    return /^important:\s*true\s*$/m.test(await res.text());
  } catch {
    return false;
  }
}

function stagedStamp(): Pick<UpdateStatus, "staged"> {
  if (stagedAtMs === undefined) return {};
  return {
    staged: {
      ...(stagedVersion === undefined ? {} : { version: stagedVersion }),
      stagedAt: stagedAtMs,
      escalated: stagedEscalated,
      ...(isNativeInstallReady() && nativeStagingFilesPresent() ? {} : { ready: false }),
      ...(stagedReleaseNotes === undefined ? {} : { releaseNotes: stagedReleaseNotes }),
    },
  };
}

function releaseNotesFor(status: UpdateStatus): Pick<UpdateStatus, "releaseNotes"> {
  const describesAnOffer =
    status.state === "available" ||
    status.state === "downloading" ||
    status.state === "preparing" ||
    status.state === "downloaded" ||
    (status.state === "error" && status.version !== undefined);
  const offerSupersedesStaged =
    describesAnOffer && hasStagedBuild() && status.version !== undefined && status.version !== stagedVersion;
  let notes = status.releaseNotes;
  if (offerSupersedesStaged) {
    notes ??= offeredReleaseNotes;
  } else if (stagedAtMs !== undefined && stagedReleaseNotes !== undefined) {
    notes = stagedReleaseNotes;
  } else if (describesAnOffer && !hasStagedBuild()) {
    notes ??= offeredReleaseNotes;
  }
  return notes === undefined ? {} : { releaseNotes: notes };
}

const STAGED_UPDATE_FILE_NAME = "staged-update.json";

function stagedUpdateFile(stateDir: string): string {
  return path.join(stateDir, STAGED_UPDATE_FILE_NAME);
}

let stagedPersistenceQueue: Promise<unknown> = Promise.resolve();

function persistStagedBuild(stateDir: string | undefined): void {
  if (stateDir === undefined || stagedVersion === undefined || stagedAtMs === undefined) return;
  const releaseNotes = stagedReleaseNotes;
  const payload = `${JSON.stringify({
    version: stagedVersion,
    stagedAt: stagedAtMs,
    channel: stagedChannel,
    ...(releaseNotes === undefined ? {} : { releaseNotes }),
  })}\n`;
  stagedPersistenceQueue = stagedPersistenceQueue
    .then(() => mkdir(stateDir, { recursive: true, mode: 0o750 }))
    .then(() => writeFile(stagedUpdateFile(stateDir), payload, { mode: 0o600 }))
    .catch(() => undefined);
}

function forgetPersistedStagedBuild(stateDir: string | undefined): void {
  if (stateDir === undefined) return;
  stagedPersistenceQueue = stagedPersistenceQueue
    .then(() => unlink(stagedUpdateFile(stateDir)))
    .catch(() => undefined);
}

function restoreStagedBuild(stateDir: string): void {
  let raw: {
    version?: unknown;
    stagedAt?: unknown;
    channel?: unknown;
    releaseNotes?: unknown;
  };
  try {
    raw = JSON.parse(readFileSync(stagedUpdateFile(stateDir), "utf8")) as typeof raw;
  } catch {
    return;
  }
  const olderStaged = typeof raw.version === "string" &&
    semver.valid(raw.version) !== null &&
    semver.valid(app.getVersion()) !== null &&
    semver.lt(raw.version, app.getVersion());
  if (
    typeof raw.version !== "string" ||
    typeof raw.stagedAt !== "number" ||
    !Number.isFinite(raw.stagedAt) ||
    raw.version === app.getVersion() ||
    olderStaged
  ) {
    if (olderStaged) discardStagedBuild();
    else forgetPersistedStagedBuild(stateDir);
    return;
  }
  stagedVersion = raw.version;
  stagedAtMs = raw.stagedAt;
  stagedChannel = typeof raw.channel === "string" ? raw.channel : undefined;
  stagedReleaseNotes = boundPlainReleaseNotes(
    typeof raw.releaseNotes === "string" ? raw.releaseNotes : undefined,
  );
  stagedEscalated = false;
}

function refreshStagedReleaseNotes(
  version: string | undefined,
  notes: Parameters<typeof normalizeReleaseNotes>[0],
): void {
  if (
    version === undefined ||
    stagedVersion === undefined ||
    (version !== stagedVersion &&
      (semver.valid(version) === null ||
        semver.valid(stagedVersion) === null ||
        !semver.eq(version, stagedVersion)))
  ) return;
  const releaseNotes = normalizeReleaseNotes(notes) ?? directFeedReleaseNotes;
  if (releaseNotes === undefined || releaseNotes === stagedReleaseNotes) return;
  stagedReleaseNotes = releaseNotes;
  persistStagedBuild(escalationStateDir);
}

function effectiveChannel(
  settings: Pick<UpdateSettings, "channel" | "feature">,
): string {
  return settings.feature ? `pr${settings.feature.pr}` : settings.channel;
}

function installedUpdateChannel(): string {
  const prerelease = semver.prerelease(app.getVersion())?.[0];
  return typeof prerelease === "string" ? prerelease : "latest";
}

function isChannelTransition(settings: Pick<UpdateSettings, "channel" | "feature">): boolean {
  return effectiveChannel(settings) !== installedUpdateChannel();
}

function unavailableChannelStatus(version?: string): UpdateStatus {
  if (version && isChannelTransition(lastAppliedUpdateSettings) &&
      semver.valid(version) && semver.valid(app.getVersion()) &&
      semver.lt(version, app.getVersion())) {
    return {
      state: "unsupported",
      message: "This channel's latest release is older than the installed build. Orchestrator will switch when a newer release is available.",
    };
  }
  return { state: "not-available" };
}

function stagedBuildIsStale(
  settings: Pick<UpdateSettings, "channel" | "feature">,
): boolean {
  return (
    stagedAtMs !== undefined &&
    stagedChannel !== undefined &&
    stagedChannel !== effectiveChannel(settings)
  );
}

let awaitingStagedReplacement = false;

function discardStagedBuild(): void {
  timedOutNativePreparation = undefined;
  awaitingStagedReplacement = true;
  nativeReadyVersion = undefined;
  stagedInCurrentProcess = false;
  forgetPersistedStagedBuild(escalationStateDir);
  offeredReleaseNotes = undefined;
  stagedReleaseNotes = undefined;
  directFeedReleaseNotes = undefined;
  stagedVersion = undefined;
  stagedInCurrentProcess = false;
  stagedChannel = undefined;
  stagedAtMs = undefined;
  stagedEscalated = false;
  stagedRequestId = undefined;
  stopEscalationTimer();
}

const MAC_STAGING_FAILURE_MESSAGE =
  "macOS couldn't prepare the update because files were missing from its temporary installation copy. Check for updates and download it again to retry. If it happens again, install the latest app manually from GitHub Releases.";

function handleMacStagingFailure(err: unknown, requestId = activeUpdaterRequestId): boolean {
  if (process.platform !== "darwin") return false;
  const message = updateErrorText(err);
  if (
    !/ditto:/i.test(message) ||
    !/\.ShipIt\/update[^/]*\/.*\.app\//i.test(message) ||
    !/No such file or directory/i.test(message)
  ) return false;
  console.error("macOS update staging failed:", err);
  reportUpdateFailure(err, "install", "stage");
  discardStagedBuild();
  downloadStalled = false;
  applyInstallOnQuitPolicy();
  automaticCheckPreviousStatus = undefined;
  broadcast({
    state: "error",
    message: MAC_STAGING_FAILURE_MESSAGE,
    ...(requestId === undefined ? {} : { requestId }),
  });
  return true;
}

function hasStagedBuild(): boolean {
  return stagedAtMs !== undefined && stagedVersion !== undefined;
}

function supersedesStagedBuild(version: string | undefined): boolean {
  return hasStagedBuild() && version !== undefined && version !== stagedVersion;
}

type UpdateCheckOutcome = Awaited<ReturnType<typeof autoUpdater.checkForUpdates>>;

const UPDATE_CHECK_TIMEOUT_MS = 60_000;
const UPDATE_CHECK_TIMEOUT_MESSAGE = "Update check timed out. The update service did not respond in time. Try again.";

type UpdateMetadataExecutor = {
  request(options: RequestOptions, token?: CancellationToken, data?: Record<string, unknown> | null): Promise<string | null>;
};

async function checkForUpdatesWithDeadline(): Promise<UpdateCheckOutcome> {
  const executor = (autoUpdater as typeof autoUpdater & { httpExecutor: UpdateMetadataExecutor }).httpExecutor;
  const request = executor.request;
  const tokens = new Set<CancellationToken>();
  let timedOut = false;
  executor.request = async (options, parent, data) => {
    const token = new CancellationToken(parent);
    tokens.add(token);
    if (timedOut) token.cancel();
    try {
      return await request.call(executor, options, token, data);
    } finally {
      tokens.delete(token);
      token.dispose();
    }
  };
  const timer = setTimeout(() => {
    timedOut = true;
    if (activeUpdaterOperation !== "automatic-check") {
      broadcast(withActiveRequest({ state: "error", message: UPDATE_CHECK_TIMEOUT_MESSAGE }));
    }
    for (const token of tokens) token.cancel();
  }, UPDATE_CHECK_TIMEOUT_MS);
  timer.unref?.();
  try {
    return await autoUpdater.checkForUpdates();
  } catch (err) {
    throw timedOut ? new Error(UPDATE_CHECK_TIMEOUT_MESSAGE) : err;
  } finally {
    clearTimeout(timer);
    executor.request = request;
  }
}

function broadcastAvailable(version: string | undefined): void {
  broadcastCompletedCheck(
    stagingDiskShortfall
      ? { state: "error", message: stagingDiskShortfall.error.message, version }
      : { state: "available", version },
    true,
  );
}

function settleCheckStatus(result: UpdateCheckOutcome): void {
  refreshStagedReleaseNotes(result?.updateInfo?.version, result?.updateInfo?.releaseNotes);
  if (lastStatus.state !== "checking") return;
  const version = result?.updateInfo?.version;
  if (result?.isUpdateAvailable === true && version !== undefined) {
    broadcastAvailable(version);
    return;
  }
  broadcastCompletedCheck(
    hasStagedBuild() ? stagedDownloadedStatus() : unavailableChannelStatus(version),
    true,
  );
}

function broadcastDiscoveredAvailable(): void {
  const discovered = directFeedDiscoveredAvailable;
  if (discovered === undefined) return;
  refreshStagedReleaseNotes(discovered.version, discovered.notes);
  if (
    lastStatus.state === "available" ||
    lastStatus.state === "downloading" ||
    lastStatus.state === "preparing" ||
    lastStatus.state === "downloaded" ||
    (stagingDiskShortfall && lastStatus.state === "error" && lastStatus.version === discovered.version)
  ) {
    return;
  }
  if (hasStagedBuild() && !supersedesStagedBuild(discovered.version)) return;
  pendingUpdateVersion = discovered.version;
  offeredReleaseNotes = discovered.notes ?? offeredReleaseNotes;
  broadcastAvailable(discovered.version);
}

function stagedDownloadedStatus(): UpdateStatus {
  return {
    state: nativePreparationBlocked || stagingDiskShortfall ? "error" : isNativeInstallReady() || !stagedInCurrentProcess ? "downloaded" : "preparing",
    ...(nativePreparationBlocked || stagingDiskShortfall
      ? { message: (nativePreparationBlocked ?? stagingDiskShortfall!.error).message }
      : {}),
    version: stagedVersion,
    stagedAt: stagedAtMs,
    escalated: stagedEscalated,
    ...(stagedRequestId === undefined ? {} : { requestId: stagedRequestId }),
  };
}

async function runEscalationCheck(): Promise<void> {
  if (stagedAtMs === undefined) {
    stopEscalationTimer();
    return;
  }
  if (escalationStateDir === undefined) return;
  if (lastStatus.state === "downloading" || lastStatus.state === "preparing" || nativePreparationBlocked || stagingDiskShortfall) return;
  const evaluatedVersion = stagedVersion;
  const evaluatedAt = stagedAtMs;
  try {
    const settings = await readUpdateSettings(escalationStateDir);
    let important = false;
    let latestStableVersion: string | undefined;
    const coords = await readAppUpdateYml();
    if (coords && settings.channel === "nightly") {
      [latestStableVersion, important] = await Promise.all([
        fetchLatestStableVersion(coords.owner, coords.repo),
        stagedVersion !== undefined
          ? fetchNightlyImportant(coords.owner, coords.repo, stagedVersion)
          : Promise.resolve(false),
      ]);
    }
    if (["downloading", "preparing"].includes(lastStatus.state) || stagedVersion !== evaluatedVersion || stagedAtMs !== evaluatedAt) return;
    stagedEscalated = evaluateEscalation({
      channel: settings.channel,
      stagedAt: stagedAtMs,
      now: Date.now(),
      important,
      runningVersion: app.getVersion(),
      latestStableVersion,
    });
    broadcast(stagedDownloadedStatus());
  } catch (err) {
    console.debug("escalation check skipped:", err);
  }
}

function stopEscalationTimer(): void {
  if (escalationTimer !== undefined) {
    clearInterval(escalationTimer);
    escalationTimer = undefined;
  }
}

function restoreAutomaticCheckPreviousStatus(): void {
  if (automaticCheckPreviousStatus === undefined) return;
  const { status, independentRevision } = automaticCheckPreviousStatus;
  automaticCheckPreviousStatus = undefined;
  if (independentStatusRevision !== independentRevision) return;
  broadcast(status);
}

async function runSerializedUpdaterOperation(
  operation: UpdaterOperation,
  runOperation: () => Promise<void>,
  requestId?: string,
): Promise<void> {
  recordUpdateDiagnostic("operation-requested", { requestedOperation: operation, requestedRequestId: requestId });
  const run = async () => {
    if (nativePreparationBlocked) {
      recordUpdateDiagnostic("operation-blocked", { requestedOperation: operation }, nativePreparationBlocked);
      throw nativePreparationBlocked;
    }
    if (operation !== "settings-write") reprobeStagingDisk();
    if (nativePreparation) await nativePreparation.promise;
    activeUpdaterOperation = operation;
    activeUpdaterRequestId = requestId;
    activeUpdaterPhase = operation === "manual-download" ? "download" : "check";
    pendingUpdateVersion = operation === "manual-download" ? offeredUpdateVersion : undefined;
    recordUpdateDiagnostic("operation-started");
    if (operation === "automatic-check") {
      automaticCheckNetFailureCounted = false;
      automaticCheckFailureCounted = false;
    }
    const nativeFailure = new Promise<never>((_resolve, reject) => { rejectNativeOperation = reject; });
    try {
      await Promise.race([runOperation(), nativeFailure]);
      if (nativePreparation) await nativePreparation.promise;
      if (nativePreparationBlocked) throw nativePreparationBlocked;
    } catch (err) {
      recordUpdateDiagnostic("operation-failed", {}, err);
      if ((operation === "automatic-check" || operation === "manual-check" ||
        operation === "manual-download" || operation === "return-home") &&
        handleMacStagingFailure(err, requestId)) return;
      throw err;
    } finally {
      recordUpdateDiagnostic("operation-finished");
      rejectNativeOperation = undefined;
      activeUpdaterOperation = undefined;
      activeUpdaterRequestId = undefined;
      if (operation === "automatic-check")
        automaticCheckPreviousStatus = undefined;
    }
  };
  const queued = updaterOperationQueue.then(run, run);
  updaterOperationQueue = queued.catch(() => undefined);
  await queued;
}

let retirementPollTimer: ReturnType<typeof setInterval> | undefined;
let retirementPollInFlight = false;

function startRetirementPollTimer(stateDir: string): void {
  if (retirementPollTimer !== undefined) return;
  retirementPollTimer = setInterval(
    () => void requestRetirementPoll(stateDir),
    30 * 60 * 1000,
  );
  retirementPollTimer.unref?.();
}

async function requestRetirementPoll(stateDir: string): Promise<void> {
  if (retirementPollInFlight) return;
  retirementPollInFlight = true;
  try {
    await runRetirementPoll(stateDir);
  } finally {
    retirementPollInFlight = false;
  }
}

async function runRetirementPoll(stateDir: string): Promise<void> {
  try {
    await runSerializedUpdaterOperation("settings-write", async () => {
      const before = await readUpdateSettings(stateDir);
      if (before.feature === null || before.feature === undefined) return;
      const settings = await reconcileAndPersist(stateDir, before);
      if (settings.feature === null || settings.feature === undefined) {
        applyUpdaterPolicy(settings);
        configureFeed(settings);
      }
    });
  } catch (err) {
    console.debug("retirement poll skipped:", err);
  }
}

function isNetError(err: unknown): boolean {
  return isNetErrorMessage(updateErrorText(err));
}

function recordAutomaticNetFailure(): void {
  if (automaticCheckNetFailureCounted) return;
  automaticCheckNetFailureCounted = true;
  consecutiveAutomaticNetFailures += 1;
}

function recordAutomaticCheckFailure(err: unknown): void {
  lastCheckError = describeUpdateFailure(err, "check");
  if (isNetError(err)) recordAutomaticNetFailure();
  else consecutiveAutomaticNetFailures = 0;
  if (!automaticCheckFailureCounted) {
    automaticCheckFailureCounted = true;
    consecutiveAutomaticCheckFailures += 1;
  }
}

function automaticChecksAreFailing(): boolean {
  return consecutiveAutomaticCheckFailures >= FAILING_CHECK_THRESHOLD;
}

function publishFailingChecks(): void {
  if (!automaticChecksAreFailing() || failingChecksPublished) return;
  failingChecksPublished = true;
  clearUnrecoverableRememberedBuild();
  broadcast(lastStatus);
}

function clearUnrecoverableRememberedBuild(): void {
  if (stagedInCurrentProcess) return;
  if (!hasStagedBuild()) return;
  console.warn(
    "clearing remembered staged build %s: automatic checks have failed %d times without re-establishing native readiness",
    stagedVersion,
    consecutiveAutomaticCheckFailures,
  );
  forgetPersistedStagedBuild(escalationStateDir);
  stagedVersion = undefined;
  stagedAtMs = undefined;
  stagedChannel = undefined;
  stagedReleaseNotes = undefined;
  stagedEscalated = false;
  stagedRequestId = undefined;
  stopEscalationTimer();
}

const UNREPORTED_UPDATE_KINDS = new Set(["timeout", "unreachable", "rate-limited", "not-found", "unavailable"]);

export function reportUpdateFailure(err: unknown, phase: UpdateErrorPhase, reason?: MainErrorReason): void {
  const kind = describeUpdateFailure(err, phase).info?.kind;
  if (kind && UNREPORTED_UPDATE_KINDS.has(kind)) return;
  captureMainError({ category: "update", reason: reason ?? phase, error: err, ...(kind ? { detail: `kind:${kind}` } : {}) });
}

function failedUpdatePhase(): UpdateErrorPhase {
  if (activeUpdaterOperation === "manual-install") return "install";
  if (activeUpdaterOperation === "return-home" && activeUpdaterPhase === "check") return "return";
  return activeUpdaterPhase;
}

function failureFields(failure: UpdateFailure): Pick<UpdateStatus, "message" | "errorInfo"> {
  return failure.info ? { message: failure.message, errorInfo: failure.info } : { message: failure.message };
}

function updateErrorFields(err: unknown, phase: UpdateErrorPhase): Pick<UpdateStatus, "message" | "errorInfo"> {
  return failureFields(describeUpdateFailure(err, phase));
}

function checkErrorFields(): Pick<UpdateStatus, "checkError" | "checkErrorInfo"> {
  if (!lastCheckError) return {};
  return lastCheckError.info
    ? { checkError: lastCheckError.message, checkErrorInfo: lastCheckError.info }
    : { checkError: lastCheckError.message };
}

function userFacingInstallError(err: unknown): unknown {
  const message = userFacingUpdateError(err, "install");
  return err instanceof Error && err.message === message ? err : new Error(message);
}

const STAGED_INSTALL_REJECTION_PATTERN =
  /did not pass validation|failed to get static code for bundle/i;

function isNativeStagingBusyError(err: unknown): boolean {
  const e = err as { code?: unknown; domain?: unknown };
  return e?.domain === "RACCommandErrorDomain" && e?.code === 1;
}

function settleWithoutFailure(): void {
  broadcastCompletedCheck(
    hasStagedBuild() ? stagedDownloadedStatus() : { state: "not-available" },
  );
}

function isStagedInstallRejection(err: unknown): boolean {
  return STAGED_INSTALL_REJECTION_PATTERN.test(updateErrorText(err));
}

let installRejections: { version: string | undefined; count: number } | undefined;

let handledInstallRejection: { version: string | undefined } | undefined;

const MAX_AUTOMATIC_INSTALL_ATTEMPTS = 2;

function automaticRecoveryExhausted(): boolean {
  return (
    installRejections !== undefined &&
    installRejections.count >= MAX_AUTOMATIC_INSTALL_ATTEMPTS
  );
}

function forgetInstallRejections(): void {
  installRejections = undefined;
}

function recordInstallRejection(version: string | undefined): number {
  installRejections =
    installRejections !== undefined && installRejections.version === version
      ? { version, count: installRejections.count + 1 }
      : { version, count: 1 };
  return installRejections.count;
}

async function clearPendingUpdateCache(): Promise<void> {
  const helper = (
    autoUpdater as unknown as {
      downloadedUpdateHelper?: { clear?: () => Promise<void> } | null;
    }
  ).downloadedUpdateHelper;
  try {
    await helper?.clear?.();
  } catch (err) {
    console.error("could not clear the cached update download:", err);
  }
}

export const E2E_UPDATE_SENTINEL_ENV = "AO_E2E_UPDATE_SENTINEL";

function installE2EUpdateSentinel(): void {
  const sentinelPath = process.env[E2E_UPDATE_SENTINEL_ENV];
  if (!sentinelPath) return;
  nativeAutoUpdater.on("update-downloaded", (_event, _notes, releaseName) => {
    try {
      writeFileSync(
        sentinelPath,
        `${JSON.stringify({ stagedAt: Date.now(), releaseName: releaseName ?? null })}\n`,
      );
      console.info(`[e2e] native updater staged ${releaseName ?? "an update"}; wrote ${sentinelPath}`);
    } catch (err) {
      console.error("[e2e] failed to write update sentinel:", err);
    }
  });
}

function wireUpdaterEvents(): void {
  wireUpdaterLogger();
  if (eventsWired) return;
  eventsWired = true;
  if (process.platform === "darwin") {
    powerMonitor.on("suspend", () => {
      if (preparationSuspendedAt === undefined) preparationSuspendedAt = Date.now();
    });
    powerMonitor.on("resume", () => {
      if (preparationSuspendedAt === undefined) return;
      const suspendedMs = Math.max(0, Date.now() - preparationSuspendedAt);
      preparationSuspendedMs += suspendedMs;
      preparationSuspendedAt = undefined;
      nativePreparation?.resume();
      recordUpdateDiagnostic("native-preparation-resumed", { suspendedMs });
    });
    nativeAutoUpdater.on("update-downloaded", (_event, _notes, releaseName) => {
      const timedOut = timedOutNativePreparation;
      const recoverable = timedOut !== undefined &&
        nativePreparation === undefined && nativePreparationBlocked === timedOut.error &&
        stagedVersion === timedOut.version && stagedChannel === autoUpdater.channel &&
        !awaitingStagedReplacement && (!releaseName || releaseName === timedOut.version);
      recordUpdateDiagnostic("native-update-downloaded", {
        releaseName, accepted: recoverable || (!!nativePreparation && (!releaseName || releaseName === nativePreparation.version)),
        hasPendingPreparation: nativePreparation !== undefined,
      });
      if (recoverable) {
        nativePreparationBlocked = undefined;
        timedOutNativePreparation = undefined;
        nativePreparationError = undefined;
        nativeReadyVersion = timedOut.version;
        stagedInCurrentProcess = true;
        applyInstallOnQuitPolicy();
        recordUpdateDiagnostic("native-preparation-recovered", { version: timedOut.version });
        broadcast(stagedDownloadedStatus());
        return;
      }
      if (!nativePreparation || (releaseName && releaseName !== nativePreparation.version)) return;
      nativePreparation.finish();
      broadcast(lastStatus.state === "downloading" ? lastStatus : stagedDownloadedStatus());
    });
    nativeAutoUpdater.on("error", (error) => {
      timedOutNativePreparation = undefined;
      recordUpdateDiagnostic("native-error", { userMessage: shortStagingMessage(error) }, error);
      console.error("native macOS updater error during staging:", error);
      reportUpdateFailure(error, "install", "stage");
      const short = new Error(shortStagingMessage(error));
      nativePreparation?.finish(short);
      nativeReadyVersion = undefined;
      nativePreparationError = short;
      if (activeUpdaterOperation === "manual-install") rejectNativeOperation?.(short);
      if (updateRestartRequested) {
        updateRestartRequested = false;
        macRestartPreparation = undefined;
        stagedInCurrentProcess = false;
        const failure = describeUpdateFailure(short, "install");
        void macRestartProgress?.fail(failure.message).catch(() => undefined);
        broadcast({ state: "error", ...failureFields(failure) });
        restartFailureHandler?.();
      }
    });
  }
  installE2EUpdateSentinel();
  autoUpdater.on("checking-for-update", () => {
    if (
      activeUpdaterOperation === "automatic-check" &&
      automaticCheckPreviousStatus === undefined
    ) {
      const status = lastStatus;
      broadcastUpdaterStatus({ state: "checking" });
      automaticCheckPreviousStatus = {
        status,
        independentRevision: independentStatusRevision,
      };
      return;
    }
    broadcastUpdaterStatus({ state: "checking" });
  });
  autoUpdater.on("update-available", (info) => {
    offeredMacFiles = Array.isArray(info?.files) ? info.files : [];
    offeredUpdateVersion = info?.version;
    selectMacTargetBytes(process.arch === "arm64");
    transferObservation = {
      eligible: differentialEligible,
      attemptedDifferential: false,
      fallback: false,
      transferred: undefined,
    };
    consecutiveAutomaticNetFailures = 0;
    consecutiveAutomaticCheckFailures = 0;
    failingChecksPublished = false;
    refreshStagedReleaseNotes(info?.version, info?.releaseNotes);
    if (stagedAtMs !== undefined && info?.version === stagedVersion) {
      broadcastCompletedCheck(stagedDownloadedStatus(), true);
      return;
    }
    if (
      installRejections !== undefined &&
      info?.version !== installRejections.version
    ) {
      forgetInstallRejections();
    }
    pendingUpdateVersion = info?.version;
    emitUpdateOutcome({ event: "ao.renderer.update_check_succeeded", phase: "check", trigger: activeUpdateTrigger() });
    offeredReleaseNotes = normalizeReleaseNotes(info?.releaseNotes) ?? directFeedReleaseNotes;
    broadcastAvailable(info?.version);
    if (autoUpdater.autoDownload) {
      activeUpdaterPhase = "download";
      broadcastUpdaterStatus({ state: "downloading", version: info?.version });
      armDownloadStallWatchdog();
    }
  });
  autoUpdater.on("update-cancelled", () => {
    clearDownloadStallWatchdog();
  });
  autoUpdater.on("update-not-available", (info) => {
    emitUpdateOutcome({ event: "ao.renderer.update_check_succeeded", phase: "check", trigger: activeUpdateTrigger() });
    consecutiveAutomaticNetFailures = 0;
    consecutiveAutomaticCheckFailures = 0;
    failingChecksPublished = false;
    broadcastCompletedCheck(unavailableChannelStatus(info?.version));
    if (stagedAtMs !== undefined)
      broadcastUpdaterStatus(stagedDownloadedStatus());
  });
  autoUpdater.on("download-progress", (p) => {
    consecutiveAutomaticNetFailures = 0;
    consecutiveAutomaticCheckFailures = 0;
    failingChecksPublished = false;
    activeUpdaterPhase = "download";
    const transferred = Number.isFinite(p?.transferred) && p.transferred >= 0 ? p.transferred : undefined;
    const total = Number.isFinite(p?.total) && p.total >= 0 ? p.total : undefined;
    const bytesPerSecond = Number.isFinite(p?.bytesPerSecond) && p.bytesPerSecond >= 0 ? p.bytesPerSecond : undefined;
    transferObservation.transferred = transferred;
    if (p?.transferred === undefined || p.transferred !== lastStatus.transferred) armDownloadStallWatchdog();
    return broadcastUpdaterStatus({
      state: p?.percent >= 100 ? "preparing" : "downloading",
      version: pendingUpdateVersion,
      percent: Math.max(0, Math.min(100, Math.floor(p?.percent ?? 0))),
      ...(transferred === undefined ? {} : { transferred }),
      ...(total === undefined ? {} : { total }),
      ...(bytesPerSecond === undefined ? {} : { bytesPerSecond }),
    });
  });
  autoUpdater.on("update-downloaded", (info) => {
    recordUpdateDiagnostic("archive-downloaded", { targetVersion: info?.version });
    clearDownloadStallWatchdog();
    downloadStalled = false;
    emitUpdateOutcome({
      event: "ao.updates.downloaded",
      phase: "download",
      trigger: activeUpdateTrigger(),
      ...(info?.version ? { to_version: info.version } : {}),
    });
    const restaged = stagedAtMs !== undefined && info?.version === stagedVersion;
    const previousStagedReleaseNotes = stagedReleaseNotes;
    stagedVersion = info?.version;
    stagedInCurrentProcess = true;
    if (process.platform === "darwin" && stagedVersion) {
      const archiveBytes = info?.files?.reduce(
        (max, file) => Math.max(max, file?.size ?? 0),
        0,
      );
      beginNativePreparation(stagedVersion, archiveBytes || undefined);
    }
    stagedChannel = autoUpdater.channel ?? undefined;
    stagedReleaseNotes =
      normalizeReleaseNotes(info?.releaseNotes) ??
      (info?.version === offeredUpdateVersion ? offeredReleaseNotes : undefined) ??
      (info?.version === directFeedDiscoveredAvailable?.version ? directFeedReleaseNotes : undefined) ??
      (restaged ? previousStagedReleaseNotes : undefined);
    if (!restaged) {
      stagedAtMs = Date.now();
      stagedEscalated = false;
    }
    stagedRequestId = activeUpdaterRequestId;
    handledInstallRejection = undefined;
    awaitingStagedReplacement = false;
    applyInstallOnQuitPolicy();
    persistStagedBuild(escalationStateDir);
    automaticCheckPreviousStatus = undefined;
    broadcast(withActiveRequest(stagedDownloadedStatus()));
    void runEscalationCheck();
    if (!restaged || escalationTimer === undefined) {
      stopEscalationTimer();
      escalationTimer = setInterval(
        () => void runEscalationCheck(),
        30 * 60 * 1000,
      );
      escalationTimer.unref?.();
    }
  });
  autoUpdater.on("error", (err) => {
    if (process.platform !== "darwin" && updateRestartRequested) {
      clearUpdateRestartRequest();
      restartFailureHandler?.();
    }
    recordUpdateDiagnostic("electron-updater-error", {}, err);
    clearDownloadStallWatchdog();
    if (handleMacStagingFailure(err)) {
      emitUpdateFailure(err);
      return;
    }
    if (downloadStalled) {
      downloadStalled = false;
      console.info("update download cancelled after stalling:", err);
      return;
    }
    if (isNativeStagingBusyError(err)) {
      console.info("native staging already in progress; request refused:", err);
      return;
    }
    emitUpdateFailure(err);
    if (isStagedInstallRejection(err)) {
      if (!hasStagedBuild() && handledInstallRejection !== undefined) {
        console.debug("ignoring a duplicate delivery of an install rejection:", err);
        return;
      }
    }
    if (hasStagedBuild() && isStagedInstallRejection(err)) {
      const failures = recordInstallRejection(stagedVersion);
      const exhausted = failures >= MAX_AUTOMATIC_INSTALL_ATTEMPTS;
      handledInstallRejection = { version: stagedVersion };
      discardStagedBuild();
      if (exhausted) {
        void runSerializedUpdaterOperation(
          "cache-clear",
          clearPendingUpdateCache,
        ).catch(() => undefined);
      }
      console.error(
        `staged update rejected at install time (attempt ${failures}${exhausted ? ", discarding cached download and stopping automatic retries" : ""}):`,
        err,
      );
      reportUpdateFailure(err, "install");
      broadcast(
        withActiveRequest({
          state: "error",
          message:
            exhausted
              ? "Couldn't install the update — the copy failed verification twice. " +
                "Orchestrator has discarded the download and stopped retrying on its own. Check " +
                "for updates again to start a fresh one, or download the latest build " +
                "manually and install it over this one."
              : "Couldn't install the update — the downloaded copy failed verification. " +
                "Orchestrator will prepare it again on the next check.",
        }),
      );
      return;
    }
    if (activeUpdaterOperation === "automatic-check" && activeUpdaterPhase === "check") {
      console.error("auto-update check failed:", err);
      reportUpdateFailure(err, "check");
      recordAutomaticCheckFailure(err);
      restoreAutomaticCheckPreviousStatus();
      publishFailingChecks();
      return;
    }
    if (isManifest404Error(err)) {
      console.info("update check failed (404, manifest not found):", err);
      if (activeUpdaterOperation === "manual-download") {
        broadcast(withActiveRequest({ state: "error", ...updateErrorFields(err, "download") }));
      } else if (stagedAtMs !== undefined) {
        lastCheckError = describeUpdateFailure(err, "check");
        broadcastUpdaterStatus(stagedDownloadedStatus());
      } else {
        broadcastCompletedCheck({ state: "error", ...updateErrorFields(err, "check") });
      }
      return;
    }
    const failedAs = failedUpdatePhase();
    if (activeUpdaterOperation === undefined || activeUpdaterOperation === "automatic-check") {
      console.error(`update ${failedAs} failed:`, err);
      reportUpdateFailure(err, failedAs);
    }
    const status: UpdateStatus = {
      state: "error",
      ...updateErrorFields(err, failedAs),
      ...(isNetError(err) ? { netError: true } : {}),
    };
    if (activeUpdaterPhase === "check") broadcastCompletedCheck(status);
    else broadcast(withActiveRequest(status));
  });
}

export function getUpdateStatus(): UpdateStatus {
  const { releaseNotes: _ignored, ...statusWithoutNotes } = lastStatus;
  return {
    ...statusWithoutNotes,
    ...stagedStamp(),
    ...releaseNotesFor(lastStatus),
    ...checkErrorFields(),
    ...(consecutiveAutomaticNetFailures >= STALE_CHECK_NUDGE_THRESHOLD
      ? { staleCheckNudge: true }
      : {}),
    ...(consecutiveAutomaticCheckFailures >= FAILING_CHECK_THRESHOLD
      ? { checksFailing: true }
      : {}),
  };
}

function automaticUpdateCheckInterval(_settings: UpdateSettings): number {
  return AUTOMATIC_UPDATE_CHECK_INTERVAL_MS;
}

async function awaitStartedDownload(result: UpdateCheckOutcome): Promise<void> {
  if (!result?.downloadPromise) return;
  activeUpdaterPhase = "download";
  pendingUpdateVersion = result.updateInfo?.version;
  activeDownloadCancellation = result.cancellationToken;
  if (lastStatus.state === "available" || lastStatus.state === "checking") {
    broadcastUpdaterStatus({ state: "downloading", version: pendingUpdateVersion });
  }
  if (lastStatus.state === "downloading" || lastStatus.state === "preparing") armDownloadStallWatchdog();
  try {
    await result.downloadPromise;
  } finally {
    clearDownloadStallWatchdog();
  }
}

async function runAutomaticUpdateCheck(
  stateDir: string,
): Promise<number> {
  let nextIntervalMs = AUTOMATIC_UPDATE_CHECK_INTERVAL_MS;
  let failed = false;
  try {
    await runSerializedUpdaterOperation("automatic-check", async () => {
      const settings = await reconcileAndPersist(
        stateDir,
        await readUpdateSettings(stateDir),
      );
      nextIntervalMs = automaticUpdateCheckInterval(settings);

      escalationStateDir = stateDir;
      wireUpdaterEvents();
      applyUpdaterPolicy(settings);
      configureFeed(settings);
      const staleStaged = stagedBuildIsStale(settings);
      if (staleStaged) discardStagedBuild();
      else discardLostNativeStaging();
      autoUpdater.autoDownload = !stagingDiskShortfall && (
        staleStaged || (hasStagedBuild() && !isNativeInstallReady()) ||
        (settings.enabled && !hasStagedBuild() && !automaticRecoveryExhausted()));
      applyInstallOnQuitPolicy();
      const restoreFeed = directPrereleaseChannel(settings)
        ? await configureDirectPrereleaseFeed(settings)
        : undefined;
      try {
        const result = await checkForUpdatesWithDeadline();
        settleCheckStatus(result);
        if (result?.downloadPromise) {
          await awaitStartedDownload(result);
        } else if (settings.enabled && !stagingDiskShortfall) {
          if (
            result?.isUpdateAvailable === true &&
            supersedesStagedBuild(result.updateInfo?.version)
          ) {
            activeUpdaterPhase = "download";
            pendingUpdateVersion = result?.updateInfo?.version;
            const token = new CancellationToken();
            activeDownloadCancellation = token;
            broadcastUpdaterStatus({ state: "downloading", version: pendingUpdateVersion });
            armDownloadStallWatchdog();
            await autoUpdater.downloadUpdate(token);
          }
        }
      } catch (err) {
        if (handleMacStagingFailure(err)) return;
        if (activeUpdaterPhase === "download") {
          if (!downloadStalled && lastStatus.state !== "error") broadcast(withActiveRequest({ state: "error", ...updateErrorFields(err, "download"), version: pendingUpdateVersion }));
        } else {
          recordAutomaticCheckFailure(err);
          restoreAutomaticCheckPreviousStatus();
          publishFailingChecks();
        }
        throw err;
      } finally {
        restoreFeed?.();
        broadcastDiscoveredAvailable();
      }
    });
  } catch (err) {
    failed = true;
    console.error("auto-update check failed:", err);
    reportUpdateFailure(err, "check");
  }
  if (failed || automaticCheckFailureCounted) {
    automaticRetryFailures = Math.min(automaticRetryFailures + 1, 5);
  } else {
    automaticRetryFailures = 0;
  }
  return Math.min(nextIntervalMs * 2 ** automaticRetryFailures, AUTOMATIC_UPDATE_RETRY_CAP_MS);
}

function schedulePeriodicAutomaticUpdateCheck(
  stateDir: string,
  intervalMs: number,
): void {
  if (
    automaticUpdateTimer !== undefined &&
    automaticUpdateTimerIntervalMs === intervalMs
  ) {
    return;
  }
  stopPeriodicAutomaticUpdateCheck();
  automaticUpdateTimerIntervalMs = intervalMs;
  automaticUpdateTimer = setInterval(() => {
    void requestAutomaticUpdateCheck(stateDir).then((nextIntervalMs) => {
      if (nextIntervalMs !== undefined)
        schedulePeriodicAutomaticUpdateCheck(stateDir, nextIntervalMs);
    });
  }, intervalMs);
  automaticUpdateTimer.unref?.();
}

function stopPeriodicAutomaticUpdateCheck(): void {
  if (automaticUpdateTimer === undefined) return;
  clearInterval(automaticUpdateTimer);
  automaticUpdateTimer = undefined;
  automaticUpdateTimerIntervalMs = undefined;
}

function reconcileAutomaticUpdateSchedule(
  stateDir: string,
  settings: UpdateSettings,
): void {
  schedulePeriodicAutomaticUpdateCheck(
    stateDir,
    automaticUpdateCheckInterval(settings),
  );
}

async function requestAutomaticUpdateCheck(
  stateDir: string,
): Promise<number | undefined> {
  discardLostNativeStaging();
  if (automaticCheckInFlight) return undefined;
  automaticCheckInFlight = true;
  try {
    return await runAutomaticUpdateCheck(stateDir);
  } finally {
    automaticCheckInFlight = false;
  }
}

export async function startAutoUpdates(stateDir: string): Promise<void> {
  escalationStateDir = stateDir;
  restoreStagedBuild(stateDir);
  recordUpdateDiagnostic("automatic-updates-started", { stagedVersion, stagedAtMs });
  startRetirementPollTimer(stateDir);
  const intervalMs = await requestAutomaticUpdateCheck(stateDir);
  if (intervalMs !== undefined)
    schedulePeriodicAutomaticUpdateCheck(stateDir, intervalMs);
}

async function persistRendererUpdateSettings(
  stateDir: string,
  settings: UpdateSettings,
): Promise<UpdateSettings> {
  return updateUpdateSettings(stateDir, current => ({
    ...settings,
    macDifferentialUpdates: current.macDifferentialUpdates === true,
  }));
}

async function persistUpdaterSettings(
  stateDir: string,
  settings: UpdateSettings,
): Promise<void> {
  const next = await persistRendererUpdateSettings(stateDir, settings);
  applyUpdaterPolicy(next);
  configureFeed(next);
  reconcileAutomaticUpdateSchedule(stateDir, next);
}

export async function setUpdateSettings(
  stateDir: string,
  settings: UpdateSettings,
): Promise<void> {
  await runSerializedUpdaterOperation("settings-write", () =>
    persistUpdaterSettings(stateDir, settings),
  );
}

export interface UpdateCheckOptions {
  settings?: UpdateSettings;
  requestId?: string;
}

export async function checkForUpdatesNow(
  stateDir: string,
  options: UpdateCheckOptions = {},
): Promise<void> {
  escalationStateDir = stateDir;
  wireUpdaterEvents();
  forgetInstallRejections();
	if (!app.isPackaged) {
    emitUpdateOutcome({
      event: "ao.renderer.update_unsupported",
      phase: activeUpdaterPhase,
      trigger: activeUpdateTrigger(),
      error_category: "not_supported",
    });
    broadcast({
      state: "unsupported",
      message: "Updates are only available in the installed app.",
      requestId: options.requestId,
    });
    return;
  }
  const failed: { phase: UpdatePhase } = { phase: "check" };
  try {
    await runSerializedUpdaterOperation(
      "manual-check",
      async () => {
        const requested = options.settings
          ? await persistRendererUpdateSettings(stateDir, options.settings)
          : await readUpdateSettings(stateDir);
        const settings = await reconcileAndPersist(stateDir, requested);
        applyUpdaterPolicy(settings);
        reconcileAutomaticUpdateSchedule(stateDir, settings);
        configureFeed(settings);
        const staleStaged = stagedBuildIsStale(settings);
        if (staleStaged) discardStagedBuild();
        autoUpdater.autoDownload = !stagingDiskShortfall && (staleStaged || (hasStagedBuild() && !isNativeInstallReady()));
        applyInstallOnQuitPolicy();
        broadcastUpdaterStatus({ state: "checking" });
        const restoreFeed = await configureDirectPrereleaseFeed(settings);
        try {
          const result = await checkForUpdatesWithDeadline();
          settleCheckStatus(result);
          if (result?.downloadPromise) failed.phase = "download";
          await awaitStartedDownload(result);
          if (!result?.downloadPromise && settings.enabled && !stagingDiskShortfall && result?.isUpdateAvailable &&
              (!hasStagedBuild() || supersedesStagedBuild(result.updateInfo?.version))) {
            failed.phase = "download";
            activeUpdaterPhase = "download";
            pendingUpdateVersion = result.updateInfo?.version;
            const token = new CancellationToken();
            activeDownloadCancellation = token;
            broadcastUpdaterStatus({ state: "downloading", version: pendingUpdateVersion });
            armDownloadStallWatchdog();
            await autoUpdater.downloadUpdate(token);
          }
        } finally {
          restoreFeed?.();
        }
      },
      options.requestId,
    );
  } catch (err) {
    if (isNativeStagingBusyError(err)) {
      console.info("manual check refused: native staging already in progress:", err);
      settleWithoutFailure();
      return;
    }
    if (isManifest404Error(err)) {
      console.info(`manual update ${failed.phase} failed:`, err);
      const failure = describeUpdateFailure(err, failed.phase);
      broadcastCompletedCheck({
        state: "error",
        ...failureFields(failure),
        ...(options.requestId === undefined ? {} : { requestId: options.requestId }),
      });
      if (stagedAtMs !== undefined) {
        lastCheckError = failure;
        broadcast(stagedDownloadedStatus());
      }
    } else {
      console.error(`manual update ${failed.phase} failed:`, err);
      reportUpdateFailure(err, failed.phase);
      broadcastCompletedCheck({
        state: "error",
        ...updateErrorFields(err, failed.phase),
        ...(isNetError(err) ? { netError: true } : {}),
        ...(options.requestId === undefined ? {} : { requestId: options.requestId }),
      });
    }
  }
}

export async function returnToHome(
  stateDir: string,
  requestId?: string,
): Promise<void> {
  escalationStateDir = stateDir;
  wireUpdaterEvents();
  if (!app.isPackaged) {
    emitUpdateOutcome({
      event: "ao.renderer.update_unsupported",
      phase: activeUpdaterPhase,
      trigger: activeUpdateTrigger(),
      error_category: "not_supported",
    });
    broadcast({
      state: "unsupported",
      message: "Updates are only available in the installed app.",
      requestId,
    });
    return;
  }
  const failed: { phase: UpdatePhase } = { phase: "check" };
  try {
    await runSerializedUpdaterOperation(
      "return-home",
      async () => {
        const cleared = await updateUpdateSettings(stateDir, (current) =>
          current.feature ? { ...current, feature: null } : current,
        );
        const settings = await reconcileAndPersist(stateDir, cleared);
        applyUpdaterPolicy(settings);
        reconcileAutomaticUpdateSchedule(stateDir, settings);
        configureFeed(settings);
        const staleStaged = stagedBuildIsStale(settings);
        if (staleStaged) discardStagedBuild();
        autoUpdater.autoDownload = !stagingDiskShortfall && (staleStaged || (hasStagedBuild() && !isNativeInstallReady()));
        applyInstallOnQuitPolicy();
        broadcastUpdaterStatus({ state: "checking" });
        const result = await checkForUpdatesWithDeadline();
        settleCheckStatus(result);
        if (result?.downloadPromise) failed.phase = "download";
        await awaitStartedDownload(result);
      },
      requestId,
    );
  } catch (err) {
    if (isNativeStagingBusyError(err)) {
      console.info("return home refused: native staging already in progress:", err);
      settleWithoutFailure();
      return;
    }
    console.error(`return home ${failed.phase} failed:`, err);
    reportUpdateFailure(err, failed.phase);
    broadcast({
      state: "error",
      ...updateErrorFields(err, failed.phase === "check" ? "return" : "download"),
      ...(requestId === undefined ? {} : { requestId }),
    });
  }
}

export async function downloadUpdateNow(requestId?: string): Promise<void> {
  if (manualDownloadPending || lastStatus.state === "downloading" || lastStatus.state === "preparing") return;
  const version = lastStatus.version;
  wireUpdaterEvents();
  forgetInstallRejections();
	if (!app.isPackaged) {
    emitUpdateOutcome({
      event: "ao.renderer.update_unsupported",
      phase: activeUpdaterPhase,
      trigger: activeUpdateTrigger(),
      error_category: "not_supported",
    });
    broadcast({
      state: "unsupported",
      message: "Updates are only available in the installed app.",
      requestId,
    });
    return;
  }
  manualDownloadPending = true;
  broadcast({ state: "downloading", version, requestId });
  try {
    await runSerializedUpdaterOperation(
      "manual-download",
      async () => {
        const token = new CancellationToken();
        if (lastStatus.state === "downloaded" || lastStatus.state === "preparing") return;
        if (stagingDiskShortfall) throw stagingDiskShortfall.error;
        pendingUpdateVersion = lastStatus.version ?? version;
        broadcastUpdaterStatus({ state: "downloading", version: pendingUpdateVersion });
        activeDownloadCancellation = token;
        applyUpdaterPolicy(lastAppliedUpdateSettings);
        transferObservation = { eligible: differentialEligible, attemptedDifferential: false, fallback: false, transferred: undefined };
        armDownloadStallWatchdog();
        await autoUpdater.downloadUpdate(token);
      },
      requestId,
    );
  } catch (err) {
    if (lastStatus.state === "error" && lastStatus.message === "Download stopped responding. Try again.") return;
    if (isNativeStagingBusyError(err)) {
      console.info("download refused: native staging already in progress:", err);
      settleWithoutFailure();
      return;
    }
    console.error("update download failed:", err);
    reportUpdateFailure(err, "download");
    broadcast({
      state: "error",
      ...updateErrorFields(err, "download"),
      requestId,
    });
  } finally {
    manualDownloadPending = false;
    clearDownloadStallWatchdog();
  }
}

export async function setMacDifferentialUpdates(
  stateDir: string,
  enabled: boolean,
): Promise<void> {
  if (typeof enabled !== "boolean") return;
  developerModeRequested = enabled;
  if (!enabled) {
    developerModeHydrated = false;
    applyUpdaterPolicy(FAIL_CLOSED_UPDATE_SETTINGS);
  }
  await runSerializedUpdaterOperation("settings-write", async () => {
    const settings = await updateUpdateSettings(stateDir, (current) => ({
      ...current,
      macDifferentialUpdates: enabled,
    }));
    developerModeHydrated = enabled && developerModeRequested;
    applyUpdaterPolicy(settings);
  });
}

export function getMacInstallBlocker(): string | undefined {
  if (process.platform !== "darwin") return undefined;
  const bundle = path.resolve(process.execPath, "..", "..", "..");
  if (!bundle.endsWith(".app")) return undefined;
  if (bundle.includes("/AppTranslocation/")) {
    return (
      "macOS is running Orchestrator from a temporary read-only location " +
      "because it was opened straight from where it was downloaded. Quit the app, " +
      "move Orchestrator into /Applications, reopen it from there, and " +
      "then restart to update."
    );
  }
  if (!existsSync(bundle)) return undefined;
  try {
    accessSync(bundle, fsConstants.W_OK);
    accessSync(path.dirname(bundle), fsConstants.W_OK);
  } catch {
    return (
      "The update can't be installed because Orchestrator's location isn't " +
      `writable: ${path.dirname(bundle)}. Fix that folder's permissions, or move ` +
      "Orchestrator somewhere you can write to, reopen it, and then " +
      "restart to update."
    );
  }
  return undefined;
}

function applyInstallOnQuitPolicy(): void {
  const blocker = getMacInstallBlocker();
  autoUpdater.autoInstallOnAppQuit = blocker === undefined && !awaitingStagedReplacement && !nativePreparationBlocked && !stagingDiskShortfall;
  if (awaitingStagedReplacement) {
    console.info(
      "install-on-quit disabled until the replacement build is staged; the cached one belongs to a channel the user left",
    );
  }
  if (blocker !== undefined) {
    console.warn(
      "install-on-quit disabled; the update cannot be installed from here:",
      blocker,
    );
  }
}

export function setUpdateRestartFailureHandler(handler: () => void): void { restartFailureHandler = handler; }

export function isUpdateRestartRequested(): boolean { return updateRestartRequested; }

export function clearUpdateRestartRequest(): void {
  if (!updateRestartRequested) return;
  updateRestartRequested = false;
  macRestartPreparation = undefined;
}

export async function quitAndInstallUpdate(confirmedVersion?: string): Promise<UpdateInstallResult> {
  recordUpdateDiagnostic("install-requested", { confirmedVersion });
  if (confirmedVersion !== undefined && (typeof confirmedVersion !== "string" || !confirmedVersion.trim())) {
    throw new Error("A valid confirmed update version is required.");
  }
  if (!app.isPackaged) return;
  if (awaitingStagedReplacement) {
    throw new Error("Check for updates and download an update before restarting to install.");
  }
  const blocker = getMacInstallBlocker();
  if (blocker !== undefined) {
    throw new Error(blocker);
  }
  if (process.platform !== "darwin") {
    if (!hasStagedBuild() || lastStatus.state === "downloading" || lastStatus.state === "preparing") {
      throw new Error("The update is not ready to install. Check for updates again.");
    }
    if (!stagedInCurrentProcess) {
      try {
        await runSerializedUpdaterOperation("manual-install", async () => {
          await prepareRememberedNonDarwinUpdate();
        });
      } catch (err) {
        console.error("failed to prepare update for restart:", err);
        reportUpdateFailure(err, "install", "restart_prep");
        throw userFacingInstallError(err);
      }
    }
    if (confirmedVersion !== undefined && stagedVersion && confirmedVersion !== stagedVersion) {
      return { state: "confirmation-required", version: stagedVersion,
        releaseNotes: lastStatus.state === "downloaded" && lastStatus.version === stagedVersion ? lastStatus.releaseNotes : undefined };
    }
    if (escalationStateDir && stagedVersion) {
      await Promise.race([
        markUpdateRelaunch({ stateDir: escalationStateDir, version: stagedVersion }).catch((err) => {
          console.warn("failed to write post-update relaunch marker:", err);
        }),
        new Promise<void>((resolve) => setTimeout(resolve, 750)),
      ]);
    }
    recordUpdateDiagnostic("quit-and-install", { version: stagedVersion });
    updateRestartRequested = true;
    try {
      autoUpdater.quitAndInstall(false, true);
    } catch (error) {
      clearUpdateRestartRequest();
      throw error;
    }
    return;
  }
  if (discardLostNativeStaging()) {
    if (escalationStateDir) void runAutomaticUpdateCheck(escalationStateDir);
    throw new Error("The staged update files are missing. Orchestrator is checking for a fresh update. Confirm installation again when it is ready.");
  }
  if (macRestartPreparation) return macRestartPreparation;
  let confirmation: UpdateInstallResult;
  let installAbandoned = false;
  const preparation = runSerializedUpdaterOperation("manual-install", async () => {
    const previousPreparationError = nativePreparationError;
    const assertInstallActive = () => {
      if (installAbandoned) throw new Error("Update preparation cancelled. Confirm installation again.");
      if (nativePreparationError && nativePreparationError !== previousPreparationError) throw nativePreparationError;
    };
    let progress: Awaited<ReturnType<typeof startMacUpdateProgress>> | undefined;
    try {
      if (!escalationStateDir || !hasStagedBuild()) {
        throw new Error("Check for updates and download an update before restarting to install.");
      }
      if (!stagedInCurrentProcess) {
        confirmation = await prepareRememberedMacUpdate(confirmedVersion);
        assertInstallActive();
        if (confirmation) return;
      }
      if (confirmedVersion !== undefined && stagedVersion && confirmedVersion !== stagedVersion) {
        confirmation = { state: "confirmation-required", version: stagedVersion,
          releaseNotes: lastStatus.state === "downloaded" && lastStatus.version === stagedVersion ? lastStatus.releaseNotes : undefined };
        return;
      }
      const version = stagedVersion;
      if (!version) throw new Error("The update is no longer ready to install. Check for updates again.");
      await waitForNativePreparation(version);
      assertInstallActive();
      progress = await startMacUpdateProgress({
        stateDir: escalationStateDir,
        resourcesPath: process.resourcesPath,
        appPath: path.resolve(process.execPath, "..", "..", ".."),
        version,
      });
      assertInstallActive();
      progress.assertAlive();
      macRestartProgress = progress;
      updateRestartRequested = true;
      await Promise.race([
        markUpdateRelaunch({ stateDir: escalationStateDir, version }).catch((err) => {
          console.warn("failed to write post-update relaunch marker:", err);
        }),
        new Promise<void>((resolve) => setTimeout(resolve, 750)),
      ]);
      assertInstallActive();
      recordUpdateDiagnostic("quit-and-install", { version });
      autoUpdater.quitAndInstall(false, true);
      if (!updateRestartRequested) throw nativePreparationError ?? new Error("The installer could not restart Orchestrator.");
    } catch (err) {
      if (installAbandoned) {
        await progress?.fail(userFacingUpdateError(err, "install")).catch(() => undefined);
        return;
      }
      recordUpdateDiagnostic("install-preparation-failed", {}, err);
      updateRestartRequested = false;
      stagedInCurrentProcess = false;
      nativeReadyVersion = undefined;
      console.error("failed to prepare update for restart:", err);
      reportUpdateFailure(err, "install", "restart_prep");
      const failure = describeUpdateFailure(err, "install");
      await progress?.fail(failure.message).catch(() => undefined);
      broadcast({ state: "error", ...failureFields(failure) });
      throw userFacingInstallError(err);
    }
  }).then(() => confirmation, err => {
    installAbandoned = true;
    const failure = describeUpdateFailure(err, "install");
    if (lastStatus.state !== "error" || lastStatus.message !== failure.message) {
      broadcast({ state: "error", ...failureFields(failure) });
    }
    throw userFacingInstallError(err);
  }).finally(() => { if (!updateRestartRequested && macRestartPreparation === preparation) macRestartPreparation = undefined; });
  macRestartPreparation = preparation;
  return macRestartPreparation;
}

async function waitForNativePreparation(version: string): Promise<void> {
  if (nativePreparationBlocked) throw nativePreparationBlocked;
  if (nativePreparation?.version === version) await nativePreparation.promise;
  if (nativePreparationError) throw nativePreparationError;
  if (nativeReadyVersion !== version || stagedVersion !== version) {
    throw new Error("The update is not ready in macOS. Check for updates again.");
  }
}

async function prepareRememberedMacUpdate(confirmedVersion?: string): Promise<UpdateInstallResult> {
  if (!escalationStateDir) throw new Error("Check for updates before restarting to install.");
  const settings = await reconcileAndPersist(escalationStateDir, await readUpdateSettings(escalationStateDir));
  configureFeed(settings);
  autoUpdater.autoDownload = false;
  applyInstallOnQuitPolicy();
  broadcastUpdaterStatus({ state: "checking" });
  const restoreFeed = await configureDirectPrereleaseFeed(settings);
  try {
    const result = await checkForUpdatesWithDeadline();
    if (result?.isUpdateAvailable !== true) {
      throw new Error("The remembered update is no longer available on the selected channel. Check for updates and try again.");
    }
    if (confirmedVersion !== undefined && result.updateInfo.version !== confirmedVersion) {
      return {
        state: "confirmation-required",
        version: result.updateInfo.version,
        releaseNotes: normalizeReleaseNotes(result.updateInfo.releaseNotes) ?? directFeedReleaseNotes,
      };
    }
    activeUpdaterPhase = "download";
    pendingUpdateVersion = result.updateInfo.version;
    const token = new CancellationToken();
    activeDownloadCancellation = token;
    await autoUpdater.downloadUpdate(token);
  } finally {
    restoreFeed?.();
  }
}

async function prepareRememberedNonDarwinUpdate(): Promise<void> {
  if (!escalationStateDir) throw new Error("Check for updates before restarting to install.");
  const settings = await reconcileAndPersist(escalationStateDir, await readUpdateSettings(escalationStateDir));
  configureFeed(settings);
  autoUpdater.autoDownload = false;
  broadcastUpdaterStatus({ state: "checking" });
  const restoreFeed = await configureDirectPrereleaseFeed(settings);
  try {
    const result = await checkForUpdatesWithDeadline();
    if (result?.isUpdateAvailable !== true) {
      throw new Error("The remembered update is no longer available. Check for updates and try again.");
    }
    activeUpdaterPhase = "download";
    pendingUpdateVersion = result.updateInfo.version;
    const token = new CancellationToken();
    activeDownloadCancellation = token;
    await autoUpdater.downloadUpdate(token);
  } finally {
    restoreFeed?.();
  }
}

export async function ensureUpdatePrefs(stateDir: string): Promise<void> {
  if (existsSync(path.join(stateDir, UPDATE_SETTINGS_FILE_NAME))) return;

  const installedNightly = installedUpdateChannel() === "nightly";
  const optIn = await dialog.showMessageBox({
    type: "question",
    buttons: ["Enable auto-updates", "Not now"],
    defaultId: 0,
    cancelId: 1,
    message: "Keep Orchestrator up to date automatically?",
    detail: "You can change this later in Settings.",
  });
  if (optIn.response !== 0) {
    await writeUpdateSettings(stateDir, {
      enabled: false,
      channel: installedNightly ? "nightly" : "latest",
      nightlyAck: installedNightly,
      feature: null,
    });
    return;
  }

  const chan = await dialog.showMessageBox({
    type: "question",
    buttons: ["Stable", "Nightly"],
    defaultId: installedNightly ? 1 : 0,
    cancelId: installedNightly ? 1 : 0,
    message: "Which update channel?",
    detail: "Stable is released and tested. Nightly is the newest daily build.",
  });
  if (chan.response !== 1) {
    await writeUpdateSettings(stateDir, {
      enabled: true,
      channel: "latest",
      nightlyAck: false,
      feature: null,
    });
    return;
  }

  const ack = await dialog.showMessageBox({
    type: "warning",
    buttons: ["I understand, use Nightly", "Use Stable instead"],
    defaultId: 1,
    cancelId: 1,
    message: "Nightly builds can be unstable",
    detail:
      "Nightly is built every day and may be broken or lose data. Only use it if you are comfortable with that.",
  });
  await writeUpdateSettings(
    stateDir,
    ack.response === 0
      ? { enabled: true, channel: "nightly", nightlyAck: true, feature: null }
      : { enabled: true, channel: "latest", nightlyAck: false, feature: null },
  );
}
