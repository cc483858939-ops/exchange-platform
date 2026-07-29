package likes

import "github.com/go-redis/redis/v7"

var behaviorClaimScript = redis.NewScript(`
local result = {}
local deferred = {}
local limit = tonumber(ARGV[1])
for _ = 1, limit do
  local pair = redis.call('SPOP', KEYS[1])
  if not pair then break end
  if redis.call('HEXISTS', KEYS[3], pair) == 0 then
    local claim_id = ARGV[3] .. ':' .. pair
    redis.call('HSET', KEYS[3], pair, claim_id)
    redis.call('ZADD', KEYS[2], ARGV[2], pair)
    table.insert(result, pair)
    table.insert(result, claim_id)
  else
    table.insert(deferred, pair)
  end
end
for _, pair in ipairs(deferred) do
  redis.call('SADD', KEYS[1], pair)
end
return result
`)

var behaviorAckScript = redis.NewScript(`
if redis.call('HGET', KEYS[4], ARGV[1]) ~= ARGV[2] then return 0 end
local state = redis.call('HGET', KEYS[2], ARGV[1])
if state then
  local current_version = string.match(state, '^[^|]*|([^|]+)|')
  if current_version == ARGV[3] then
    redis.call('HDEL', KEYS[2], ARGV[1])
    redis.call('SREM', KEYS[1], ARGV[1])
  else
    redis.call('SADD', KEYS[1], ARGV[1])
  end
else
  redis.call('SREM', KEYS[1], ARGV[1])
end
redis.call('HDEL', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[3], ARGV[1])
return 1
`)

var behaviorRequeueScript = redis.NewScript(`
if redis.call('HGET', KEYS[4], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[3], ARGV[1])
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
  redis.call('SADD', KEYS[1], ARGV[1])
end
return 1
`)

var behaviorReapExpiredScript = redis.NewScript(`
local pairs = redis.call('ZRANGEBYSCORE', KEYS[3], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
local count = 0
for _, pair in ipairs(pairs) do
  if redis.call('HEXISTS', KEYS[4], pair) == 1 then
    redis.call('HDEL', KEYS[4], pair)
    if redis.call('HEXISTS', KEYS[2], pair) == 1 then
      redis.call('SADD', KEYS[1], pair)
    end
    count = count + 1
  end
  redis.call('ZREM', KEYS[3], pair)
end
return count
`)
