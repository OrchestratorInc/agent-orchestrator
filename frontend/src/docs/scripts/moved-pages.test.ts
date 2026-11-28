// @vitest-environment node
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Old top-level guide URLs (docs.orchestrator.inc/review-loop and so on) that
// other sites still link to. GitHub Pages has no server redirects, so each one
// is a static page in public/<slug>/index.html with an immediate meta refresh
// and a canonical link to the guide; Google treats that as a permanent
// redirect. Without them the links land on a 404 (Search Console, Nov 2026).
const MOVED = ["review-loop", "per-role-agents", "parallel-issues"];

const root = fileURLToPath(new URL("..", import.meta.url));

describe("moved guide pages", () => {
  it.each(MOVED)("/%s/ redirects to an existing guide", (slug) => {
    const html = readFileSync(join(root, "public", slug, "index.html"), "utf8");
    const target = `/guides/${slug}/`;
    expect(existsSync(join(root, "content", "guides", `${slug}.mdx`))).toBe(true);
    expect(html).toContain(`<meta http-equiv="refresh" content="0; url=${target}">`);
    expect(html).toContain(`<link rel="canonical" href="https://docs.orchestrator.inc${target}">`);
    expect(html).not.toContain("noindex");
  });

  it("does not shadow a real page", () => {
    const pages = readdirSync(join(root, "content")).map((name) => name.replace(/(\.[a-z-]+)?\.mdx$/i, ""));
    for (const slug of MOVED) expect(pages).not.toContain(slug);
  });
});
