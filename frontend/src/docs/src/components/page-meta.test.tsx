import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { cleanup, render, screen, within } from "@testing-library/react";
import type * as PageTree from "fumadocs-core/page-tree";
import { FrameworkProvider } from "fumadocs-core/framework";
import { TreeContextProvider } from "fumadocs-ui/contexts/tree";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { COMPANY } from "@/lib/constants";
import { LastUpdated, PageBreadcrumbs } from "./page-meta";
import { SiteFooter } from "./site-footer";

afterEach(cleanup);

const tree: PageTree.Root = {
  name: "Docs",
  children: [
    { type: "page", name: "Quickstart", url: "/quickstart" },
    {
      type: "folder",
      name: "Built-in capabilities",
      index: { type: "page", name: "Overview", url: "/plugins" },
      children: [
        {
          type: "folder",
          name: "Workspaces",
          index: { type: "page", name: "Workspaces", url: "/plugins/workspaces" },
          children: [{ type: "page", name: "Git worktrees", url: "/plugins/workspaces/worktree" }],
        },
      ],
    },
  ],
  // Pages no sidebar list includes, with the folder names from the file system.
  fallback: {
    name: "Docs",
    children: [
      {
        type: "folder",
        name: "Plugins",
        children: [{ type: "page", name: "Adapter development", url: "/plugins/authoring" }],
      },
    ],
  },
};

function renderAt(pathname: string, ui: ReactNode, locale = "en") {
  const i18n = setupI18n({ locale, messages: { [locale]: {} } });
  return render(
    <FrameworkProvider useRouter={() => ({ push() {}, refresh() {} })} usePathname={() => pathname} useParams={() => ({})}>
      <I18nProvider i18n={i18n}>
        <TreeContextProvider tree={tree}>{ui}</TreeContextProvider>
      </I18nProvider>
    </FrameworkProvider>,
  );
}

describe("PageBreadcrumbs", () => {
  it("links every section above a nested page and marks the page as current", () => {
    renderAt("/plugins/workspaces/worktree/", <PageBreadcrumbs />);
    const trail = screen.getByRole("navigation", { name: "Breadcrumb" });
    const links = within(trail).getAllByRole("link");
    expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
      ["Built-in capabilities", "/plugins"],
      ["Workspaces", "/plugins/workspaces"],
    ]);
    expect(within(trail).getByText("Git worktrees").getAttribute("aria-current")).toBe("page");
  });

  it("links the sidebar's sections above a page the sidebar does not list", () => {
    renderAt("/plugins/authoring/", <PageBreadcrumbs />);
    const trail = screen.getByRole("navigation", { name: "Breadcrumb" });
    const links = within(trail).getAllByRole("link");
    expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([["Built-in capabilities", "/plugins"]]);
    expect(within(trail).getByText("Adapter development").getAttribute("aria-current")).toBe("page");
  });

  it("is hidden on top-level pages", () => {
    renderAt("/quickstart/", <PageBreadcrumbs />);
    expect(screen.queryByRole("navigation", { name: "Breadcrumb" })).toBeNull();
  });
});

describe("LastUpdated", () => {
  it("shows the commit date in the interface language", () => {
    renderAt("/", <LastUpdated date="2026-11-03T23:30:00.000Z" />);
    const time = screen.getByText("Last updated Nov 3, 2026");
    expect(time.getAttribute("dateTime")).toBe("2026-11-03T23:30:00.000Z");
  });
});

describe("SiteFooter", () => {
  it("has the landing site's Product, Community and Legal links", () => {
    renderAt("/ja/", <SiteFooter homeUrl="/ja/" />);
    const hrefs = (name: string) =>
      within(screen.getByRole("navigation", { name })).getAllByRole("link").map((link) => link.getAttribute("href"));
    expect(hrefs("Product")).toEqual([COMPANY.DOWNLOAD_URL, COMPANY.CHANGELOG_URL, "/ja/"]);
    expect(hrefs("Community")).toEqual([COMPANY.GITHUB_URL, COMPANY.DISCORD_URL, COMPANY.X_URL]);
    expect(hrefs("Legal")).toEqual([COMPANY.PRIVACY_URL, COMPANY.REFUNDS_URL, COMPANY.SUPPORT_URL, COMPANY.LICENSE_URL]);
  });

  it("opens the external column links in a new tab", () => {
    renderAt("/", <SiteFooter />);
    const columns = ["Product", "Community", "Legal"].map((name) => screen.getByRole("navigation", { name }));
    for (const link of columns.flatMap((column) => within(column).getAllByRole("link"))) {
      if (link.getAttribute("href")?.startsWith("http")) {
        expect(link.getAttribute("target"), link.getAttribute("href") ?? "").toBe("_blank");
        expect(link.getAttribute("rel")).toContain("noopener");
      }
    }
  });
});
