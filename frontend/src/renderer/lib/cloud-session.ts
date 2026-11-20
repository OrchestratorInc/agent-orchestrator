import { useEffect, useSyncExternalStore } from "react";
import { aoBridge } from "./bridge";
import { noteCloudSession } from "./cloud-telemetry";
import type { CloudAccount } from "../../shared/cloud-account";

export type { CloudAccount };

export type CloudSessionStatus = "loading" | "authenticated" | "unauthenticated";

export interface UseCloudSessionResult {
  configured: boolean;
  session: CloudAccount | null;
  status: CloudSessionStatus;
  signIn: (returnTo?: string) => void;
  signOut: () => Promise<void>;
}

// The WorkOS AuthKit client id is public configuration (it appears in every
// sign-in URL), so a baked default keeps sign-in working without build-time
// setup; VITE_WORKOS_CLIENT_ID overrides it per build when needed.
const DEFAULT_WORKOS_CLIENT_ID = "client_01KZ3VRKC374HS91XGRDPT3671";

export function isCloudSignInConfigured(
  clientId = import.meta.env.VITE_WORKOS_CLIENT_ID ?? DEFAULT_WORKOS_CLIENT_ID,
): boolean {
  return Boolean(clientId?.trim());
}

// One store for the whole renderer. Many components read the session at once
// (every artifact card does, through useCloudCp), so they share one bridge
// subscription and one getSession round trip instead of each making their own.
// A component that mounts later starts from the last resolved session instead
// of "loading"; otherwise every newly opened view briefly reads as signed out,
// and Cloud data it gates (projects, a just-created session) drops out until
// the bridge answers.
type CloudSessionSnapshot = { session: CloudAccount | null; status: CloudSessionStatus };

/** A mount within this long of the last read reuses it; later mounts read again. */
const SESSION_REUSE_MS = 5_000;
/** The first read again after a failed one, doubling each time it fails again, up to the max. */
const FAILED_READ_RETRY_MS = 1_000;
const FAILED_READ_RETRY_MAX_MS = 30_000;
/** Failed reads in a row, with nothing known yet, after which signed out shows instead of loading. */
const FAILED_READS_BEFORE_SIGNED_OUT = 3;

let snapshot: CloudSessionSnapshot = { session: null, status: "loading" };
let snapshotKey: string | undefined;
let readAt = 0;
let reading: Promise<void> | undefined;
// Counts the session changes main pushed and the sign-outs here. A read answers as of when it
// started: one that finishes after a newer change (a sign-out, say) would bring the old account
// back, so it is dropped.
let changes = 0;
let failedReads = 0;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
// Signed out shows only because reads kept failing, not because main said so,
// so reads go on until one answers.
let signedOutByFailedReads = false;
let unsubscribeBridge: (() => void) | undefined;
const listeners = new Set<() => void>();

function statusOf(session: CloudAccount | null): CloudSessionStatus {
  return session ? "authenticated" : "unauthenticated";
}

/** Publishes a change that supersedes every read still in flight. */
function publishChange(session: CloudAccount | null): void {
  changes += 1;
  signedOutByFailedReads = false;
  publish(session);
}

// Each answer from main is a new object; keep the old snapshot when nothing
// changed, so a repeated read re-renders no reader.
function publish(session: CloudAccount | null): void {
  const key = JSON.stringify(session);
  if (key === snapshotKey && snapshot.status !== "loading") return;
  snapshotKey = key;
  snapshot = { session, status: statusOf(session) };
  for (const listener of listeners) listener();
}

function readSession(force = false): void {
  const cloud = aoBridge.cloud;
  // Component tests and browser preview can provide only the local bridge.
  // Treat a missing Cloud preload surface as signed out rather than throwing
  // from every consumer that mounts useCloudCp.
  if (!cloud) {
    publish(null);
    return;
  }
  // Only an answer from main is reused. While reads are failing, a mount reads
  // again, so recovery resumes even if the retry timer fired with no reader.
  if (reading || (!force && !awaitingRead() && Date.now() - readAt < SESSION_REUSE_MS)) return;
  const startedAt = changes;
  reading = cloud.getSession().then((s) => {
    failedReads = 0;
    signedOutByFailedReads = false;
    if (startedAt !== changes) return;
    noteCloudSession(s, true);
    publish(s);
  }, () => {
    // Main could not read the saved login just then (it answers a missing one
    // with null, not an error). That is not a sign-out: keep what is shown,
    // and if nothing is known yet, read again shortly. After a few failures,
    // show signed out rather than loading forever, and keep reading.
    failedReads += 1;
    if (startedAt !== changes) return;
    if (snapshot.status === "loading" && failedReads >= FAILED_READS_BEFORE_SIGNED_OUT) {
      signedOutByFailedReads = true;
      publish(null);
    }
    if (awaitingRead()) retryFailedRead();
  }).finally(() => {
    readAt = Date.now();
    reading = undefined;
  });
}

function awaitingRead(): boolean {
  return snapshot.status === "loading" || signedOutByFailedReads;
}

function retryFailedRead(): void {
  if (retryTimer) return;
  const delay = Math.min(FAILED_READ_RETRY_MS * 2 ** (failedReads - 1), FAILED_READ_RETRY_MAX_MS);
  retryTimer = setTimeout(() => {
    retryTimer = undefined;
    if (listeners.size > 0 && awaitingRead()) readSession(true);
  }, delay);
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (!unsubscribeBridge && aoBridge.cloud) {
    unsubscribeBridge = aoBridge.cloud.onSessionChanged((s) => {
      // A change that brings a new signed-in user is a completed sign-in;
      // token refreshes re-send the same user and are not counted.
      noteCloudSession(s, false);
      publishChange(s);
    });
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size > 0) return;
    unsubscribeBridge?.();
    unsubscribeBridge = undefined;
  };
}

const getSnapshot = () => snapshot;

export function useCloudSession(): UseCloudSessionResult {
  const configured = isCloudSignInConfigured();
  const { session, status } = useSyncExternalStore(subscribe, getSnapshot);

  // A mount reads the session again (main refreshes a stale token on a read),
  // unless a read is in flight or finished within SESSION_REUSE_MS.
  useEffect(readSession, []);

  const signIn = () => {
    void aoBridge.cloud.signIn();
  };

  const signOut = async () => {
    await aoBridge.cloud.signOut();
    publishChange(null);
  };

  return { configured, session, status, signIn, signOut };
}
