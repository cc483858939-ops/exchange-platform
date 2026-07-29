import http from 'k6/http';
import { check } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const baseURL = __ENV.BASE_URL || 'http://127.0.0.1:3000';
const virtualUsers = Number(__ENV.VUS || 10);
const duration = __ENV.DURATION || '20s';
const articleIDs = String(__ENV.ARTICLE_IDS || '')
  .split(',')
  .map((value) => value.trim())
  .filter(Boolean);

if (articleIDs.length === 0) {
  throw new Error('ARTICLE_IDS must contain one or more comma-separated article IDs');
}

export const options = { vus: virtualUsers, duration, discardResponseBodies: false };

const mutationSuccess = new Rate('like_mutation_success');
const mutationLatency = new Trend('like_mutation_duration', true);
const changedMutations = new Counter('like_mutations_changed');
let liked = false;

function jsonParams(accessToken) {
  return {
    // Register already returns an RFC 7235 Bearer header value.
    headers: { Authorization: accessToken, 'Content-Type': 'application/json' },
    tags: { endpoint: 'article_like' },
  };
}

export function setup() {
  const runID = `ltqps${Date.now()}${Math.floor(Math.random() * 1e6)}`;
  const users = [];
  for (let index = 0; index < virtualUsers; index += 1) {
    const username = `${runID}u${index}`;
    const password = `LoadTest-${runID}-${index}`;
    const response = http.post(
      `${baseURL}/api/auth/register`,
      JSON.stringify({ username, password }),
      { headers: { 'Content-Type': 'application/json' }, tags: { endpoint: 'register_setup' } },
    );
    if (response.status !== 200) {
      throw new Error(`failed to create load-test user ${index}: ${response.status} ${response.body}`);
    }
    const token = response.json('access_token');
    if (!token) {
      throw new Error(`registration response for user ${index} did not contain an access token`);
    }
    users.push({ username, token });
  }
  return { users, articleIDs };
}

export default function (data) {
  const user = data.users[(__VU - 1) % data.users.length];
  const articleID = data.articleIDs[(__VU - 1) % data.articleIDs.length];
  const expectedLiked = !liked;
  const response = http.request(
    expectedLiked ? 'PUT' : 'DELETE',
    `${baseURL}/api/articles/${articleID}/like`,
    null,
    jsonParams(user.token),
  );
  mutationLatency.add(response.timings.duration);

  let body;
  try {
    body = response.json();
  } catch (_) {
    body = null;
  }
  const ok = check(response, {
    'like request returned 200': (res) => res.status === 200,
    'returned the requested liked state': () => body && body.liked === expectedLiked,
    'returned a non-negative counter': () => body && Number.isInteger(body.likes) && body.likes >= 0,
  });
  mutationSuccess.add(ok);
  if (ok) {
    changedMutations.add(1);
    liked = expectedLiked;
  }
}
