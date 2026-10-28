import { describe, expect, it } from "vitest";
import {
  STAGING_WORKOS_CLIENT_ID,
  isStagingControlPlane,
  resolveCloudAuthEnvironment,
} from "./cloud-auth-environment";

const PRODUCTION_CLIENT_ID = "client_production";

describe("resolveCloudAuthEnvironment", () => {
  it("keeps a normal packaged launch on the build's WorkOS client and production return page", () => {
    expect(resolveCloudAuthEnvironment({}, true, PRODUCTION_CLIENT_ID)).toEqual({
      staging: false,
      clientId: PRODUCTION_CLIENT_ID,
      redirectUri: "https://api.aoagents.dev/app/auth/return",
      storeFile: "cloud-auth.bin",
    });
  });

  it("signs a packaged build pointed at staging in to staging's WorkOS, in its own file", () => {
    const env = { AO_CLOUD_CONTROL_PLANE_URL: "https://staging-api.aoagents.dev" };
    expect(resolveCloudAuthEnvironment(env, true, PRODUCTION_CLIENT_ID)).toEqual({
      staging: true,
      clientId: STAGING_WORKOS_CLIENT_ID,
      redirectUri: "https://staging-api.aoagents.dev/app/auth/return",
      storeFile: "cloud-auth-staging.bin",
    });
  });

  it("treats production and other control planes as not staging", () => {
    for (const url of [
      "https://api.aoagents.dev",
      "http://127.0.0.1:8081",
      "https://staging-api.aoagents.dev.example.com",
      "http://staging-api.aoagents.dev",
      "not a url",
    ]) {
      const result = resolveCloudAuthEnvironment({ AO_CLOUD_CONTROL_PLANE_URL: url }, true, PRODUCTION_CLIENT_ID);
      expect(result.clientId, url).toBe(PRODUCTION_CLIENT_ID);
      expect(result.storeFile, url).toBe("cloud-auth.bin");
    }
  });

  it("keeps a development build on the loopback callback, even for staging", () => {
    const env = { AO_CLOUD_CONTROL_PLANE_URL: "https://staging-api.aoagents.dev" };
    const result = resolveCloudAuthEnvironment(env, false, PRODUCTION_CLIENT_ID);
    expect(result.redirectUri).toBe("http://127.0.0.1:3000/callback");
    expect(result.clientId).toBe(STAGING_WORKOS_CLIENT_ID);
  });

  it("lets AO_CLOUD_AUTH_REDIRECT override the return page", () => {
    const env = {
      AO_CLOUD_CONTROL_PLANE_URL: "https://staging-api.aoagents.dev",
      AO_CLOUD_AUTH_REDIRECT: " ao-app://callback ",
    };
    expect(resolveCloudAuthEnvironment(env, true, PRODUCTION_CLIENT_ID).redirectUri).toBe("ao-app://callback");
  });
});

describe("isStagingControlPlane", () => {
  it("accepts the staging host on any path or port over HTTPS", () => {
    expect(isStagingControlPlane("https://staging-api.aoagents.dev")).toBe(true);
    expect(isStagingControlPlane(" https://staging-api.aoagents.dev:8443/ ")).toBe(true);
    expect(isStagingControlPlane(undefined)).toBe(false);
    expect(isStagingControlPlane("")).toBe(false);
  });
});
