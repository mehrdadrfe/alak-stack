import { useEffect, useState } from 'react'

export default function RuleList() {
  const [rules, setRules] = useState([])
  const api = process.env.NEXT_PUBLIC_API_URL

  useEffect(() => {
    fetch(`${api}/rules`)
      .then(res => res.json())
      .then(setRules)
  }, [])

  return (
    <div>
      <h2>Existing Rules</h2>
      <ul>
        {rules.map((r, i) => (
          <li key={i}>
            <code>{r.asn} - {r.country} → Drop {r.drop_percent}%</code>
          </li>
        ))}
      </ul>
    </div>
  )
}
