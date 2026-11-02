import { setupI18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";
import { createRelativeLink } from "fumadocs-ui/mdx";
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import type { Metadata } from "next";
import { notFound } from "next/navigation";
import type { ComponentProps } from "react";
import { CATALOGS } from "@/i18n/catalogs";
import { DEFAULT_LOCALE, type SupportedLocale } from "@/i18n/locales";
import { localePath } from "@/i18n/paths";
import { visibleProductName } from "@/lib/branding";
import { COMPANY } from "@/lib/constants";
import { markdownPath, sourceUrl } from "@/lib/llms";
import { articleStructuredData, OG_IMAGE_SIZE, OG_LOCALES, ogImagePath } from "@/lib/seo";
import { source } from "@/lib/source";
import { articleLocales, articlePath, localizeHref } from "@/lib/translations";
import { Card, getMDXComponents } from "./mdx";
import { PageActions } from "./page-actions";
import { EnglishArticleNotice, LocalizedTitle } from "./page-locale";

type Props = { params: Promise<{ slug?: string[] }> };

// Escapes `<` so the JSON cannot close the script element.
function JsonLd({ data }: { data: object }) {
  return <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(data).replace(/</g, "\\u003c") }} />;
}

function getArticle(slug: string[] | undefined, locale: SupportedLocale) {
  const page = source.getPage(slug ?? [], locale);
  if (!page) notFound();
  return page;
}

/**
 * The article pages for one language. English covers every article; any
 * other language covers only the articles translated into it, under
 * `/<locale>/`.
 */
export function articleRoute(locale: SupportedLocale) {
  async function Page(props: Props) {
    const { slug } = await props.params;
    const page = getArticle(slug, locale);
    const MDX = page.data.body;
    const RelativeLink = createRelativeLink(source, page);

    return (
      <DocsPage toc={page.data.toc} full={page.data.full}>
        <LocalizedTitle source={page.data.title} />
        <DocsTitle lang={locale}>{page.data.title}</DocsTitle>
        <DocsDescription lang={locale}>{page.data.description && visibleProductName(page.data.description)}</DocsDescription>
        {locale === DEFAULT_LOCALE && <EnglishArticleNotice />}
        {/* The Markdown copy is English, also for translated articles. */}
        <PageActions markdownPath={markdownPath(page.slugs)} sourceUrl={sourceUrl(page)} />
        <DocsBody lang={locale}>
          <MDX
            components={getMDXComponents({
              a: ({ href, ...rest }: ComponentProps<"a">) => <RelativeLink href={localizeHref(href, locale)} {...rest} />,
              Card: ({ href, ...rest }: ComponentProps<typeof Card>) => <Card href={localizeHref(href, locale)} {...rest} />,
            })}
          />
        </DocsBody>
        <JsonLd data={articleStructuredData({ title: page.data.title, description: page.data.description, slugs: page.slugs, url: page.url, locale, path: page.path })} />
      </DocsPage>
    );
  }

  async function generateStaticParams() {
    return source.getPages(locale).map((page) => ({ slug: page.slugs }));
  }

  async function generateMetadata(props: Props): Promise<Metadata> {
    const { slug } = await props.params;
    const page = getArticle(slug, locale);
    const englishPath = articlePath(page.slugs);
    const locales = articleLocales(page.slugs);
    const i18n = setupI18n({ locale, messages: { [locale]: CATALOGS[locale] } });
    const title = page.data.title;
    const name = COMPANY.NAME;
    const url = localePath(englishPath, locale);
    const preview = {
      title,
      description: page.data.description,
      images: [{ url: ogImagePath(page.slugs), ...OG_IMAGE_SIZE, alt: title }],
    };

    return {
      title: i18n._(msg`${title} | Orchestrator.inc Docs`),
      description: page.data.description,
      openGraph: {
        type: "article",
        siteName: i18n._(msg`${name} Docs`),
        url,
        locale: OG_LOCALES[locale],
        alternateLocale: locales.filter((language) => language !== locale).map((language) => OG_LOCALES[language]),
        ...preview,
      },
      twitter: { card: "summary_large_image", ...preview },
      alternates: {
        canonical: url,
        // Only articles that exist in more than one language have alternates.
        languages:
          locales.length > 1
            ? {
                ...Object.fromEntries(locales.map((language) => [language, localePath(englishPath, language)])),
                "x-default": englishPath,
              }
            : undefined,
      },
    };
  }

  return { Page, generateStaticParams, generateMetadata };
}
