import http from 'k6/http';
import { check, sleep } from 'k6';

// Minimal smoke: health + metrics (optional token). Run:
//   k6 run -e BASE_URL=http://127.0.0.1:8080 tests/load/smoke.js
const baseURL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const metricsToken = __ENV.METRICS_TOKEN || '';

export const options = {
  vus: 5,
  duration: '30s',
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<500'],
  },
};

export default function () {
  const health = http.get(`${baseURL}/healthz`);
  check(health, { 'healthz 200': (r) => r.status === 200 });

  if (metricsToken) {
    const metrics = http.get(`${baseURL}/metrics`, {
      headers: { Authorization: `Bearer ${metricsToken}` },
    });
    check(metrics, { 'metrics 200': (r) => r.status === 200 });
  }

  sleep(0.2);
}
