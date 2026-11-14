import { COMPANY } from "../../lib/constants";

export const dynamic = "force-static";

const body = `User-agent: *
Allow: /
Disallow: /api/
Content-Signal: search=yes, ai-input=yes, ai-train=yes

Sitemap: ${COMPANY.DOCS_URL}/sitemap.xml
`;

export function GET() {
  return new Response(body, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
}
