import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

const config = {
  output: "export",
  reactStrictMode: true,
  trailingSlash: true,
  experimental: {
    globalNotFound: true,
    swcPlugins: [["@lingui/swc-plugin", {}]],
  },
  turbopack: {
    root: import.meta.dirname,
  },
  images: {
    unoptimized: true,
  },
};

export default withMDX(config);
