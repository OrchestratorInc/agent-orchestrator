import { notFound } from "next/navigation";
import type { Metadata } from "next";
import { createRelativeLink } from "fumadocs-ui/mdx";
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import { getMDXComponents } from "@/components/mdx";
import { EnglishArticleNotice, LocalizedTitle } from "@/components/page-locale";
import { source } from "@/lib/source";
import { visibleProductName } from "@/lib/branding";

export default async function Page(props: PageProps<"/[[...slug]]">) {
  const params = await props.params;
  const page = source.getPage(params.slug);
  if (!page) notFound();

  const MDX = page.data.body;

  // The articles are English whatever the interface language is.
  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
      <LocalizedTitle source={page.data.title} />
      <DocsTitle lang="en">{page.data.title}</DocsTitle>
      <DocsDescription lang="en">{page.data.description && visibleProductName(page.data.description)}</DocsDescription>
      <EnglishArticleNotice />
      <DocsBody lang="en">
        <MDX
          components={getMDXComponents({
            a: createRelativeLink(source, page),
          })}
        />
      </DocsBody>
    </DocsPage>
  );
}

export async function generateStaticParams() {
  return source.generateParams();
}

export async function generateMetadata(props: PageProps<"/[[...slug]]">): Promise<Metadata> {
  const params = await props.params;
  const page = source.getPage(params.slug);
  if (!page) notFound();

  return {
    title: `${page.data.title} | Agent Orchestrator Docs`,
    description: page.data.description,
    alternates: { canonical: `/${params.slug?.length ? `${params.slug.join("/")}/` : ""}` },
  };
}
