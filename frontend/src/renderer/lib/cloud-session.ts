import { useEffect, useState } from "react";
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

// The session the app last resolved. A component that mounts later starts from
// it instead of "loading" while its own getSession round trip runs; otherwise
// every newly opened view briefly reads as signed out, and Cloud data it gates
// (projects, a just-created session) drops out until the bridge answers.
let lastKnownSession: { session: CloudAccount | null } | undefined;

function rememberSession(session: CloudAccount | null): void {
  lastKnownSession = { session };
}

function statusOf(session: CloudAccount | null): CloudSessionStatus {
  return session ? "authenticated" : "unauthenticated";
}

export function useCloudSession(): UseCloudSessionResult {
  const configured = isCloudSignInConfigured();
  const [session, setSession] = useState<CloudAccount | null>(() => lastKnownSession?.session ?? null);
  const [status, setStatus] = useState<CloudSessionStatus>(() => lastKnownSession ? statusOf(lastKnownSession.session) : "loading");

  useEffect(() => {
    let active = true;
    // Component tests and browser preview can provide only the local bridge.
    // Treat a missing Cloud preload surface as signed out rather than throwing
    // from every consumer that mounts useCloudCp.
    const cloud = aoBridge.cloud;
    if (!cloud) {
      setSession(null);
      setStatus("unauthenticated");
      return () => {
        active = false;
      };
    }
    cloud.getSession().then((s) => {
      noteCloudSession(s, true);
      rememberSession(s);
      if (!active) return;
      setSession(s);
      setStatus(s ? "authenticated" : "unauthenticated");
    }).catch(() => {
      if (!active) return;
      setSession(null);
      setStatus("unauthenticated");
    });

    const unsub = cloud.onSessionChanged((s) => {
      // A change that brings a new signed-in user is a completed sign-in;
      // token refreshes re-send the same user and are not counted.
      noteCloudSession(s, false);
      rememberSession(s);
      setSession(s);
      setStatus(s ? "authenticated" : "unauthenticated");
    });

    return () => {
      active = false;
      unsub();
    };
  }, []);

  const signIn = () => {
    void aoBridge.cloud.signIn();
  };

  const signOut = async () => {
    await aoBridge.cloud.signOut();
    rememberSession(null);
    setSession(null);
    setStatus("unauthenticated");
  };

  return { configured, session, status, signIn, signOut };
}
