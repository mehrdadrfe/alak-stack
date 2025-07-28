/** @type {import('next').NextConfig} */
const nextConfig = {
  env: {
    ALAK_API_URL: process.env.ALAK_API_URL || 'http://alak-api:8080',
  },
};

module.exports = nextConfig;
