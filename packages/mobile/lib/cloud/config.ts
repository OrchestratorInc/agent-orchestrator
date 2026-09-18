/**
 * The AuthKit client id is public configuration — it appears in every sign-in
 * URL — so a baked default keeps sign-in working with no build-time setup,
 * exactly as the desktop app does (frontend/src/main/cloud-auth.ts).
 */
export const WORKOS_CLIENT_ID =
	process.env.EXPO_PUBLIC_WORKOS_CLIENT_ID?.trim() || "client_01KZ3VRKC374HS91XGRDPT3671";

/** Control plane the app signs into. Overridable for local Docker development. */
export const CLOUD_BASE_URL =
	process.env.EXPO_PUBLIC_AO_CLOUD_URL?.trim() || "https://staging-api.aoagents.dev";

// Confirmed against cloud/internal/httpapi/server.go:323 — router.Route("/api/cloud/v1", ...).
export const CLOUD_API_PREFIX = "/api/cloud/v1";
export const WORKOS_REDIRECT_URI = "aomobile://callback";
