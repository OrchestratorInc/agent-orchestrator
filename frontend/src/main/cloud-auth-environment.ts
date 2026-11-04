// Which AO Cloud sign-in a launch uses.
//
// Production and staging run separate WorkOS environments. A release build
// bakes production's AuthKit client (VITE_WORKOS_CLIENT_ID), so a nightly
// launched with AO_CLOUD_CONTROL_PLANE_URL pointing at staging would sign in to
// production's WorkOS and every staging request would come back 401. When the
// control plane is staging, the app signs in to staging's WorkOS instead,
// returns through staging's bounce page, and keeps that sign-in in its own file
// so it never mixes with the production one. A normal launch is unchanged.

// Staging's AuthKit client. Like every WorkOS client id it is public: it
// appears in every sign-in URL.
export const STAGING_WORKOS_CLIENT_ID = "client_01KZ3VRKC374HS91XGRDPT3671";
export const STAGING_CONTROL_PLANE_HOST = "staging-api.aoagents.dev";

const PRODUCTION_AUTH_RETURN_URI = "https://api.aoagents.dev/app/auth/return";
const STAGING_AUTH_RETURN_URI = "https://staging-api.aoagents.dev/app/auth/return";
const LOOPBACK_REDIRECT_URI = "http://127.0.0.1:3000/callback";

export interface CloudAuthEnvironment {
  staging: boolean;
  clientId: string;
  redirectUri: string;
  // File name, inside the Cloud auth dir, that holds this environment's sign-in.
  storeFile: string;
}

export function isStagingControlPlane(raw: string | undefined): boolean {
  const value = raw?.trim();
  if (!value) return false;
  try {
    const url = new URL(value);
    return url.protocol === "https:" && url.hostname === STAGING_CONTROL_PLANE_HOST;
  } catch {
    return false;
  }
}

export function resolveCloudAuthEnvironment(
  env: Record<string, string | undefined>,
  isPackaged: boolean,
  buildClientId: string,
): CloudAuthEnvironment {
  const staging = isStagingControlPlane(env.AO_CLOUD_CONTROL_PLANE_URL);
  // A development build signs in through the loopback callback whatever the
  // environment; AO_CLOUD_AUTH_REDIRECT still overrides every default.
  const packagedRedirect = staging ? STAGING_AUTH_RETURN_URI : PRODUCTION_AUTH_RETURN_URI;
  return {
    staging,
    clientId: staging ? STAGING_WORKOS_CLIENT_ID : buildClientId,
    redirectUri:
      env.AO_CLOUD_AUTH_REDIRECT?.trim() ||
      (isPackaged ? packagedRedirect : LOOPBACK_REDIRECT_URI),
    storeFile: staging ? "cloud-auth-staging.bin" : "cloud-auth.bin",
  };
}
