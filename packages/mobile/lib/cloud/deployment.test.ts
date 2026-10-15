import { describe, expect, it } from "vitest";
import { cloudAccountScopeKey, resolveCloudDeployment } from "./deployment";
import profiles from "../../eas.json";
import { buildWorkOSAuthUrl } from "./signIn";

describe("Cloud deployment", () => {
  it.each([true, false])("uses production AuthKit for the production API in development=%s", (development) => {
    const deployment = resolveCloudDeployment({ cloudUrl: "https://api.aoagents.dev/", development });
    const url = new URL(buildWorkOSAuthUrl({ clientId: deployment.clientId, redirectUri: deployment.redirectUri, codeChallenge: "challenge", state: "state" }));
    expect(url.searchParams.get("client_id")).toBe("client_01KZ3VRKTDKBZ3JA8AMKTF6W2X");
    expect(url.searchParams.get("redirect_uri")).toBe("https://api.aoagents.dev/app/auth/return");
    expect(deployment.callbackUri).toBe("ao-app://callback");
  });
  it("keeps staging's registered native login separate from production", () => {
    const deployment = resolveCloudDeployment({ development: true });
    const url = new URL(buildWorkOSAuthUrl({ clientId: deployment.clientId, redirectUri: deployment.redirectUri, codeChallenge: "challenge", state: "state" }));
    expect(url.searchParams.get("client_id")).toBe("client_01KZ3VRKC374HS91XGRDPT3671");
    expect(url.searchParams.get("redirect_uri")).toBe("aoagents.ao://callback");
    expect(deployment.callbackUri).toBe("aoagents.ao://callback");
  });
  it("retains explicit AuthKit client and native redirect overrides", () => {
    const deployment = resolveCloudDeployment({ cloudUrl: "https://api.aoagents.dev", clientId: " client-custom ", redirectUri: " aoagents.ao://callback ", development: true });
    expect(deployment.clientId).toBe("client-custom");
    expect(deployment.redirectUri).toBe("aoagents.ao://callback");
    expect(deployment.callbackUri).toBe("aoagents.ao://callback");
  });
  it("does not reuse staging or older production credentials for the new login configuration", () => {
    const production = resolveCloudDeployment({ development: false });
    const staging = resolveCloudDeployment({ development: true });
    const oldProduction = JSON.stringify(["https://api.aoagents.dev", "client_01KZ3VRKC374HS91XGRDPT3671", "aoagents.ao://callback", "workos"]);
    expect(production.key).not.toBe(staging.key);
    expect(production.key).not.toBe(oldProduction);
  });
  it("does not mix account discovery and sessions across deployments", () => {
    expect(() => resolveCloudDeployment({ cloudUrl: "https://staging-api.aoagents.dev", accountUrl: "https://api.aoagents.dev", development: true })).toThrow("Cloud deployment settings disagree");
  });
  it("normalizes matching settings and keeps account identity in scope", () => {
    const deployment = resolveCloudDeployment({ cloudUrl: "https://cloud.test/", accountUrl: "https://cloud.test", development: true });
    expect(deployment.baseUrl).toBe("https://cloud.test");
    expect(deployment.redirectUri).toBe("aoagents.ao://callback");
    expect(cloudAccountScopeKey({ baseUrl: deployment.baseUrl, userId: "u1", orgId: "o1" })).not.toBe(cloudAccountScopeKey({ baseUrl: deployment.baseUrl, userId: "u2", orgId: "o1" }));
  });
  it.each(["https://user:secret@cloud.test", "https://cloud.test?env=prod", "https://cloud.test#fragment", "http://cloud.test"])("rejects unsafe API URL %s", (cloudUrl) => {
    expect(() => resolveCloudDeployment({ cloudUrl, development: true })).toThrow();
  });
  it("only allows cleartext loopback APIs in development", () => {
    expect(resolveCloudDeployment({ cloudUrl: "http://127.0.0.1:8080", development: true }).authKind).toBe("local");
    expect(() => resolveCloudDeployment({ cloudUrl: "http://127.0.0.1:8080", development: false })).toThrow();
  });
  it("ships explicit staging test profiles and production release configuration", () => {
    expect(profiles.build.production.env.EXPO_PUBLIC_AO_CLOUD_URL).toBe("https://api.aoagents.dev");
    expect(profiles.build.development.env.EXPO_PUBLIC_AO_CLOUD_URL).toBe("https://staging-api.aoagents.dev");
    expect(profiles.build.preview.env.EXPO_PUBLIC_AO_CLOUD_URL).toBe("https://staging-api.aoagents.dev");
  });
});
