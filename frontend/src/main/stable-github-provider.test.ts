// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { NsisUpdater } from "electron-updater";
import { ElectronHttpExecutor } from "electron-updater/out/electronHttpExecutor";
import { GitHubProvider } from "electron-updater/out/providers/GitHubProvider";
import type { ProviderRuntimeOptions } from "electron-updater/out/providers/Provider";
import { StableGitHubProvider } from "./stable-github-provider";

const tag = "v0.14.10";
const nightlyNotes = "Changes in this nightly";
const feed = `<feed>${Array.from({ length: 10 }, (_, i) => `<entry>
  <title>Nightly</title><link href="https://github.com/OrchestratorInc/agent-orchestrator/releases/tag/v0.14.11-nightly.${202611101246 - i}"/>
  <content>${nightlyNotes}</content></entry>`).join("")}</feed>`;
const manifest = "version: 0.14.10\nfiles:\n  - url: stable.zip\n    sha512: checksum\npath: stable.zip\nsha512: checksum\nreleaseDate: 2026-11-06T00:00:00.000Z\n";

function providers(platform: ProviderRuntimeOptions["platform"] = "darwin") {
  vi.stubEnv("TEST_UPDATER_ARCH", "x64");
  const updater = new NsisUpdater(null, {
    version: "0.14.9", name: "AO", isPackaged: true,
    appUpdateConfigPath: "/unused", userDataPath: "/unused", baseCachePath: "/unused",
    whenReady: async () => undefined, relaunch: vi.fn(), quit: vi.fn(), onQuit: vi.fn(),
  });
  updater.channel = "latest";
  updater.allowPrerelease = false;
  const executor = new ElectronHttpExecutor();
  vi.spyOn(executor, "request").mockImplementation(async (options) => {
    if (options.path?.endsWith(".atom")) return feed;
    if (options.path?.endsWith("/latest")) return JSON.stringify({ tag_name: tag });
    if (options.path?.endsWith(platform === "darwin" ? "/latest-mac.yml" : platform === "linux" ? "/latest-linux.yml" : "/latest.yml")) return manifest;
    throw new Error(`Unexpected metadata request: ${options.path}`);
  });
  const runtime = { platform, executor, isUseMultipleRangeRequest: false } satisfies ConstructorParameters<typeof GitHubProvider>[2];
  const repo = { owner: "OrchestratorInc", repo: "agent-orchestrator" };
  return {
    stock: new GitHubProvider({ provider: "github", ...repo }, updater, runtime),
    fixed: new StableGitHubProvider({ provider: "custom", ...repo }, updater, runtime),
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("stable notes when ten nightlies have displaced stable from the Atom feed", () => {
  it("reproduces the dependency bug while retaining the stable download", async () => {
    const { stock } = providers();
    expect(await stock.getLatestVersion()).toMatchObject({ tag, version: "0.14.10", releaseNotes: nightlyNotes });
  });

  it.each(["darwin", "linux", "win32"] satisfies ProviderRuntimeOptions["platform"][])("uses exact stable notes and preserves download metadata and URLs on %s", async (platform) => {
    const fetchNotes = vi.fn(async () => new Response(JSON.stringify({ tag_name: tag, draft: false, prerelease: false, body: "Stable fixes" })));
    vi.stubGlobal("fetch", fetchNotes);
    const { stock, fixed } = providers(platform);
    const before = await stock.getLatestVersion();
    const after = await fixed.getLatestVersion();
    expect(after).toEqual({ ...before, releaseNotes: "Stable fixes" });
    expect(fixed.resolveFiles(after)).toEqual(stock.resolveFiles(before));
    expect(fetchNotes).toHaveBeenCalledWith(
      `https://api.github.com/repos/OrchestratorInc/agent-orchestrator/releases/tags/${tag}`,
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });

  it.each([
    ["rate limit", () => new Response("rate limited", { status: 403 })],
    ["missing release", () => new Response("missing", { status: 404 })],
    ["invalid JSON", () => new Response("invalid")],
    ["wrong tag", () => Response.json({ tag_name: "v0.14.11", draft: false, prerelease: false, body: nightlyNotes })],
    ["prerelease", () => Response.json({ tag_name: tag, draft: false, prerelease: true, body: nightlyNotes })],
    ["draft", () => Response.json({ tag_name: tag, draft: true, prerelease: false, body: nightlyNotes })],
    ["no notes", () => Response.json({ tag_name: tag, draft: false, prerelease: false, body: null })],
    ["network failure", () => { throw new Error("offline"); }],
  ])("omits unverified notes on %s without blocking the stable update", async (_reason, response) => {
    vi.stubGlobal("fetch", vi.fn(async () => response()));
    const { fixed } = providers();
    expect(await fixed.getLatestVersion()).toMatchObject({ tag, version: "0.14.10", releaseNotes: undefined });
  });

  it("does not retain notes from an earlier successful check after a fetch failure", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(Response.json({ tag_name: tag, draft: false, prerelease: false, body: "Stable fixes" }))
      .mockRejectedValueOnce(new Error("offline")));
    const { fixed } = providers();
    expect((await fixed.getLatestVersion()).releaseNotes).toBe("Stable fixes");
    expect((await fixed.getLatestVersion()).releaseNotes).toBeUndefined();
  });
});
