/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  async redirects() {
    // Task boards are scoped to a project; route from review nav to selection.
    return [{ source: "/tasks", destination: "/projects", permanent: false }];
  },
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080"}/api/:path*`,
      },
    ];
  },
};

module.exports = nextConfig;
