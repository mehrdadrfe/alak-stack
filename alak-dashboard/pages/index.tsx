import { useEffect, useState } from 'react';
import Head from 'next/head';

type Rule = {
  asn: string;
  country: string;
  tsp?: string;
  city?: string;
  drop_percent: number;
  ttl?: number;
};

export default function Home() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [asn, setAsn] = useState('');
  const [country, setCountry] = useState('');
  const [tsp, setTsp] = useState('');
  const [city, setCity] = useState('');
  const [dropPercent, setDropPercent] = useState<number>(0);
  const [ttl, setTtl] = useState<number>(0);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<boolean>(false);

  const fetchRules = async () => {
    try {
      const res = await fetch(process.env.NEXT_PUBLIC_API_URL + '/rules');
      if (!res.ok) throw new Error(`Fetch failed: ${res.statusText}`);
      const data = await res.json();
      if (Array.isArray(data)) {
        setRules(data);
      } else {
        console.warn('Unexpected /rules response shape:', data);
        setRules([]);
      }
    } catch (err) {
      console.error('Failed to fetch rules:', err);
      setRules([]);
    }
  };

  const submitRule = async () => {
    setError(null);
    setSuccess(false);
    try {
      const res = await fetch(process.env.NEXT_PUBLIC_API_URL + '/rules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ asn, country, tsp, city, drop_percent: dropPercent, ttl }),
      });
      if (!res.ok) throw new Error(await res.text());

      setSuccess(true);
      setAsn('');
      setCountry('');
      setTsp('');
      setCity('');
      setDropPercent(0);
      setTtl(0);
      fetchRules();
    } catch (err: any) {
      setError(err.message || 'Failed to add rule');
    }
  };

  const deleteRule = async (rule: Rule) => {
    if (!rule.asn || !rule.country) {
      alert('ASN and Country are required to delete a rule.');
      return;
    }

    const query = new URLSearchParams({
      asn: rule.asn,
      country: rule.country,
      tsp: rule.tsp || '',
      city: rule.city || '',
    });

    try {
      const res = await fetch(`${process.env.NEXT_PUBLIC_API_URL}/rules?${query.toString()}`, {
        method: 'DELETE',
      });

      if (!res.ok) throw new Error(await res.text());
      fetchRules();
    } catch (err) {
      console.error('Delete failed:', err);
      alert('Failed to delete rule');
    }
  };

  useEffect(() => {
    fetchRules();
  }, []);

  return (
    <>
      <Head>
        <title>Alak Dashboard – Load Shedding Rules</title>
      </Head>
      <main style={{ padding: '2rem', fontFamily: 'system-ui, sans-serif' }}>
        <h1>🎛️ Load Shedding Rules</h1>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            submitRule();
          }}
          style={{ marginBottom: '1rem', display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}
        >
          <input placeholder="ASN" value={asn} onChange={(e) => setAsn(e.target.value)} required />
          <input placeholder="Country (e.g. IR)" value={country} onChange={(e) => setCountry(e.target.value)} required />
          <input placeholder="TSP (e.g. irancell)" value={tsp} onChange={(e) => setTsp(e.target.value)} />
          <input placeholder="City (e.g. tehran)" value={city} onChange={(e) => setCity(e.target.value)} />
          <input
            type="number"
            placeholder="Drop %"
            value={dropPercent}
            onChange={(e) => setDropPercent(Number(e.target.value))}
            min={0}
            max={100}
            style={{ width: '90px' }}
          />
          <input
            type="number"
            placeholder="TTL (s)"
            value={ttl}
            onChange={(e) => setTtl(Number(e.target.value))}
            min={0}
            style={{ width: '90px' }}
          />
          <button type="submit">➕ Add Rule</button>
        </form>

        {success && <p style={{ color: 'green' }}>✅ Rule added successfully</p>}
        {error && <p style={{ color: 'red' }}>❌ {error}</p>}

        <h2>📋 Active Rules</h2>

        {Array.isArray(rules) && rules.length === 0 ? (
          <p style={{ color: '#888' }}>No rules configured yet.</p>
        ) : (
          <table border={1} cellPadding={8} style={{ borderCollapse: 'collapse', width: '100%', marginTop: '1rem' }}>
            <thead>
              <tr>
                <th>ASN</th>
                <th>Country</th>
                <th>TSP</th>
                <th>City</th>
                <th>Drop %</th>
                <th>TTL (s)</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((rule, i) => (
                <tr key={i}>
                  <td>{rule.asn}</td>
                  <td>{rule.country}</td>
                  <td>{rule.tsp || '-'}</td>
                  <td>{rule.city || '-'}</td>
                  <td>{rule.drop_percent}</td>
                  <td>{rule.ttl ?? '-'}</td>
                  <td>
                    <button onClick={() => deleteRule(rule)}>🗑 Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </main>
    </>
  );
}
