package likes

import "github.com/go-redis/redis/v7"

var mutateScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if redis.call('GET', KEYS[1]) ~= '1' then
  return redis.error_reply('LIKE_NOT_READY')
end
local count_raw = redis.call('GET', KEYS[2])
local version_raw = redis.call('GET', KEYS[4])
if not count_raw or not version_raw then
  return redis.error_reply('LIKE_NOT_READY')
end
local count = tonumber(count_raw)
local version = tonumber(version_raw)
if not count or count < 0 or not version or version < 0 then
  return redis.error_reply('LIKE_NOT_READY')
end
if redis.call('SCARD', KEYS[3]) ~= count then
  return redis.error_reply('LIKE_NOT_READY')
end
local current = redis.call('SISMEMBER', KEYS[3], ARGV[2])
local desired = ARGV[3] == '1' and 1 or 0
local changed = (desired == 1 and current == 0) or (desired == 0 and current == 1)
if changed then
  if not type_matches(KEYS[5], 'set') or
     not type_matches(KEYS[6], 'set') or
     not type_matches(KEYS[7], 'hash') or
     not type_matches(KEYS[8], 'set') or
     not type_matches(KEYS[9], 'zset') or
     not type_matches(KEYS[10], 'hash') then
    return redis.error_reply('LIKE_TYPE_PRECHECK')
  end

  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[2])
  redis.call('PERSIST', KEYS[4])
  if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PERSIST', KEYS[3]) end
  redis.call('HDEL', KEYS[10], ARGV[1])
  redis.call('SADD', KEYS[8], ARGV[1])

  if desired == 1 then
    redis.call('SADD', KEYS[3], ARGV[2])
    count = count + 1
  else
    redis.call('SREM', KEYS[3], ARGV[2])
    count = math.max(0, count - 1)
  end
  version = version + 1
  redis.call('SET', KEYS[2], count)
  redis.call('SET', KEYS[4], version)
  redis.call('SADD', KEYS[5], ARGV[1])
  local pair = ARGV[2] .. ':' .. ARGV[1]
  redis.call('HSET', KEYS[7], pair, ARGV[3] .. '|' .. version .. '|' .. ARGV[4])
  redis.call('SADD', KEYS[6], pair)
  redis.call('ZADD', KEYS[9], ARGV[5], ARGV[1])
  current = desired
end
return {count, current, changed, version}
`)
var initializeScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'set') or
   not type_matches(KEYS[4], 'string') or
   not type_matches(KEYS[5], 'set') or
   not type_matches(KEYS[6], 'zset') or
   not type_matches(KEYS[7], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if redis.call('GET', KEYS[1]) == '1' then return 0 end
redis.call('DEL', KEYS[3])
for i = 5, #ARGV do redis.call('SADD', KEYS[3], ARGV[i]) end
redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[4], ARGV[2])
redis.call('SADD', KEYS[5], ARGV[3])
redis.call('ZADD', KEYS[6], ARGV[4], ARGV[3])
redis.call('HDEL', KEYS[7], ARGV[3])
redis.call('SET', KEYS[1], '1')
return 1
`)

var recoverScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'set') or
   not type_matches(KEYS[4], 'string') or
   not type_matches(KEYS[5], 'set') or
   not type_matches(KEYS[6], 'zset') or
   not type_matches(KEYS[7], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end

local ready = redis.call('GET', KEYS[1])
if ready == '1' then
  local count_raw = redis.call('GET', KEYS[2])
  local version_raw = redis.call('GET', KEYS[4])
  local count = count_raw and tonumber(count_raw)
  local version = version_raw and tonumber(version_raw)
  if count and count >= 0 and version and version >= 0 and redis.call('SCARD', KEYS[3]) == count then
    return 0
  end
end

local count = tonumber(ARGV[1])
local version = tonumber(ARGV[2])
if not count or count < 0 or not version or version < 0 then
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end

if ARGV[4] == 'marker' then
  if redis.call('SISMEMBER', KEYS[5], ARGV[3]) ~= 1 or
     redis.call('HGET', KEYS[7], ARGV[3]) ~= ARGV[5] or
     ARGV[2] ~= ARGV[5] then
    return redis.error_reply('LIKE_RECOVERY_FENCE_LOST')
  end
elseif ARGV[4] == 'zero' then
  if redis.call('SISMEMBER', KEYS[5], ARGV[3]) == 1 or
     redis.call('HEXISTS', KEYS[7], ARGV[3]) == 1 or
     count ~= 0 or version ~= 0 or tonumber(ARGV[6]) ~= 0 then
    return redis.error_reply('LIKE_RECOVERY_UNSAFE')
  end
else
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end

redis.call('DEL', KEYS[3])
redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[4], ARGV[2])
for i = 8, #ARGV do redis.call('SADD', KEYS[3], ARGV[i]) end
redis.call('SADD', KEYS[5], ARGV[3])
redis.call('ZADD', KEYS[6], ARGV[7], ARGV[3])
redis.call('HDEL', KEYS[7], ARGV[3])
redis.call('SET', KEYS[1], '1')
return 1
`)

var armExpiryScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'set') or
   not type_matches(KEYS[4], 'string') or
   not type_matches(KEYS[5], 'set') or
   not type_matches(KEYS[6], 'zset') or
   not type_matches(KEYS[7], 'hash') or
   not type_matches(KEYS[8], 'set') or
   not type_matches(KEYS[9], 'zset') or
   not type_matches(KEYS[10], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end

if redis.call('GET', KEYS[1]) ~= '1' then return 0 end
if redis.call('SISMEMBER', KEYS[5], ARGV[1]) ~= 1 then return 0 end
local count_raw = redis.call('GET', KEYS[2])
local version_raw = redis.call('GET', KEYS[4])
local count = count_raw and tonumber(count_raw)
local version = version_raw and tonumber(version_raw)
if not count or count < 0 or not version or version < 0 or
   redis.call('SCARD', KEYS[3]) ~= count or version ~= tonumber(ARGV[2]) then
  return 0
end
if redis.call('SISMEMBER', KEYS[8], ARGV[1]) == 1 or
   redis.call('ZSCORE', KEYS[9], ARGV[1]) or
   redis.call('HEXISTS', KEYS[10], ARGV[1]) == 1 then
  return 0
end

redis.call('HSET', KEYS[7], ARGV[1], ARGV[2])
redis.call('EXPIRE', KEYS[1], ARGV[3])
redis.call('EXPIRE', KEYS[2], ARGV[3])
redis.call('EXPIRE', KEYS[4], ARGV[3])
if redis.call('EXISTS', KEYS[3]) == 1 then redis.call('EXPIRE', KEYS[3], ARGV[3]) end
redis.call('ZREM', KEYS[6], ARGV[1])
return 1
`)

var purgePostScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'set') or
   not type_matches(KEYS[4], 'string') or
   not type_matches(KEYS[5], 'set') or
   not type_matches(KEYS[6], 'zset') or
   not type_matches(KEYS[7], 'hash') or
   not type_matches(KEYS[8], 'set') or
   not type_matches(KEYS[9], 'zset') or
   not type_matches(KEYS[10], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end

redis.call('DEL', KEYS[1], KEYS[2], KEYS[3], KEYS[4])
redis.call('SREM', KEYS[5], ARGV[1])
redis.call('ZREM', KEYS[6], ARGV[1])
redis.call('HDEL', KEYS[7], ARGV[1])
redis.call('SREM', KEYS[8], ARGV[1])
redis.call('ZREM', KEYS[9], ARGV[1])
redis.call('HDEL', KEYS[10], ARGV[1])
return 1
`)

var claimScript = redis.NewScript(`
local ids = redis.call('SMEMBERS', KEYS[1])
local result = {}
local limit = tonumber(ARGV[1])
for _, post_id in ipairs(ids) do
  if #result >= limit * 2 then break end
  if redis.call('HEXISTS', KEYS[3], post_id) == 0 then
    local claim_id = ARGV[3] .. ':' .. post_id
    redis.call('SREM', KEYS[1], post_id)
    redis.call('HSET', KEYS[3], post_id, claim_id)
    redis.call('ZADD', KEYS[2], ARGV[2], post_id)
    table.insert(result, post_id)
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
for _, post_id in ipairs(ids) do
  local claim_id = redis.call('HGET', KEYS[3], post_id)
  if claim_id then
    redis.call('HDEL', KEYS[3], post_id)
    redis.call('ZREM', KEYS[2], post_id)
    redis.call('SADD', KEYS[1], post_id)
    count = count + 1
  else
    redis.call('ZREM', KEYS[2], post_id)
  end
end
return count
`)
