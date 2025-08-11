import type { NextApiRequest, NextApiResponse } from 'next';

export default function handler(_req: NextApiRequest, res: NextApiResponse) {
  // Never throws, never proxies.
  res.status(200).json({
    ok: true,
    CONTROLLER_ORIGIN: process.env.CONTROLLER_ORIGIN || null,
    GEO_ORIGIN: process.env.GEO_ORIGIN || null,
    node: process.version,
    pid: process.pid,
  });
}