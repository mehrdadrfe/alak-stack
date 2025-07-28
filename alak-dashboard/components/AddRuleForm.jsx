'use client'

import { useState } from 'react'

export default function AddRuleForm() {
  const [asn, setAsn] = useState('')
  const [country, setCountry] = useState('')
  const [dropPercent, setDropPercent] = useState('')
  const [message, setMessage] = useState(null)

  const handleSubmit = async (e) => {
    e.preventDefault()
    setMessage(null)

    const rule = { asn, country, drop_percent: parseInt(dropPercent, 10) }

    try {
      const api = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080'
      const res = await fetch(`${api}/rules`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(rule),
      })

      if (!res.ok) {
        const err = await res.text()
        throw new Error(err || 'Unknown error')
      }

      setMessage('✅ Rule added successfully')
      setAsn('')
      setCountry('')
      setDropPercent('')
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
        <input value={asn} onChange={(e) => setAsn(e.target.value)} required />
      </div>
      <div>
        <label>Country:</label>
        <input value={country} onChange={(e) => setCountry(e.target.value)} required />
      </div>
      <div>
        <label>Drop Percent:</label>
        <input
          type="number"
          value={dropPercent}
          onChange={(e) => setDropPercent(e.target.value)}
          required
          min={0}
          max={100}
        />
      </div>
      <button type="submit">Submit</button>
      {message && <p>{message}</p>}
    </form>
  )
}
