import { createMDX } from "fumadocs-mdx/next";

const withMDX = createMDX();

/** @type {import("next").NextConfig} */
const config = {
  output: "export",
  reactStrictMode: true,
  trailingSlash: true,
  experimental: {
    // Compiles the Lingui macros. Keep the plugin version pinned to one
    // that matches Next's swc_core.
    swcPlugins: [["@lingui/swc-plugin", {}]],
    // The root layout sits inside the docs route so it can set <html lang>,
    // so unmatched URLs need a 404 page outside it.
    globalNotFound: true,
  },
  turbopack: {
    root: import.meta.dirname,
  },
  images: {
    unoptimized: true,
  },
};

export default withMDX(config);
