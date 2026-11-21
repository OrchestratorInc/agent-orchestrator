import { articleRoute } from "@/components/article-route";

const route = articleRoute("ja");

// Only translated articles exist here; anything else is not found.
export const dynamicParams = false;

export default route.Page;
export const generateStaticParams = route.generateStaticParams;
export const generateMetadata = route.generateMetadata;
