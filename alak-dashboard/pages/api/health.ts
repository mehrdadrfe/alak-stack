import type { NextApiRequest, NextApiResponse } from 'next';

export default function handler(_req: NextApiRequest, res: NextApiResponse) {
  // Keep this stupid-simple: never throw, never proxy.
  res.status(200).json({ ok: true });
}
