import { GitHubProvider } from "electron-updater/out/providers/GitHubProvider";
import type { AppUpdater } from "electron-updater/out/AppUpdater";
import type { ProviderRuntimeOptions } from "electron-updater/out/providers/Provider";
import type { CustomPublishOptions } from "builder-util-runtime";

export class StableGitHubProvider extends GitHubProvider {
  constructor(options: CustomPublishOptions, updater: AppUpdater, runtime: ProviderRuntimeOptions) {
    const { owner, repo } = options;
    if (typeof owner !== "string" || typeof repo !== "string") {
      throw new Error("Stable GitHub provider requires an owner and repository");
    }
    super({ provider: "github", owner, repo }, updater, runtime);
  }

  override async getLatestVersion() {
    const info = await super.getLatestVersion();
    let notes: string | undefined;
    // GitHub's Atom feed contains only ten releases. If stable has fallen out,
    // the stock provider silently pairs its download with the first nightly's notes.
    try {
      const response = await fetch(
        `https://api.github.com/repos/${this.options.owner}/${this.options.repo}/releases/tags/${encodeURIComponent(info.tag)}`,
        {
          headers: { Accept: "application/vnd.github+json" },
          signal: AbortSignal.timeout(5000),
        },
      );
      if (response.ok) {
        const release: unknown = await response.json();
        if (release !== null && typeof release === "object" &&
          "tag_name" in release && release.tag_name === info.tag &&
          "draft" in release && release.draft === false &&
          "prerelease" in release && release.prerelease === false &&
          "body" in release && typeof release.body === "string") {
          notes = release.body;
        }
      }
    } catch {
      // Optional notes must never prevent an update or reuse another build's notes.
    }
    return { ...info, releaseNotes: notes };
  }
}
