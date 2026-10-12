import { describe, expect, it } from "vitest";
import { cloudAccountScopeKey, resolveCloudDeployment } from "./deployment";
import profiles from "../../eas.json";

describe("Cloud deployment", () => {
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
