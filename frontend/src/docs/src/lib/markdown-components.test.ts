import { describe, expect, it } from "vitest";
import { DESKTOP_DOWNLOADS, downloadUrl } from "./downloads";
import { expandComponents, installDownloadsMarkdown } from "./markdown-components";

describe("expandComponents", () => {
  it("turns <InstallDownloads /> into direct release download links", () => {
    const out = expandComponents("Start here.\n\n<InstallDownloads />\n\nThen open the app.");
    expect(out).not.toContain("<InstallDownloads");
    for (const { assets } of DESKTOP_DOWNLOADS) {
      for (const [, asset] of assets) expect(out).toContain(`(${downloadUrl(asset)})`);
    }
    expect(out).toContain(
      "- macOS Apple silicon: [Disk image](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/Orchestrator-darwin-arm64.dmg)",
    );
    expect(out.startsWith("Start here.\n\nDownload the latest")).toBe(true);
    expect(out.endsWith("\n\nThen open the app.")).toBe(true);
  });

  it("lists every platform once", () => {
    const items = installDownloadsMarkdown().split("\n").filter((line) => line.startsWith("- "));
    expect(items).toHaveLength(DESKTOP_DOWNLOADS.length);
  });

  it("turns cards into a list of absolute Markdown links", () => {
    const input = [
      "<Cards>",
      '  <Card title="Quickstart" description="Run one worker." href="/quickstart" />',
      "",
      '  <Card title="Remote" href="/configuration/remote-access#pairing" description="Pair a phone." />',
      "",
      '  <Card title="Home" href="/" />',
      "</Cards>",
    ].join("\n");
    expect(expandComponents(input)).toBe(
      [
        "- [Quickstart](https://docs.orchestrator.inc/quickstart.md): Run one worker.",
        "- [Remote](https://docs.orchestrator.inc/configuration/remote-access.md#pairing): Pair a phone.",
        "- [Home](https://docs.orchestrator.inc/index.md)",
      ].join("\n"),
    );
  });

  it("spells out <PlatformSupport /> statuses and its note", () => {
    const out = expandComponents('<PlatformSupport macos="full" linux="partial" windows="none" note="<>Agents vary.</>" />');
    expect(out).toBe(["Platform support:", "", "- macOS: Supported", "- Linux: Limited", "- Windows: Not supported", "", "Agents vary."].join("\n"));
  });

  it("leaves components inside fenced code blocks alone", () => {
    const input = ["```mdx", "<InstallDownloads />", '<Card title="x" href="/x" />', "```"].join("\n");
    expect(expandComponents(input)).toBe(input);
  });

  it("leaves components inside a fence that starts in a list item alone", () => {
    const input = ["* ```mdx", "  <InstallDownloads />", "  ```", "", "<InstallDownloads />"].join("\n");
    const out = expandComponents(input);
    expect(out.startsWith(["* ```mdx", "  <InstallDownloads />", "  ```", ""].join("\n"))).toBe(true);
    expect(out).toContain("Download the latest Orchestrator desktop release:");
    expect(out.match(/<InstallDownloads/g)).toHaveLength(1);
  });

  it("converts tags that span several lines", () => {
    const input = ['<PlatformSupport macos="full" linux="full" windows="partial" note="<>First line', 'Second line</>" />', "", "Next."].join("\n");
    expect(expandComponents(input)).toBe(
      ["Platform support:", "", "- macOS: Supported", "- Linux: Supported", "- Windows: Limited", "", "First line\nSecond line", "", "Next."].join("\n"),
    );
    const card = ["<Cards>", '  <Card', '    title="Quickstart"', '    href="/quickstart"', "  />", "</Cards>"].join("\n");
    expect(expandComponents(card)).toBe("- [Quickstart](https://docs.orchestrator.inc/quickstart.md)");
  });

  it("keeps other Markdown and components unchanged", () => {
    const input = '## Heading\n\n<Steps>\n  <Step>\n    Do it.\n  </Step>\n</Steps>\n\n- a\n\n- b';
    expect(expandComponents(input)).toBe(input);
  });
});
