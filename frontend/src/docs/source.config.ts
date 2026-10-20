import remarkGfm from "remark-gfm";
import { defineDocs, defineConfig } from "fumadocs-mdx/config";

export const docs = defineDocs({
  dir: "content",
});

export default defineConfig({
  mdxOptions: {
    remarkPlugins: [remarkGfm],
    remarkStructureOptions: {
      stringify: {
        stringify(node) {
          if (node.type !== "mdxJsxFlowElement" || node.name !== "Card") return;

          const text: string[] = [];
          for (const attribute of node.attributes) {
            if (attribute.type !== "mdxJsxAttribute") continue;
            if (attribute.name !== "title" && attribute.name !== "description") continue;
            if (typeof attribute.value === "string") text.push(attribute.value);
          }
          return text.length > 0 ? text.join(" ") : undefined;
        },
      },
    },
  },
});
