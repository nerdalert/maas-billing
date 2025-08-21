import http from "k6/http";
import { check, sleep } from "k6";

// ------------------------------
// Config via environment
// ------------------------------
const API_HOST   = __ENV.API_HOST  || "simulator-llm.apps.summit-gpu.octo-emerging.redhataicoe.com";
const MODEL_ID   = __ENV.MODEL_ID  || "simulator-model";
const MODE       = __ENV.MODE      || "burst";   // "burst" | "soak"

// Burst config
const BURST_ITERS  = Number(__ENV.BURST_ITERS  || 25);  // number of requests per user
const BURST_VUS    = Number(__ENV.BURST_VUS    || 10);  // VUs per user

// Soak config
const DURATION     = __ENV.DURATION   || "10m";  // test duration (soak)
const RATE_FREE    = Number(__ENV.RATE_FREE  || 5);   // rps for free user
const RATE_PREM    = Number(__ENV.RATE_PREM  || 10);  // rps for premium user

// Tokens
const MAX_TOK_FREE = Number(__ENV.MAX_TOK_FREE  || 10);
const MAX_TOK_PREM = Number(__ENV.MAX_TOK_PREM  || 15);

// Sleep
const NO_SLEEP   = (__ENV.NO_SLEEP || "false").toLowerCase() === "true";

// API keys
const KEY_FREE1    = __ENV.KEY_FREE1    || "freeuser1_key";
const KEY_FREE2    = __ENV.KEY_FREE2    || "freeuser2_key";
const KEY_PREM1    = __ENV.KEY_PREM1    || "premiumuser1_key";
const KEY_PREM2    = __ENV.KEY_PREM2    || "premiumuser2_key";

const BASE_URL = `http://${API_HOST}/v1/chat/completions`;

// ------------------------------
// Scenario definitions
// ------------------------------
let scenarios;

if (MODE === "burst") {
  scenarios = {
    free1: { executor: "shared-iterations", exec: "free1", vus: BURST_VUS, iterations: BURST_ITERS, maxDuration: "5m" },
    free2: { executor: "shared-iterations", exec: "free2", vus: BURST_VUS, iterations: BURST_ITERS, maxDuration: "5m" },
    prem1: { executor: "shared-iterations", exec: "prem1", vus: BURST_VUS, iterations: BURST_ITERS, maxDuration: "5m" },
    prem2: { executor: "shared-iterations", exec: "prem2", vus: BURST_VUS, iterations: BURST_ITERS, maxDuration: "5m" },
  };
} else {
  scenarios = {
    free1: { executor: "constant-arrival-rate", exec: "free1", rate: RATE_FREE, timeUnit: "1s", duration: DURATION, preAllocatedVUs: 20, maxVUs: 200 },
    free2: { executor: "constant-arrival-rate", exec: "free2", rate: RATE_FREE, timeUnit: "1s", duration: DURATION, preAllocatedVUs: 20, maxVUs: 200 },
    prem1: { executor: "constant-arrival-rate", exec: "prem1", rate: RATE_PREM, timeUnit: "1s", duration: DURATION, preAllocatedVUs: 20, maxVUs: 200 },
    prem2: { executor: "constant-arrival-rate", exec: "prem2", rate: RATE_PREM, timeUnit: "1s", duration: DURATION, preAllocatedVUs: 20, maxVUs: 200 },
  };
}

export const options = {
  discardResponseBodies: true,
  scenarios,
};

// ------------------------------
// Shared request function
// ------------------------------
function postChat(apikey, prompt, max_tokens) {
  const payload = JSON.stringify({
    model: MODEL_ID,
    messages: [{ role: "user", content: prompt }],
    max_tokens,
  });

  const headers = {
    "Authorization": `APIKEY ${apikey}`,
    "Content-Type": "application/json",
  };

  const res = http.post(BASE_URL, payload, { headers });
  check(res, { "status is 2xx/3xx": (r) => r.status >= 200 && r.status < 400 });
  return res;
}

// ------------------------------
// Scenario entry points
// ------------------------------
export function free1() {
  postChat(KEY_FREE1, "Free user request", MAX_TOK_FREE);
  if (!NO_SLEEP) sleep(0.05);
}
export function free2() {
  postChat(KEY_FREE2, "Second free user request", MAX_TOK_FREE);
  if (!NO_SLEEP) sleep(0.05);
}
export function prem1() {
  postChat(KEY_PREM1, "Premium user 1 request", MAX_TOK_PREM);
  if (!NO_SLEEP) sleep(0.02);
}
export function prem2() {
  postChat(KEY_PREM2, "Premium user 2 request", MAX_TOK_PREM);
  if (!NO_SLEEP) sleep(0.02);
}

