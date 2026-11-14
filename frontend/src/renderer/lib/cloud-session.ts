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

let snapshot: CloudSessionSnapshot = { session: null, status: "loading" };
let snapshotKey: string | undefined;
let readAt = 0;
let reading: Promise<void> | undefined;
// Counts the session changes main pushed and the sign-outs here. A read answers as of when it
// started: one that finishes after a newer change (a sign-out, say) would bring the old account
// back, so it is dropped.
let changes = 0;
let unsubscribeBridge: (() => void) | undefined;
const listeners = new Set<() => void>();

function statusOf(session: CloudAccount | null): CloudSessionStatus {
  return session ? "authenticated" : "unauthenticated";
}

/** Publishes a change that supersedes every read still in flight. */
function publishChange(session: CloudAccount | null): void {
  changes += 1;
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

function readSession(): void {
  const cloud = aoBridge.cloud;
  // Component tests and browser preview can provide only the local bridge.
  // Treat a missing Cloud preload surface as signed out rather than throwing
  // from every consumer that mounts useCloudCp.
  if (!cloud) {
    publish(null);
    return;
  }
  if (reading || (snapshot.status !== "loading" && Date.now() - readAt < SESSION_REUSE_MS)) return;
  const startedAt = changes;
  reading = cloud.getSession().then((s) => {
    if (startedAt !== changes) return;
    noteCloudSession(s, true);
    publish(s);
  }, () => {
    if (startedAt === changes) publish(null);
  }).finally(() => {
    readAt = Date.now();
    reading = undefined;
  });
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
