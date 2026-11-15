import { articleRoute } from "@/components/article-route";

const route = articleRoute("ja");

export const dynamicParams = false;

export default route.Page;
export const generateStaticParams = route.generateStaticParams;
export const generateMetadata = route.generateMetadata;
