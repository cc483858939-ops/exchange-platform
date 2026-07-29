package likes

import "github.com/go-redis/redis/v7"

var mutateScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= '1' then
  return redis.error_reply('LIKE_NOT_READY')
end
local changed
if ARGV[3] == '1' then
  changed = redis.call('SADD', KEYS[3], ARGV[2])
else
  changed = redis.call('SREM', KEYS[3], ARGV[2])
end
local count = tonumber(redis.call('GET', KEYS[2]) or '0')
local version = tonumber(redis.call('GET', KEYS[4]) or '0')
if changed == 1 then
  if ARGV[3] == '1' then count = count + 1 else count = math.max(0, count - 1) end
  version = version + 1
  redis.call('SET', KEYS[2], count)
  redis.call('SET', KEYS[4], version)
  redis.call('SADD', KEYS[5], ARGV[1])
  local pair = ARGV[2] .. ':' .. ARGV[1]
  redis.call('HSET', KEYS[7], pair, ARGV[3] .. '|' .. version .. '|' .. ARGV[4])
  redis.call('SADD', KEYS[6], pair)
end
local current = redis.call('SISMEMBER', KEYS[3], ARGV[2])
return {count, current, changed, version}
`)
var initializeScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == '1' then return 0 end
redis.call('DEL', KEYS[3])
for i = 3, #ARGV do redis.call('SADD', KEYS[3], ARGV[i]) end
redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[4], ARGV[2])
redis.call('SET', KEYS[1], '1')
return 1
`)

var claimScript = redis.NewScript(`
local ids = redis.call('SMEMBERS', KEYS[1])
local result = {}
local limit = tonumber(ARGV[1])
for _, article_id in ipairs(ids) do
  if #result >= limit * 2 then break end
  if redis.call('HEXISTS', KEYS[3], article_id) == 0 then
    local claim_id = ARGV[3] .. ':' .. article_id
    redis.call('SREM', KEYS[1], article_id)
    redis.call('HSET', KEYS[3], article_id, claim_id)
    redis.call('ZADD', KEYS[2], ARGV[2], article_id)
    table.insert(result, article_id)
    table.insert(result, claim_id)
  end
end
return result
`)

var ackClaimScript = redis.NewScript(`
if redis.call('HGET', KEYS[2], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[1], ARGV[1])
return 1
`)

var requeueClaimScript = redis.NewScript(`
if redis.call('HGET', KEYS[3], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[3], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('SADD', KEYS[1], ARGV[1])
return 1
`)

var reapExpiredScript = redis.NewScript(`
local ids = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
local count = 0
for _, article_id in ipairs(ids) do
  local claim_id = redis.call('HGET', KEYS[3], article_id)
  if claim_id then
    redis.call('HDEL', KEYS[3], article_id)
    redis.call('ZREM', KEYS[2], article_id)
    redis.call('SADD', KEYS[1], article_id)
    count = count + 1
  else
    redis.call('ZREM', KEYS[2], article_id)
  end
end
return count
`)
