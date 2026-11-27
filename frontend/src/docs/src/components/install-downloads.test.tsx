import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { downloadUrl } from "@/lib/downloads";
import { InstallDownloads } from "./mdx";

afterEach(cleanup);

describe("InstallDownloads", () => {
  it.each([
    ["macOS Apple silicon", "Orchestrator-darwin-arm64.dmg"],
    ["macOS Intel", "Orchestrator-darwin-x64.dmg"],
  ])("offers %s only the stable disk image", (platform, asset) => {
    render(<InstallDownloads />);
    const row = screen.getByRole("rowheader", { name: platform }).closest("tr")!;
    const links = within(row).getAllByRole("link");
    expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([["Disk image", downloadUrl(asset)]]);
  });

  it("links no macOS zip", () => {
    render(<InstallDownloads />);
    const hrefs = screen.getAllByRole("link").map((link) => link.getAttribute("href") ?? "");
    expect(hrefs.filter((href) => /darwin-.*\.zip$/.test(href))).toEqual([]);
  });
});
