'use client'

import { useEffect, useMemo, useState } from 'react'

const debounce = (fn, ms = 400) => {
  let t
  return (...args) => {
    clearTimeout(t)
    t = setTimeout(() => fn(...args), ms)
  }
}

export default function AddRuleForm() {
  const [asn, setAsn] = useState('')
  const [tsp, setTsp] = useState('')
  const [country, setCountry] = useState('')
  const [city, setCity] = useState('')
  const [dropPercent, setDropPercent] = useState('')
  const [enabled, setEnabled] = useState(true)

  const [message, setMessage] = useState(null)
  const [busy, setBusy] = useState(false)

  const [tspList, setTspList] = useState([])
  const [suggestions, setSuggestions] = useState([])
  const hasSuggestions = suggestions && suggestions.length > 1

  const normASN = useMemo(() => asn.trim().toUpperCase(), [asn])
  const normTSP = useMemo(() => tsp.trim().toLowerCase(), [tsp])

  const clearMsgSoon = () => setTimeout(() => setMessage(null), 2500)

  async function safeJSON(res) {
    const text = await res.text()
    try { return JSON.parse(text) } catch { return text || null }
  }

  async function lookupByASN(asnValue) {
    if (!asnValue) return null
    const res = await fetch(`/api/geo/lookup?asn=${encodeURIComponent(asnValue)}`)
    if (res.status === 300) return safeJSON(res)
    if (!res.ok) throw new Error(await res.text())
    return res.json()
  }

  async function lookupByTSP(tspValue) {
    if (!tspValue) return null
    const res = await fetch(`/api/geo/lookup?tsp=${encodeURIComponent(tspValue)}`)
    if (res.status === 300) return safeJSON(res)
    if (!res.ok) throw new Error(await res.text())
    return res.json()
  }

  function applyLookup(obj) {
    if (!obj) return
    if (obj.asn) setAsn(obj.asn)
    if (obj.tsp) setTsp(obj.tsp)
    if (obj.country) setCountry(obj.country)
    if (obj.city) setCity(obj.city || '')
  }

  useEffect(() => {
    let alive = true
    fetch('/api/geo/tsp-list')
      .then(r => (r.ok ? r.json() : []))
      .then(list => { if (alive) setTspList(Array.isArray(list) ? list : []) })
      .catch(() => {})
    return () => { alive = false }
  }, [])

  useEffect(() => {
    const run = debounce(async (val) => {
      if (!val) return
      try {
        const res = await lookupByASN(val)
        if (Array.isArray(res)) setSuggestions(res)
        else { setSuggestions([]); applyLookup(res) }
      } catch {}
    })
    if (normASN && normASN.length >= 3) run(normASN)
  }, [normASN])

  useEffect(() => {
    const run = debounce(async (val) => {
      if (!val) return
      try {
        const res = await lookupByTSP(val)
        if (Array.isArray(res)) setSuggestions(res)
        else { setSuggestions([]); applyLookup(res) }
      } catch {}
    })
    if (normTSP && normTSP.length >= 3) run(normTSP)
  }, [normTSP])

  const handlePickSuggestion = (s) => {
    applyLookup(s)
    setSuggestions([])
    setMessage('✅ Filled from suggestions')
    clearMsgSoon()
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setMessage(null)

    const dp = Number(dropPercent)
    if (!normASN || !normTSP || !country.trim()) {
      setMessage('❌ ASN, TSP, and Country are required')
      return
    }
    if (!Number.isFinite(dp) || dp < 0 || dp > 100) {
      setMessage('❌ Drop% must be a number between 0–100')
      return
    }

    const payload = {
      asn: normASN,
      tsp: normTSP,
      country: country.trim().toUpperCase(),
      drop_percent: dp,
      enabled
    }

    try {
      setBusy(true)
      const res = await fetch('/api/rules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (!res.ok) throw new Error(await safeJSON(res) || 'Unknown error')
      setMessage('✅ Rule added successfully')
      setAsn(''); setTsp(''); setCountry(''); setCity(''); setDropPercent('')
      setEnabled(true)
      clearMsgSoon()
    } catch (err) {
      setMessage('❌ Error adding rule')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ maxWidth: 520 }}>
      <h2 style={{ marginBottom: 10 }}>Add Load Shedding Rule</h2>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <label style={{ display: 'grid', gap: 6 }}>
          <span>ASN</span>
          <input
            value={asn}
            onChange={e => setAsn(e.target.value)}
            placeholder="e.g., AS44244"
            autoCapitalize="characters"
          />
        </label>

        <label style={{ display: 'grid', gap: 6 }}>
          <span>TSP (ISP)</span>
          <input
            list="tspList"
            value={tsp}
            onChange={e => setTsp(e.target.value)}
            placeholder="e.g., irancell"
          />
          <datalist id="tspList">
            {tspList.map((v, i) => <option key={i} value={v} />)}
          </datalist>
        </label>

        <label style={{ display: 'grid', gap: 6 }}>
          <span>Country</span>
          <input
            value={country}
            onChange={e => setCountry(e.target.value)}
            placeholder="e.g., IR"
          />
        </label>

        <label style={{ display: 'grid', gap: 6 }}>
          <span>City (from Geo)</span>
          <input value={city} readOnly placeholder="auto (optional)" />
        </label>

        <label style={{ display: 'grid', gap: 6 }}>
          <span>Drop Percent</span>
          <input
            type="number"
            value={dropPercent}
            onChange={e => setDropPercent(e.target.value)}
            min={0}
            max={100}
            placeholder="0–100"
          />
        </label>

        <label style={{ display: 'grid', gap: 6, alignItems: 'center' }}>
          <span>Enabled</span>
          <input
            type="checkbox"
            checked={enabled}
            onChange={e => setEnabled(e.target.checked)}
          />
        </label>
      </div>

      {hasSuggestions && (
        <div style={{
          marginTop: 12, padding: 10, border: '1px solid #ddd',
          borderRadius: 8, background: '#fafafa'
        }}>
          <div style={{ marginBottom: 6, fontWeight: 600 }}>
            Multiple matches — pick one:
          </div>
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            {suggestions.map((s, i) => (
              <li key={i} style={{ margin: '4px 0' }}>
                <button
                  type="button"
                  onClick={() => handlePickSuggestion(s)}
                  style={{
                    border: '1px solid #ccc',
                    borderRadius: 6,
                    padding: '2px 8px',
                    cursor: 'pointer',
                    background: '#fff'
                  }}
                  title="Apply this match"
                >
                  {s.asn || '(asn?)'} · {s.country || '(country?)'} · {s.tsp || '(tsp?)'}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div style={{ marginTop: 14 }}>
        <button
          type="submit"
          disabled={busy}
          style={{
            padding: '6px 14px',
            borderRadius: 8,
            border: '1px solid #ccc',
            cursor: busy ? 'wait' : 'pointer',
            background: busy ? '#eee' : '#e3f2fd'
          }}
        >
          {busy ? 'Saving…' : 'Submit'}
        </button>
        {message && <span style={{ marginLeft: 12 }}>{message}</span>}
      </div>
    </form>
  )
}
