.PHONY: run load-test load-test-staged load-test-smoke

# Requires: k6 (https://k6.io/docs/getting-started/installation/)

load-test:
	k6 run k6.js

load-test-staged:
	k6 run --stage 30s:5,1m:10,30s:0 k6.js

load-test-smoke:
	k6 run --vus 2 --duration 30s k6.js
