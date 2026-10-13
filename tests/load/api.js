import http from 'k6/http';
import { check, sleep } from 'k6';

// API read/write mix on a live database. Run:
//   k6 run -e BASE_URL=http://127.0.0.1:8080 -e API_KEY=… tests/load/api.js
// The key is sent as X-Auth-Token (Settings → API keys). There is no default:
// AUTH_TOKEN is not supported and the server will not start if it is set.
// Adjust VUS/DURATION via env. Thresholds catch regressions (N+1, lock
// contention), not to benchmark the box.
//
// Responses are {"data":…,"total":…}: lists put an array in data, a single
// resource puts the object in data with total=1.
const baseURL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const token = __ENV.API_KEY || '';
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

function envelopeList(r) {
  try {
    const j = r.json();
    return r.status === 200 && Array.isArray(j.data) && Number.isInteger(j.total);
  } catch (_) {
    return false;
  }
}

function envelopeOne(r) {
  try {
    const j = r.json();
    return r.status === 200 && j.data && typeof j.data === 'object' && !Array.isArray(j.data) && j.total === 1;
  } catch (_) {
    return false;
  }
}

export function setup() {
  if (!token) {
    throw new Error('API_KEY is required: k6 run -e API_KEY=… tests/load/api.js');
  }
}

export default function () {
  const offset = Math.floor(Math.random() * 9000);
  const list = http.get(`${baseURL}/v1/entries?limit=50&offset=${offset}&status=unread`, { headers, tags: { name: 'list_entries' } });
  check(list, { 'list envelope': envelopeList });

  let ids = [];
  try { ids = list.json('data').map((e) => e.id); } catch (_) {}
  if (ids.length > 0) {
    const id = ids[Math.floor(Math.random() * ids.length)];
    const one = http.get(`${baseURL}/v1/entries/${id}`, { headers, tags: { name: 'get_entry' } });
    check(one, { 'get envelope': envelopeOne });

    const flip = ids.slice(0, 5);
    const bulk = http.put(`${baseURL}/v1/entries`, JSON.stringify({ entry_ids: flip, status: 'read' }), { headers, tags: { name: 'bulk_update' } });
    check(bulk, { 'bulk envelope': envelopeOne });
    http.put(`${baseURL}/v1/entries`, JSON.stringify({ entry_ids: flip, status: 'unread' }), { headers, tags: { name: 'bulk_update' } });
  }

  const q = terms[Math.floor(Math.random() * terms.length)];
  const search = http.get(`${baseURL}/v1/entries?q=${q}&limit=20`, { headers, tags: { name: 'search' } });
  check(search, { 'search envelope': envelopeList });

  const feeds = http.get(`${baseURL}/v1/feeds?limit=100`, { headers, tags: { name: 'list_feeds' } });
  check(feeds, { 'feeds envelope': envelopeList });

  sleep(0.1);
}
