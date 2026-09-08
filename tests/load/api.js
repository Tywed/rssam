import http from 'k6/http';
import { check, sleep } from 'k6';

// API read/write mix on a seeded database (≈200 feeds / 10k entries). Run:
//   k6 run -e BASE_URL=http://127.0.0.1:8080 -e AUTH_TOKEN=... tests/load/api.js
// Adjust VUS/DURATION via env. Thresholds are deliberately conservative:
// they exist to catch regressions (N+1 queries, lock contention), not to
// benchmark the box.
//
// Reference run (2 vCPU sandbox, Postgres 17 on the same host, 20 VUs, 30 s,
// 201 feeds / 10 001 entries): ~210 req/s, 0 failed, p95 list_entries 270 ms
// (19 ms unloaded; CPU-bound JSON of 50 entries ≈ 45 KB), search 220 ms,
// get_entry 20 ms, bulk_update 15 ms, list_feeds 18 ms. No ERROR/WARN in
// the journal; EXPLAIN of the list query is 7 ms (sort of 6.8k rows).
const baseURL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const token = __ENV.AUTH_TOKEN || 'dev-token'; // server needs ALLOW_DEV_TOKEN=true for the default
const headers = { 'X-Auth-Token': token, 'Content-Type': 'application/json' };

export const options = {
  vus: Number(__ENV.VUS || 20),
  duration: __ENV.DURATION || '30s',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{name:list_entries}': ['p(95)<300'],
    'http_req_duration{name:search}': ['p(95)<400'],
    'http_req_duration{name:get_entry}': ['p(95)<150'],
    'http_req_duration{name:list_feeds}': ['p(95)<300'],
    'http_req_duration{name:bulk_update}': ['p(95)<300'],
  },
};

const terms = ['go', 'rust', 'rss', 'postgres', 'kernel'];

export default function () {
  const offset = Math.floor(Math.random() * 9000);
  const list = http.get(`${baseURL}/v1/entries?limit=50&offset=${offset}&status=unread`, { headers, tags: { name: 'list_entries' } });
  check(list, { 'list 200': (r) => r.status === 200 });

  let ids = [];
  try { ids = list.json('data').map((e) => e.id); } catch (_) {}
  if (ids.length > 0) {
    const id = ids[Math.floor(Math.random() * ids.length)];
    const one = http.get(`${baseURL}/v1/entries/${id}`, { headers, tags: { name: 'get_entry' } });
    check(one, { 'get 200': (r) => r.status === 200 });

    // Flip a few entries read/unread: exercises the bulk UPDATE path + WS fan-out.
    const flip = ids.slice(0, 5);
    const bulk = http.put(`${baseURL}/v1/entries`, JSON.stringify({ entry_ids: flip, status: 'read' }), { headers, tags: { name: 'bulk_update' } });
    check(bulk, { 'bulk 200': (r) => r.status === 200 });
    http.put(`${baseURL}/v1/entries`, JSON.stringify({ entry_ids: flip, status: 'unread' }), { headers, tags: { name: 'bulk_update' } });
  }

  const q = terms[Math.floor(Math.random() * terms.length)];
  const search = http.get(`${baseURL}/v1/entries?q=${q}&limit=20`, { headers, tags: { name: 'search' } });
  check(search, { 'search 200': (r) => r.status === 200 });

  const feeds = http.get(`${baseURL}/v1/feeds?limit=100`, { headers, tags: { name: 'list_feeds' } });
  check(feeds, { 'feeds 200': (r) => r.status === 200 });

  sleep(0.1);
}
