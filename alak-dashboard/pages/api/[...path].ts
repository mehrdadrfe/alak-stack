import type { NextApiRequest, NextApiResponse } from 'next'

export const config = { api: { bodyParser: false } }

function sanitizeHeaders(req: NextApiRequest): Headers {
  const h = new Headers()
  for (const [k, v] of Object.entries(req.headers)) {
    if (v == null) continue
    const kl = k.toLowerCase()
    if (['host', 'connection', 'transfer-encoding', 'content-length'].includes(kl)) continue
    if (Array.isArray(v)) h.set(k, v.join(', '))
    else h.set(k, String(v))
  }
  return h
}

async function readRawBody(req: NextApiRequest): Promise<Buffer | undefined> {
  if (req.method === 'GET' || req.method === 'HEAD') return undefined
  const chunks: Buffer[] = []
  for await (const chunk of req) {
    chunks.push(typeof chunk === 'string' ? Buffer.from(chunk) : chunk)
  }
  return Buffer.concat(chunks)
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const segs = ([] as string[]).concat((req.query.path as any) ?? [])

  // ---- local endpoints (never proxy) ----
  if (segs.length === 1 && segs[0] === 'health') {
    res.status(200).json({ ok: true })
    return
  }
  if (segs.length === 1 && segs[0] === '_diag') {
    // Read env at runtime (pod’s env), no throws
    const controller = (process.env.CONTROLLER_ORIGIN || '').replace(/\/+$/, '') || null
    res.status(200).json({
      ok: true,
      CONTROLLER_ORIGIN: controller,
      note: 'This is the controller proxy. Real API routes are under /api/*.',
      node: process.version,
      pid: process.pid,
    })
    return
  }
  if (req.method === 'OPTIONS') {
    res.status(204).end()
    return
  }

  // ---- proxy to controller (runtime env read) ----
  try {
    let origin = process.env.CONTROLLER_ORIGIN || ''
    origin = origin.replace(/\/+$/, '') // strip trailing slashes
    if (!origin) throw new Error('CONTROLLER_ORIGIN is not set')

    const search = req.url && req.url.includes('?') ? '?' + req.url.split('?')[1] : ''
    const targetURL = `${origin}/${segs.join('/')}${search}`

    const headers = sanitizeHeaders(req)
    const body = await readRawBody(req)

    const upstream = await fetch(targetURL, {
      method: req.method,
      headers,
      body,
      redirect: 'manual',
    })

    res.setHeader('x-proxy-target', targetURL)

    upstream.headers.forEach((val, key) => {
      const kl = key.toLowerCase()
      if (['connection', 'transfer-encoding'].includes(kl)) return
      res.setHeader(key, val)
    })

    res.status(upstream.status)
    const ab = await upstream.arrayBuffer()
    res.send(Buffer.from(ab))
  } catch (err: any) {
    const msg = err?.message || 'proxy error'
    res.setHeader('x-proxy-error', msg)
    res.status(500).json({ ok: false, error: msg })
  }
}
