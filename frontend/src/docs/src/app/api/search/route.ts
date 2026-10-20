import { createFromSource } from "fumadocs-core/search/server";
import { source } from "@/lib/source";
import { visibleProductName } from "@/lib/branding";
import { DEFAULT_LOCALE } from "@/i18n/locales";

// Articles are English in every locale, so one English index serves them
// all; the search dialog adds the reader's locale prefix to each result.
const search = createFromSource({
  ...source,
  _i18n: undefined,
  getPages: () => source.getPages(DEFAULT_LOCALE).map((page) => ({
    ...page,
    data: {
      ...page.data,
      description: page.data.description && visibleProductName(page.data.description),
    },
  })),
});

export const dynamic = "force-static";

export const GET = search.staticGET;
