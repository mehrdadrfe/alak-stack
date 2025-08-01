'use client'

import { useState, useEffect } from 'react'

async function loadMapping(setMapping) {
  try {
    const res = await fetch('/asn-country-map.json')
    setMapping(res.ok ? await res.json() : {})
  } catch {
    setMapping({})
  }
}

function getCountry(asnValue, tspValue, mappingObj) {
  let detected = ''
  if (asnValue) detected = mappingObj[asnValue.toUpperCase()] || ''
  if (!detected && tspValue) {
    const found = Object.entries(mappingObj).find(
      ([key, value]) => key.toLowerCase() === tspValue.toLowerCase()
    )
    detected = found ? found[1] : ''
  }
  return detected
}

export default function AddRuleForm() {
  const [asn, setAsn] = useState('')
  const [tsp, setTsp] = useState('')
  const [country, setCountry] = useState('')
  const [dropPercent, setDropPercent] = useState('')
  const [message, setMessage] = useState(null)
  const [mapping, setMapping] = useState({})

  useEffect(() => { loadMapping(setMapping) }, [])

  // User types: keep auto-fill
  const handleAsnChange = (e) => {
    const nextAsn = e.target.value
    setAsn(nextAsn)
    setCountry(getCountry(nextAsn, tsp, mapping))
  }
  const handleTspChange = (e) => {
    const nextTsp = e.target.value
    setTsp(nextTsp)
    setCountry(getCountry(asn, nextTsp, mapping))
  }

  // EXAMPLE: Async "search" for ASN/TSP from API
  // Replace this with your real backend query
  const handleAsyncSearch = async () => {
    // Simulate a network request with a Promise
    const found = await new Promise(resolve =>
      setTimeout(() => resolve({
        asn: 'AS44244',
        tsp: 'iran cell service and communication company'
      }), 400)
    )
    // Use results directly for country lookup
    const foundCountry = getCountry(found.asn, found.tsp, mapping)
    setAsn(found.asn)
    setTsp(found.tsp)
    setCountry(foundCountry)
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setMessage(null)
    const rule = { asn, tsp, country, drop_percent: parseInt(dropPercent, 10) }
    try {
      const api = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
      const res = await fetch(`${api}/rules`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(rule),
      })
      if (!res.ok) throw new Error(await res.text() || 'Unknown error')
      setMessage('✅ Rule added successfully')
      setAsn(''); setTsp(''); setCountry(''); setDropPercent('')
    } catch (err) {
      console.error(err)
      setMessage('❌ Error adding rule')
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ maxWidth: 400 }}>
      <h2>Add Load Shedding Rule</h2>
      <div>
        <label>ASN:</label>
        <input value={asn} onChange={handleAsnChange} />
      </div>
      <div>
        <label>TSP:</label>
        <input value={tsp} onChange={handleTspChange} />
      </div>
      <div>
        <label>Country:</label>
        <input value={country} onChange={e => setCountry(e.target.value)} />
      </div>
      <div>
        <label>Drop Percent:</label>
        <input
          type="number"
          value={dropPercent}
          onChange={e => setDropPercent(e.target.value)}
          min={0}
          max={100}
        />
      </div>
      <button type="submit">Submit</button>
      <button type="button" onClick={handleAsyncSearch} style={{ marginLeft: 10 }}>
        Async Search Example
      </button>
      {message && <p>{message}</p>}
    </form>
  )
}
