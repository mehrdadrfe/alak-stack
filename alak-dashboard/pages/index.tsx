// pages/index.tsx
import { useState, useMemo } from 'react'
import useSWR from 'swr'
import Head from 'next/head'

type Rule = {
  asn: string
  country: string
  tsp?: string
  drop_percent: number
  ttl?: number
  enabled?: boolean
}

const fetcher = async (url: string) => {
  const res = await fetch(url)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

/**
 * Compute endpoint bases.
 * - Default: use same-origin Next API routes (proxy) → '/api' and '/api/geo'
 * - Dev-only bypass: if NEXT_PUBLIC_API_BYPASS=1, hit services directly via NEXT_PUBLIC_API_URL / NEXT_PUBLIC_GEO_API_URL
 */
function useApiBases() {
  const isDev = process.env.NODE_ENV === 'development'
  const bypass = isDev && process.env.NEXT_PUBLIC_API_BYPASS === '1'

  const directApi = (process.env.NEXT_PUBLIC_API_URL || '').replace(/\/+$/, '')
  const directGeo = (process.env.NEXT_PUBLIC_GEO_API_URL || '').replace(/\/+$/, '')

  const apiBase = bypass && directApi ? directApi : ''        // '' means same-origin '/api'
  const geoBase = bypass && directGeo ? directGeo : ''        // '' means same-origin '/api/geo'

  return { apiBase, geoBase }
}

export default function Home() {
  const { apiBase, geoBase } = useApiBases()

  // helpers to build URLs (proxy by default)
  const apiUrl = (p: string) => (apiBase ? `${apiBase}${p}` : `/api${p}`)
  const geoUrl = (p: string) => (geoBase ? `${geoBase}${p}` : `/api/geo${p}`)

  const { data: rulesData, error: rulesError, mutate: reloadRules } = useSWR<Rule[]>(
    apiUrl('/rules'),
    fetcher,
    { refreshInterval: 5000 }
  )
  const rules: Rule[] = Array.isArray(rulesData) ? rulesData : []

  const [searchValue, setSearchValue] = useState('')
  const [asn, setAsn] = useState('')
  const [tsp, setTsp] = useState('')
  const [country, setCountry] = useState('')
  const [dropPercent, setDropPercent] = useState<number>(0)
  const [ttl, setTtl] = useState<number>(0)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<boolean>(false)
  const [loading, setLoading] = useState<boolean>(false)
  const [searchLoading, setSearchLoading] = useState<boolean>(false)
  const [tspMatches, setTspMatches] = useState<any[]>([])

  // Unified Search (ASN or TSP)
  const handleSearch = async () => {
    if (!searchValue.trim()) return
    setSearchLoading(true)
    setTspMatches([])
    try {
      const input = searchValue.trim()
      const isASN = /^(AS)?\d+$/i.test(input)
      const query = isASN
        ? `asn=${encodeURIComponent(input.toUpperCase().startsWith('AS') ? input.toUpperCase() : `AS${input}`)}`
        : `tsp=${encodeURIComponent(input.toLowerCase())}`

      const res = await fetch(geoUrl(`/lookup?${query}`))
      if (res.status === 300) {
        const data = await res.json()
        setTspMatches(data)
        setError(null)
      } else if (res.ok) {
        const data = await res.json()
        setAsn(data.asn || '')
        setTsp(data.tsp || '')
        setCountry(data.country || '')
        setError(null)
      } else {
        throw new Error('Not found')
      }
    } catch (err) {
      console.error('Search failed:', err)
      setError('No match found for this input')
    } finally {
      setSearchLoading(false)
    }
  }

  const handleSelectTSP = (match: any) => {
    setAsn(match.asn)
    setTsp(match.tsp)
    setCountry(match.country || '')
    setTspMatches([])
  }

  // Submit Rule
  const submitRule = async () => {
    setError(null)
    setSuccess(false)
    try {
      setLoading(true)
      const res = await fetch(apiUrl('/rules'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ asn, country, tsp, drop_percent: dropPercent, ttl, enabled: true }),
      })
      if (!res.ok) throw new Error(await res.text())
      setSuccess(true)
      setAsn(''); setCountry(''); setTsp(''); setDropPercent(0); setTtl(0); setSearchValue('')
      reloadRules()
    } catch (err) {
      console.error('Rule submission failed:', err)
      setError('Failed to submit rule')
    } finally {
      setLoading(false)
    }
  }

  const deleteRule = async (rule: Rule) => {
    if (!confirm(`Delete rule ASN:${rule.asn}, Country:${rule.country}?`)) return
    const query = new URLSearchParams({ asn: rule.asn, country: rule.country, tsp: rule.tsp || '' })
    await fetch(apiUrl(`/rules?${query.toString()}`), { method: 'DELETE' })
    reloadRules()
  }

  const toggleRuleEnabled = async (rule: Rule) => {
    await fetch(apiUrl('/toggle-rule'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        asn: rule.asn,
        country: rule.country,
        tsp: rule.tsp || '',
        enabled: !(rule.enabled ?? false),
      }),
    })
    reloadRules()
  }

  return (
    <>
      <Head><title>Alak Dashboard – ASN/TSP Rules</title></Head>
      <main style={{ padding: '2rem', fontFamily: 'system-ui, sans-serif', maxWidth: 800, margin: '0 auto' }}>
        <h1>🎛️ Alak Load Shedding Rules</h1>

        {/* Single Search */}
        <section style={{ border: '1px solid #ccc', padding: '1rem', marginBottom: '1rem', borderRadius: 6 }}>
          <label>
            Enter ASN or TSP:
            <input
              value={searchValue}
              onChange={(e) => setSearchValue(e.target.value)}
              placeholder="e.g. AS44244 or irancell"
              style={{ marginLeft: 8, width: 250 }}
            />
          </label>
          <button style={{ marginLeft: 8 }} onClick={handleSearch}>🔍 Search</button>
          {searchLoading && <span style={{ marginLeft: 10 }}>⏳ Searching...</span>}
        </section>

        {/* Multiple TSP Matches Dropdown */}
        {tspMatches.length > 0 && (
          <div style={{ border: '1px solid #aaa', padding: '0.5rem', marginBottom: '1rem' }}>
            <p>Multiple matches found, select one:</p>
            <ul style={{ listStyle: 'none', padding: 0 }}>
              {tspMatches.map((match, i) => (
                <li key={i} style={{ cursor: 'pointer', padding: '4px 0' }} onClick={() => handleSelectTSP(match)}>
                  {match.tsp} ({match.asn})
                </li>
              ))}
            </ul>
          </div>
        )}

        {/* Auto-filled fields */}
        <section style={{ border: '1px solid #eee', padding: '1rem', marginBottom: '1rem' }}>
          <label>ASN:<input value={asn} onChange={(e) => setAsn(e.target.value)} /></label><br /><br />
          <label>TSP:<input value={tsp} onChange={(e) => setTsp(e.target.value)} /></label><br /><br />
          <label>Country:<input value={country} onChange={(e) => setCountry(e.target.value)} /></label>
        </section>

        {/* Add Rule */}
        <form onSubmit={(e) => { e.preventDefault(); submitRule() }} style={{ display: 'grid', gap: '0.75rem', marginBottom: '1rem' }}>
          <label>Drop %:<input type="number" value={dropPercent} onChange={(e) => setDropPercent(Number(e.target.value))} /></label>
          <label>TTL (s):<input type="number" value={ttl} onChange={(e) => setTtl(Number(e.target.value))} /></label>
          <button type="submit" disabled={loading}>{loading ? 'Adding...' : '➕ Add Rule'}</button>
        </form>

        {success && <p style={{ color: 'green' }}>✅ Rule added successfully</p>}
        {error && <p style={{ color: 'red' }}>❌ {error}</p>}

        {/* Rules Table */}
        <h2>📋 Active Rules</h2>
        {rulesError ? (
          <p style={{ color: 'red' }}>Failed to load rules: {rulesError.message}</p>
        ) : rules.length === 0 ? (
          <p style={{ color: '#888' }}>No rules configured yet.</p>
        ) : (
          <table border={1} cellPadding={8} style={{ width: '100%', borderCollapse: 'collapse' }}>
            <thead>
              <tr>
                <th>ASN</th>
                <th>Country</th>
                <th>TSP</th>
                <th>Drop %</th>
                <th>TTL (s)</th>
                <th>Enabled</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule, i) => (
                <tr key={i}>
                  <td>{rule.asn}</td>
                  <td>{rule.country}</td>
                  <td>{rule.tsp || '-'}</td>
                  <td>{rule.drop_percent}</td>
                  <td>{rule.ttl !== undefined && rule.ttl >= 0 ? rule.ttl : '-'}</td>
                  <td>
                    <input
                      type="checkbox"
                      checked={rule.enabled !== false}
                      onChange={() => toggleRuleEnabled(rule)}
                    />
                  </td>
                  <td><button onClick={() => deleteRule(rule)}>🗑 Delete</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </main>
    </>
  )
}
