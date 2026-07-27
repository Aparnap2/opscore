import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const apiBase = __ENV.API_BASE || 'http://localhost:8081';

const failureRate = new Rate('failed_requests');
const uploadLatency = new Trend('upload_latency');
const summaryLatency = new Trend('summary_latency');

export const options = {
	thresholds: {
		failed_requests: ['rate<0.01'],
		http_req_duration: ['p(95)<2000'],
	},
	scenarios: {
		smoke: {
			executor: 'constant-vus',
			vus: 1,
			duration: '30s',
			startTime: '0s',
		},
		normal_load: {
			executor: 'ramping-vus',
			startVUs: 0,
			stages: [
				{ duration: '30s', target: 10 },
				{ duration: '1m', target: 10 },
				{ duration: '30s', target: 0 },
			],
			startTime: '30s',
		},
	},
};

export default function () {
	// Health check
	let res = http.get(`${apiBase}/health`, {
		headers: { 'X-Tenant-ID': 'load-test' },
	});
	check(res, { 'health status 200': (r) => r.status === 200 });
	failureRate.add(res.status !== 200);

	// Summary endpoint
	let t1 = Date.now();
	res = http.get(`${apiBase}/status/summary`, {
		headers: { 'X-Tenant-ID': 'load-test' },
	});
	summaryLatency.add(Date.now() - t1);
	check(res, { 'summary status 200': (r) => r.status === 200 });
	failureRate.add(res.status !== 200);

	// Metrics endpoints
	res = http.get(`${apiBase}/metrics/llm-summary`, {
		headers: { 'X-Tenant-ID': 'load-test' },
	});
	check(res, { 'llm metrics 200': (r) => r.status === 200 });

	res = http.get(`${apiBase}/metrics/workflow-summary`, {
		headers: { 'X-Tenant-ID': 'load-test' },
	});
	check(res, { 'workflow metrics 200': (r) => r.status === 200 });

	// Job list
	res = http.get(`${apiBase}/jobs/recent?limit=5`, {
		headers: { 'X-Tenant-ID': 'load-test' },
	});
	check(res, { 'jobs recent 200': (r) => r.status === 200 });

	sleep(1);
}
