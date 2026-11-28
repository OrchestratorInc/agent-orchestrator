import { setupI18n } from "@lingui/core";
import { describe, expect, it } from "vitest";
import { COMPANY } from "./constants";
import { baseOptions } from "./layout.shared";

const i18n = setupI18n({ locale: "en", messages: { en: {} } });

describe("baseOptions", () => {
  const options = baseOptions(i18n, "/ja/");
  const links = options.links ?? [];

  it("puts the navbar on top", () => {
    expect(options.nav?.mode).toBe("top");
    expect(options.nav?.url).toBe("/ja/");
  });

  it("links to the changelog, Discord and GitHub in a new tab", () => {
    for (const url of [COMPANY.CHANGELOG_URL, COMPANY.DISCORD_URL, COMPANY.GITHUB_URL]) {
      const link = links.find((item) => "url" in item && item.url === url);
      expect(link, url).toBeDefined();
      expect(link && "external" in link && link.external).toBe(true);
    }
  });

  it("gives the icon links accessible names", () => {
    const icons = links.filter((item) => item.type === "icon");
    expect(icons.map((item) => item.type === "icon" && item.label)).toEqual(["Discord", "GitHub"]);
  });

  it("keeps Download in the navbar and in the mobile menu", () => {
    expect(links.some((item) => item.type === "custom" && item.on === "nav")).toBe(true);
    expect(links.some((item) => "url" in item && item.url === COMPANY.DOWNLOAD_URL && item.on === "menu")).toBe(true);
  });
});
