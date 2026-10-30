import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

/** @type {import("next").NextConfig} */
const config = {
  output: "export",
  reactStrictMode: true,
  trailingSlash: true,
  experimental: {
    // Each language has its own root layout, so the 404 page renders its
    // own <html> instead of borrowing one.
    globalNotFound: true,
    // Compiles the Lingui macros. Keep the plugin version pinned to one
    // that matches Next's swc_core.
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
