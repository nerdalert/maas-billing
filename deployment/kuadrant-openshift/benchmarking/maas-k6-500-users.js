import http from "k6/http";
import { check, sleep } from "k6";

// ------------------------------
// Config via environment
// ------------------------------
const API_URL    = __ENV.API_URL   || "http://simulator-llm.apps.summit-gpu.octo-emerging.redhataicoe.com/v1/chat/completions";
const MODEL_ID   = __ENV.MODEL_ID  || "simulator-model";
const MODE       = __ENV.MODE      || "burst";   // "burst" | "soak"

// Burst config
const BURST_ITERS  = Number(__ENV.BURST_ITERS  || 1000);  // number of requests per user
const BURST_VUS    = Number(__ENV.BURST_VUS    || 20);    // VUs per user

// Soak config
const DURATION     = __ENV.DURATION   || "10m";  // test duration (soak)
const RATE_FREE    = Number(__ENV.RATE_FREE  || 5);   // rps for free user
const RATE_PREM    = Number(__ENV.RATE_PREM  || 10);  // rps for premium user

// Tokens
const MAX_TOK_FREE = Number(__ENV.MAX_TOK_FREE  || 10);
const MAX_TOK_PREM = Number(__ENV.MAX_TOK_PREM  || 15);

// Sleep
const NO_SLEEP   = (__ENV.NO_SLEEP || "false").toLowerCase() === "true";

const BASE_URL = API_URL;

// ------------------------------
// Generate 500 users API keys
// ------------------------------
const freeUsers = [];
const premiumUsers = [];

// Generate 250 free users
for (let i = 1; i <= 250; i++) {
  freeUsers.push(`freeuser${i}_key`);
}

// Generate 250 premium users
for (let i = 1; i <= 250; i++) {
  premiumUsers.push(`premiumuser${i}_key`);
}

// ------------------------------
// Scenario definitions
// ------------------------------
let scenarios = {};

if (MODE === "burst") {
  // Create scenarios for all 250 free users
  for (let i = 0; i < freeUsers.length; i++) {
    scenarios[`free_user_${i + 1}`] = {
      executor: "shared-iterations",
      exec: "freeUserTest",
      vus: BURST_VUS,
      iterations: BURST_ITERS,
      maxDuration: "10m",
      env: { USER_INDEX: i.toString(), USER_TYPE: "free" }
    };
  }
  
  // Create scenarios for all 250 premium users
  for (let i = 0; i < premiumUsers.length; i++) {
    scenarios[`premium_user_${i + 1}`] = {
      executor: "shared-iterations",
      exec: "premiumUserTest",
      vus: BURST_VUS,
      iterations: BURST_ITERS,
      maxDuration: "10m",
      env: { USER_INDEX: i.toString(), USER_TYPE: "premium" }
    };
  }
} else {
  // Soak mode - distribute load across all users
  for (let i = 0; i < freeUsers.length; i++) {
    scenarios[`free_user_${i + 1}`] = {
      executor: "constant-arrival-rate",
      exec: "freeUserTest",
      rate: RATE_FREE,
      timeUnit: "1s",
      duration: DURATION,
      preAllocatedVUs: 2,
      maxVUs: 10,
      env: { USER_INDEX: i.toString(), USER_TYPE: "free" }
    };
  }
  
  for (let i = 0; i < premiumUsers.length; i++) {
    scenarios[`premium_user_${i + 1}`] = {
      executor: "constant-arrival-rate",
      exec: "premiumUserTest",
      rate: RATE_PREM,
      timeUnit: "1s",
      duration: DURATION,
      preAllocatedVUs: 2,
      maxVUs: 10,
      env: { USER_INDEX: i.toString(), USER_TYPE: "premium" }
    };
  }
}

export const options = {
  discardResponseBodies: true,
  scenarios,
};

// ------------------------------
// Shared request function
// ------------------------------
function postChat(apikey, prompt, max_tokens, userType) {
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
  check(res, { 
    [`${userType} status is 2xx/3xx`]: (r) => r.status >= 200 && r.status < 400,
    [`${userType} response time < 30s`]: (r) => r.timings.duration < 30000,
  });
  return res;
}

// ------------------------------
// Scenario entry points
// ------------------------------
export function freeUserTest() {
  const userIndex = parseInt(__ENV.USER_INDEX);
  const apiKey = freeUsers[userIndex];
  const userId = `freeuser${userIndex + 1}`;
  
  postChat(apiKey, `Free user ${userId} request`, MAX_TOK_FREE, "free");
  if (!NO_SLEEP) sleep(0.05);
}

export function premiumUserTest() {
  const userIndex = parseInt(__ENV.USER_INDEX);
  const apiKey = premiumUsers[userIndex];
  const userId = `premiumuser${userIndex + 1}`;
  
  postChat(apiKey, `Premium user ${userId} request`, MAX_TOK_PREM, "premium");
  if (!NO_SLEEP) sleep(0.02);
}