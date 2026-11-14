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

const DEFAULT_WORKOS_CLIENT_ID = "client_01KZ3VRKC374HS91XGRDPT3671";

export function isCloudSignInConfigured(
  clientId = import.meta.env.VITE_WORKOS_CLIENT_ID ?? DEFAULT_WORKOS_CLIENT_ID,
): boolean {
  return Boolean(clientId?.trim());
}

type CloudSessionSnapshot = { session: CloudAccount | null; status: CloudSessionStatus };

const SESSION_REUSE_MS = 5_000;

let snapshot: CloudSessionSnapshot = { session: null, status: "loading" };
let snapshotKey: string | undefined;
let readAt = 0;
let reading: Promise<void> | undefined;
let changes = 0;
let unsubscribeBridge: (() => void) | undefined;
const listeners = new Set<() => void>();

function statusOf(session: CloudAccount | null): CloudSessionStatus {
  return session ? "authenticated" : "unauthenticated";
}

function publishChange(session: CloudAccount | null): void {
  changes += 1;
  publish(session);
}

function publish(session: CloudAccount | null): void {
  const key = JSON.stringify(session);
  if (key === snapshotKey && snapshot.status !== "loading") return;
  snapshotKey = key;
  snapshot = { session, status: statusOf(session) };
  for (const listener of listeners) listener();
}

function readSession(): void {
  const cloud = aoBridge.cloud;
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
