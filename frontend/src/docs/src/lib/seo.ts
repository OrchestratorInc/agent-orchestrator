import { getBreadcrumbItems } from "fumadocs-core/breadcrumb";
import type { SupportedLocale } from "@/i18n/locales";
import { localePath, withTrailingSlash } from "@/i18n/paths";
import { COMPANY } from "./constants";
import { docsTree } from "./docs-tree";

/** The `og:locale` of each language, in the `language_TERRITORY` form link previews expect. */
export const OG_LOCALES: Record<SupportedLocale, string> = {
  en: "en_US",
  ja: "ja_JP",
  "zh-CN": "zh_CN",
  "zh-TW": "zh_TW",
  ko: "ko_KR",
  fr: "fr_FR",
  es: "es_ES",
  de: "de_DE",
  "pt-BR": "pt_BR",
  it: "it_IT",
  ru: "ru_RU",
  tr: "tr_TR",
  pl: "pl_PL",
  nl: "nl_NL",
  id: "id_ID",
  cs: "cs_CZ",
  vi: "vi_VN",
};

export const OG_IMAGE_SIZE = { width: 1200, height: 630 };

/**
 * The preview image of an article, e.g. `/og/quickstart/image.png`. There is
 * one per English article; translations share it.
 */
export function ogImagePath(slugs: string[]): string {
  return `/og/${[...slugs, "image.png"].join("/")}`;
}

/** An absolute docs URL, for structured data. */
export function absoluteUrl(path: string): string {
  return `${COMPANY.DOCS_URL}${path}`;
}

// The name search engines and link previews show, with the names the
// product is also searched by.
export const BRAND = "Orchestrator.inc";
const ALTERNATE_NAMES = ["Agent Orchestrator", "AO"];

/** The site name, e.g. in `og:site_name`. */
export const SITE_NAME = `${BRAND} Docs`;

const organization = {
  "@type": "Organization",
  "@id": `${COMPANY.MARKETING_URL}/#organization`,
  name: BRAND,
  alternateName: ALTERNATE_NAMES,
  url: COMPANY.MARKETING_URL,
  logo: `${COMPANY.MARKETING_URL}/ao-logo.svg`,
  sameAs: [COMPANY.GITHUB_URL],
};

const website = {
  "@type": "WebSite",
  "@id": `${COMPANY.DOCS_URL}/#website`,
  name: SITE_NAME,
  alternateName: ALTERNATE_NAMES.map((name) => `${name} Docs`),
  url: absoluteUrl("/"),
  publisher: { "@id": organization["@id"] },
};

type Article = {
  title: string;
  description?: string;
  slugs: string[];
  url: string;
  locale: SupportedLocale;
};

/**
 * The breadcrumb trail of an article, following the sidebar: the docs home,
 * then each folder that has its own page, then the article itself.
 */
function breadcrumbs({ title, url, locale }: Article) {
  const trail = [{ name: SITE_NAME, url: localePath("/", locale) }];
  for (const item of getBreadcrumbItems(url, docsTree(locale))) {
    if (item.url && typeof item.name === "string") trail.push({ name: item.name, url: withTrailingSlash(item.url) });
  }
  trail.push({ name: title, url: withTrailingSlash(url) });

  return {
    "@type": "BreadcrumbList",
    itemListElement: trail.map((item, index) => ({
      "@type": "ListItem",
      position: index + 1,
      name: item.name,
      item: absoluteUrl(item.url),
    })),
  };
}

/**
 * The JSON-LD of an article: a `TechArticle` with its breadcrumbs, or on the
 * docs home, with the organization and site instead.
 */
export function articleStructuredData(article: Article) {
  const url = absoluteUrl(withTrailingSlash(article.url));
  const isHome = article.slugs.length === 0;
  return {
    "@context": "https://schema.org",
    "@graph": [
      {
        "@type": "TechArticle",
        "@id": `${url}#article`,
        headline: article.title,
        description: article.description,
        url,
        inLanguage: article.locale,
        image: absoluteUrl(ogImagePath(article.slugs)),
        publisher: isHome ? { "@id": organization["@id"] } : organization,
        isPartOf: { "@id": website["@id"] },
      },
      ...(isHome ? [organization, website] : [breadcrumbs(article)]),
    ],
  };
}
