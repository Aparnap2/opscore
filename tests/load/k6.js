import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errorRate = new Rate('errors');
const uploadDuration = new Trend('upload_duration');
const vendorDuration = new Trend('vendor_duration');

// Simulate 3 tenants with different load profiles
const tenants = [
    { id: 'tenant-alpha', apiKey: 'owner-dev-key', weight: 5 },   // heavy user
    { id: 'tenant-beta', apiKey: 'ops-admin-dev-key', weight: 3 },  // medium user
    { id: 'tenant-gamma', apiKey: 'reviewer-dev-key', weight: 1 },  // light user
];

export const options = {
    stages: [
        { duration: '30s', target: 10 },  // ramp up to 10 VUs
        { duration: '1m', target: 20 },   // sustain at 20 VUs
        { duration: '30s', target: 0 },   // ramp down
    ],
    thresholds: {
        http_req_duration: ['p(95)<2000'],  // 95% of requests under 2s
        errors: ['rate<0.1'],               // error rate under 10%
    },
};

function pickTenant() {
    const totalWeight = tenants.reduce(function (sum, t) { return sum + t.weight; }, 0);
    var r = Math.random() * totalWeight;
    for (var i = 0; i < tenants.length; i++) {
        var t = tenants[i];
        r -= t.weight;
        if (r <= 0) return t;
    }
    return tenants[0];
}

function simulateUpload() {
    var tenant = pickTenant();
    var fileName = 'test-' + Date.now() + '.pdf';
    var fileContent = JSON.stringify({ simulated: true, timestamp: Date.now() });

    var payload = http.file(fileContent, fileName, 'application/pdf');

    var params = {
        headers: {
            'X-Tenant-ID': tenant.id,
            'X-API-Key': tenant.apiKey,
        },
    };

    var res = http.post('http://localhost:8080/upload', payload, params);

    check(res, {
        'upload status is 202': function (r) { return r.status === 202; },
        'upload has job_id': function (r) { return JSON.parse(r.body).job_id !== undefined; },
    });

    errorRate.add(res.status !== 202);
    uploadDuration.add(res.timings.duration);

    return res;
}

function simulateVendorCreate() {
    var tenant = pickTenant();

    var payload = {
        name: 'Test Vendor ' + Date.now(),
        gst: '27ABCDE1234F1Z' + String(Math.floor(Math.random() * 10)),
        pan: 'ABCDE' + String(Math.floor(Math.random() * 10000)) + 'F',
    };

    var params = {
        headers: {
            'X-Tenant-ID': tenant.id,
            'X-API-Key': tenant.apiKey,
            'Content-Type': 'application/x-www-form-urlencoded',
        },
    };

    var res = http.post('http://localhost:8080/vendors', payload, params);

    check(res, {
        'vendor status is 201': function (r) { return r.status === 201; },
        'vendor has vendor_id': function (r) { return JSON.parse(r.body).vendor_id !== undefined; },
    });

    errorRate.add(res.status !== 201);
    vendorDuration.add(res.timings.duration);

    return res;
}

function simulateStatusCheck() {
    var tenant = pickTenant();

    var params = {
        headers: {
            'X-Tenant-ID': tenant.id,
            'X-API-Key': tenant.apiKey,
        },
    };

    var res = http.get('http://localhost:8080/status/summary', params);

    check(res, {
        'status summary is 200': function (r) { return r.status === 200; },
    });

    errorRate.add(res.status !== 200);
    sleep(0.5);  // lighter load on read endpoints
}

export default function () {
    // Mix of operations matching real usage patterns
    simulateUpload();
    sleep(1);
    simulateVendorCreate();
    sleep(1);
    simulateStatusCheck();
    sleep(1);

    // Occasionally check health (no auth)
    if (Math.random() < 0.3) {
        var healthRes = http.get('http://localhost:8080/health');
        check(healthRes, { 'health is 200': function (r) { return r.status === 200; } });
    }
}
