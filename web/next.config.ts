import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  poweredByHeader: false,
  distDir: process.env.WHIRLWIND_NEXT_DIST_DIR || ".next",
  turbopack: { root: process.cwd() },
};

export default nextConfig;
