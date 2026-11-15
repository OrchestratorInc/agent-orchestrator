import { getBreadcrumbItems } from "fumadocs-core/breadcrumb";
import type { SupportedLocale } from "@/i18n/locales";
import { localePath, withTrailingSlash } from "@/i18n/paths";
import { COMPANY } from "./constants";
import { commitDates } from "./last-modified";
import { docsTree } from "./docs-tree";

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

export function ogImagePath(slugs: string[]): string {
  return `/og/${[...slugs, "image.png"].join("/")}`;
}

export function absoluteUrl(path: string): string {
  return `${COMPANY.DOCS_URL}${path}`;
}

export const SITE_NAME = `${COMPANY.NAME} Docs`;

const organization = { "@id": `${COMPANY.MARKETING_URL}/#organization` };

const website = {
  "@type": "WebSite",
  "@id": `${COMPANY.DOCS_URL}/#website`,
  name: SITE_NAME,
  alternateName: ["Agent Orchestrator Docs", "AO Docs"],
  url: absoluteUrl("/"),
  publisher: organization,
};

type Article = {
  title: string;
  description?: string;
  slugs: string[];
  url: string;
  locale: SupportedLocale;
  path: string;
};

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

export function articleStructuredData(article: Article) {
  const url = absoluteUrl(withTrailingSlash(article.url));
  const isHome = article.slugs.length === 0;
  const { published, modified } = commitDates(article.path);
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
        datePublished: published.toISOString(),
        dateModified: modified.toISOString(),
        author: organization,
        publisher: organization,
        isPartOf: { "@id": website["@id"] },
      },
      website,
      ...(isHome ? [] : [breadcrumbs(article)]),
    ],
  };
}
