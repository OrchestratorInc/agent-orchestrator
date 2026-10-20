import { notFound } from "next/navigation";
import type { Metadata } from "next";
import type { ComponentProps } from "react";
import { msg } from "@lingui/core/macro";
import { createRelativeLink } from "fumadocs-ui/mdx";
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import { Card, getMDXComponents, PluginCard } from "@/components/mdx";
import { source } from "@/lib/source";
import { visibleProductName } from "@/lib/branding";
import { DEFAULT_LOCALE, localizePath, SUPPORTED_LOCALES, type SupportedLocale } from "@/i18n/locales";
import { joinSlug, localizedAlternates, splitSlug } from "@/i18n/routing";
import { initServerI18n } from "@/i18n/server";

/** Locale and page for the catch-all params. */
async function resolvePage(props: PageProps<"/[[...slug]]">) {
  const { locale, slug } = splitSlug((await props.params).slug);
  const page = source.getPage(slug, locale);
  if (!page) notFound();
  return { locale, slug, page };
}

/** True when the locale has its own copy of the article, not the English fallback. */
function hasTranslatedBody(locale: SupportedLocale, slug: string[]): boolean {
  if (locale === DEFAULT_LOCALE) return true;
  return source.getPage(slug, locale)?.data !== source.getPage(slug, DEFAULT_LOCALE)?.data;
}

/** Keeps same-site links in the reader's locale. Assets and other hosts pass through. */
function localizeHref(locale: SupportedLocale, href: string | undefined): string | undefined {
  if (!href || !href.startsWith("/") || href.startsWith("//") || href.startsWith("/docs/")) return href;
  return localizePath(locale, href);
}

export default async function Page(props: PageProps<"/[[...slug]]">) {
  const { locale, slug, page } = await resolvePage(props);
  const i18n = await initServerI18n(locale);
  const translated = hasTranslatedBody(locale, slug);
  // Marks an English article inside a translated interface.
  const bodyLang = translated ? undefined : DEFAULT_LOCALE;
  const RelativeLink = createRelativeLink(source, page);

  const MDX = page.data.body;

  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
      <DocsTitle lang={bodyLang}>{page.data.title}</DocsTitle>
      <DocsDescription lang={bodyLang}>
        {page.data.description && visibleProductName(page.data.description)}
      </DocsDescription>
      {!translated && (
        <p className="-mt-2 text-sm text-fd-muted-foreground">
          {i18n._(msg`This page is not translated yet, so it is shown in English.`)}
        </p>
      )}
      <DocsBody lang={bodyLang}>
        <MDX
          components={getMDXComponents({
            a: (linkProps: ComponentProps<"a">) => (
              <RelativeLink {...linkProps} href={localizeHref(locale, linkProps.href)} />
            ),
            Card: (cardProps: ComponentProps<typeof Card>) => (
              <Card {...cardProps} href={localizeHref(locale, cardProps.href)} />
            ),
            PluginCard: (cardProps: ComponentProps<typeof PluginCard>) => (
              <PluginCard {...cardProps} href={localizeHref(locale, cardProps.href) ?? cardProps.href} />
            ),
          })}
        />
      </DocsBody>
    </DocsPage>
  );
}

export async function generateStaticParams() {
  return SUPPORTED_LOCALES.flatMap((locale) =>
    source.getPages(locale).map((page) => ({ slug: joinSlug(locale, page.slugs) })),
  );
}

export async function generateMetadata(props: PageProps<"/[[...slug]]">): Promise<Metadata> {
  const { locale, slug, page } = await resolvePage(props);
  const i18n = await initServerI18n(locale);
  const title = page.data.title;

  return {
    title: i18n._(msg`${title} | Agent Orchestrator Docs`),
    description: page.data.description,
    alternates: localizedAlternates(locale, `/${slug.join("/")}`),
  };
}
