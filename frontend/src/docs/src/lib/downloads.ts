/** Where `releases/latest/download/<asset>` links point. */
export const RELEASE_DOWNLOAD_URL = `https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download`;

/** The desktop release assets, by platform: `[label, asset file name]`. */
export const DESKTOP_DOWNLOADS: { platform: string; assets: [label: string, asset: string][] }[] = [
  { platform: "macOS Apple silicon", assets: [["Disk image", "Orchestrator-darwin-arm64.dmg"], ["Zip", "agent-orchestrator-darwin-arm64.zip"]] },
  { platform: "macOS Intel", assets: [["Disk image", "Orchestrator-darwin-x64.dmg"], ["Zip", "agent-orchestrator-darwin-x64.zip"]] },
  { platform: "Windows x64", assets: [["Installer", "agent-orchestrator-win32-x64.exe"]] },
  {
    platform: "Linux x64",
    assets: [["AppImage", "agent-orchestrator-linux-x64.AppImage"], ["Debian / Ubuntu", "agent-orchestrator-linux-x64.deb"], ["Fedora / RHEL", "agent-orchestrator-linux-x64.rpm"]],
  },
];

export function downloadUrl(asset: string): string {
  return `${RELEASE_DOWNLOAD_URL}/${asset}`;
}
