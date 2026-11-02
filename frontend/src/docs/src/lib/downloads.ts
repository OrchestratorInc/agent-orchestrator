const RELEASE_URL = "https://github.com/Untrivial-ai/agent-orchestrator/releases/latest/download";

/** The desktop packages of the latest release, by platform. Shown on the installation page and in its Markdown copy. */
export const DOWNLOADS: { platform: string; assets: { label: string; url: string }[] }[] = [
  { platform: "macOS Apple silicon", assets: [["Disk image", "darwin-arm64.dmg"], ["Zip", "darwin-arm64.zip"]] },
  { platform: "macOS Intel", assets: [["Disk image", "darwin-x64.dmg"], ["Zip", "darwin-x64.zip"]] },
  { platform: "Windows x64", assets: [["Installer", "win32-x64.exe"]] },
  { platform: "Linux x64", assets: [["AppImage", "linux-x64.AppImage"], ["Debian / Ubuntu", "linux-x64.deb"], ["Fedora / RHEL", "linux-x64.rpm"]] },
].map(({ platform, assets }) => ({
  platform,
  assets: assets.map(([label, asset]) => ({ label: label!, url: `${RELEASE_URL}/agent-orchestrator-${asset}` })),
}));
