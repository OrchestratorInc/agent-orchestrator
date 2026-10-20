import type { MetadataRoute } from "next";
import { COMPANY } from "@/lib/constants";
import { source } from "@/lib/source";

export const dynamic = "force-static";

/** Every page has one URL; the reader picks the interface language on the page. */
export default function sitemap(): MetadataRoute.Sitemap {
  return source.getPages().map((page) => ({
    url: `${COMPANY.DOCS_URL}/${page.slugs.length ? `${page.slugs.join("/")}/` : ""}`,
  }));
}
