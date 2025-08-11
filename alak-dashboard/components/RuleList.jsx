'use client'

import { useEffect, useState } from 'react';

async function fetchRules() {
  const res = await fetch(`/api/rules`);
  if (!res.ok) throw new Error(`Failed to fetch rules: ${res.status}`);
  return res.json();
}

async function patchRule(rule, newEnabled) {
  const updated = { ...rule, enabled: newEnabled };
  const res = await fetch(`/api/rules`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(updated),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

export default function RuleList() {
  const [rules, setRules] = useState([]);
  const [errMsg, setErrMsg] = useState(null);
  const [busyKey, setBusyKey] = useState(null); // prevent double-clicks

  useEffect(() => { loadRules(); }, []);

  async function loadRules() {
    setErrMsg(null);
    try {
      const data = await fetchRules();
      if (!Array.isArray(data)) throw new Error('Invalid response format');
      setRules(data);
    } catch (err) {
      setErrMsg(err.message);
    }
  }

  async function handleToggle(rule, newEnabled) {
    setErrMsg(null);
    const key = `${rule.asn}|${rule.country}|${rule.tsp}`;
    try {
      setBusyKey(key);
      await patchRule(rule, newEnabled);
      await loadRules();
    } catch (err) {
      setErrMsg('Failed to update rule: ' + err.message);
    } finally {
      setBusyKey(null);
    }
  }

  return (
    <div>
      <h2>Existing Rules</h2>
      {errMsg && <p style={{ color: 'red' }}>{errMsg}</p>}
      <table border="1" cellPadding="6" style={{ borderCollapse: 'collapse', minWidth: 600 }}>
        <thead>
          <tr>
            <th>ASN</th>
            <th>Country</th>
            <th>TSP</th>
            <th>Drop %</th>
            <th>Enabled</th>
            <th>Toggle</th>
          </tr>
        </thead>
        <tbody>
          {rules.map((rule, i) => {
            const key = `${rule.asn}|${rule.country}|${rule.tsp}`;
            const isBusy = busyKey === key;
            return (
              <tr key={i}>
                <td>{String(rule?.asn ?? '')}</td>
                <td>{String(rule?.country ?? '')}</td>
                <td>{String(rule?.tsp ?? '')}</td>
                <td>{Number.isFinite(rule?.drop_percent) ? rule.drop_percent : '-'}</td>
                <td style={{ color: rule.enabled ? 'green' : 'gray' }}>
                  {rule.enabled ? 'Yes' : 'No'}
                </td>
                <td>
                  <button
                    disabled={isBusy}
                    style={{
                      background: rule.enabled ? '#ff7878' : '#7fff7f',
                      color: '#222',
                      padding: '3px 12px',
                      border: '1px solid #ccc',
                      borderRadius: 5,
                      cursor: isBusy ? 'wait' : 'pointer',
                      opacity: isBusy ? 0.6 : 1
                    }}
                    onClick={() => handleToggle(rule, !rule.enabled)}
                  >
                    {isBusy ? 'Working…' : (rule.enabled ? 'Disable' : 'Enable')}
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
