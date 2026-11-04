import { articleRoute } from "@/components/article-route";
import { DEFAULT_LOCALE } from "@/i18n/locales";

const route = articleRoute(DEFAULT_LOCALE);

export default route.Page;
export const generateStaticParams = route.generateStaticParams;
export const generateMetadata = route.generateMetadata;
