package likes

import "github.com/go-redis/redis/v7"

var mutateScript = redis.NewScript(`
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

local ready_type = redis.call('TYPE', KEYS[1]).ok
if ready_type ~= 'none' and ready_type ~= 'string' then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
local ready = redis.call('GET', KEYS[1])
if ready == 'deleted' then return redis.error_reply('LIKE_POST_DELETED') end

if not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'string') or
   not type_matches(KEYS[4], 'set') or
   not type_matches(KEYS[5], 'set') or
   not type_matches(KEYS[6], 'set') or
   not type_matches(KEYS[7], 'hash') or
   not type_matches(KEYS[8], 'set') or
   not type_matches(KEYS[9], 'zset') or
   not type_matches(KEYS[10], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end

local post_id = ARGV[1]
local user_id = ARGV[2]
if not string.match(post_id, '^%d+$') or post_id == '0' or
   not string.match(user_id, '^%d+$') or user_id == '0' then
  return redis.error_reply('LIKE_POST_NOT_READY')
end

if redis.call('SISMEMBER', KEYS[4], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_NOT_READY')
end
if ready ~= '1' then
  return redis.error_reply('LIKE_POST_NOT_READY')
end
local count_raw = redis.call('GET', KEYS[2])
local version_raw = redis.call('GET', KEYS[3])
if not count_raw or not version_raw then
  return redis.error_reply('LIKE_POST_NOT_READY')
end
if not string.match(count_raw, '^%d+$') or not string.match(version_raw, '^%d+$') then
  return redis.error_reply('LIKE_POST_NOT_READY')
end
local count = tonumber(count_raw)
local version = tonumber(version_raw)
if not count or count < 0 or not version or version < 0 then
  return redis.error_reply('LIKE_POST_NOT_READY')
end
local current = redis.call('SISMEMBER', KEYS[4], post_id)
local desired = ARGV[3] == '1' and 1 or 0
local changed = (desired == 1 and current == 0) or (desired == 0 and current == 1)
if changed then
  if desired == 0 and count == 0 then
    return redis.error_reply('LIKE_COUNT_INCONSISTENT')
  end

  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[2])
  redis.call('PERSIST', KEYS[3])
  redis.call('HDEL', KEYS[10], post_id)
  redis.call('SADD', KEYS[8], post_id)

  if desired == 1 then
    redis.call('SADD', KEYS[4], post_id)
    count = count + 1
  else
    redis.call('SREM', KEYS[4], post_id)
    count = count - 1
  end
  version = version + 1
  redis.call('SET', KEYS[2], count)
  redis.call('SET', KEYS[3], version)
  redis.call('SADD', KEYS[5], post_id)
  local pair = user_id .. ':' .. post_id
  redis.call('HSET', KEYS[7], pair, ARGV[3] .. '|' .. version .. '|' .. ARGV[4])
  redis.call('SADD', KEYS[6], pair)
  redis.call('ZADD', KEYS[9], ARGV[5], post_id)
  current = desired
end
return {count, current, changed, version}
`)

var scanUserLikesScript = redis.NewScript(`
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if actual == 'none' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_NOT_READY')
end
local result = redis.call('SSCAN', KEYS[1], ARGV[1], 'COUNT', ARGV[2])
local values = {result[1]}
for _, member in ipairs(result[2]) do table.insert(values, member) end
return values
`)

var initializeUserEmptyScript = redis.NewScript(`
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if actual == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') == 1 then return 0 end
  if redis.call('SCARD', KEYS[1]) > 0 then return redis.error_reply('LIKE_USER_NOT_READY') end
end
redis.call('SADD', KEYS[1], '0')
return 1
`)

var removeDeletedUserPostRelationsScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if user_type == 'none' then return redis.error_reply('LIKE_USER_NOT_READY') end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
local removed = 0
local result = {0}
for i = 1, #ARGV do
  local post_id = ARGV[i]
  if post_id ~= '0' then
    local ready_type = redis.call('TYPE', KEYS[i + 1]).ok
    if ready_type ~= 'none' and ready_type ~= 'string' then
      table.insert(result, post_id)
      table.insert(result, 'post_ready_type_error')
    else
      local ready = redis.call('GET', KEYS[i + 1])
      if not ready or ready == 'deleted' then
      removed = removed + redis.call('SREM', KEYS[1], post_id)
      elseif ready == '1' then
        table.insert(result, post_id)
        table.insert(result, 'post_lifecycle_mismatch')
      else
        table.insert(result, post_id)
        table.insert(result, 'post_ready_state_invalid')
      end
    end
  end
end
result[1] = removed
return result
`)

var beginRebuildScript = redis.NewScript(`
local ready_type = redis.call('TYPE', KEYS[1]).ok
if ready_type ~= 'none' and ready_type ~= 'string' then return redis.error_reply('LIKE_TYPE_PRECHECK') end
if redis.call('GET', KEYS[1]) == 'deleted' and ARGV[3] ~= '1' then return redis.error_reply('LIKE_POST_DELETED') end
local token_type = redis.call('TYPE', KEYS[2]).ok
if token_type ~= 'none' and token_type ~= 'string' then return redis.error_reply('LIKE_TYPE_PRECHECK') end
local current_token = redis.call('GET', KEYS[2])
if current_token == ARGV[1] then
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
  return 1
end
if current_token or not redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[2], 'NX') then
  return redis.error_reply('LIKE_RECOVERY_FENCE_LOST')
end
return 1
`)

var releaseRebuildScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok == 'string' and redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// This fence is independent of live-state/global-index type prechecks.
var markPostDeletedScript = redis.NewScript(`
-- A delayed duplicate must not turn an already collected fence permanent again.
if redis.call('TYPE', KEYS[1]).ok ~= 'string' or redis.call('GET', KEYS[1]) ~= 'deleted' then
  redis.call('SET', KEYS[1], 'deleted')
end
redis.call('DEL', KEYS[2])
return 1
`)

var initializeScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok == 'string' and redis.call('GET', KEYS[1]) == 'deleted' and string.sub(ARGV[5], 1, 11) ~= 'reactivate:' then return redis.error_reply('LIKE_POST_DELETED') end
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'string') or
   not type_matches(KEYS[4], 'set') or
   not type_matches(KEYS[5], 'zset') or
   not type_matches(KEYS[6], 'hash') or not type_matches(KEYS[7], 'string') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if ARGV[5] == '' or redis.call('GET', KEYS[7]) ~= ARGV[5] then return redis.error_reply('LIKE_RECOVERY_FENCE_LOST') end
if tonumber(ARGV[1]) ~= 0 or tonumber(ARGV[2]) ~= 0 then return redis.error_reply('LIKE_RECOVERY_UNSAFE') end
local current_ready = redis.call('GET', KEYS[1])
if current_ready == '1' then
  local count = redis.call('GET', KEYS[2])
  local version = redis.call('GET', KEYS[3])
  if count and version and string.match(count, '^%d+$') and string.match(version, '^%d+$') then
    redis.call('DEL', KEYS[7])
    return 0
  end
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end
if current_ready or redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 or
   redis.call('SISMEMBER', KEYS[4], ARGV[3]) == 1 or redis.call('HEXISTS', KEYS[6], ARGV[3]) == 1 then
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end
redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[3], ARGV[2])
redis.call('SADD', KEYS[4], ARGV[3])
redis.call('ZADD', KEYS[5], ARGV[4], ARGV[3])
redis.call('HDEL', KEYS[6], ARGV[3])
redis.call('SET', KEYS[1], '1')
redis.call('DEL', KEYS[7])
return 1
`)

var recoverScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok == 'string' and redis.call('GET', KEYS[1]) == 'deleted' then return redis.error_reply('LIKE_POST_DELETED') end
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'string') or
   not type_matches(KEYS[4], 'set') or
   not type_matches(KEYS[5], 'zset') or
   not type_matches(KEYS[6], 'hash') or not type_matches(KEYS[7], 'string') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if ARGV[5] == '' or redis.call('GET', KEYS[7]) ~= ARGV[5] then return redis.error_reply('LIKE_RECOVERY_FENCE_LOST') end

local count = tonumber(ARGV[1])
local version = tonumber(ARGV[2])
if not count or count ~= 0 or not version or version ~= 0 then
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end

local ready = redis.call('GET', KEYS[1])
if ready == '1' then
  local count_raw = redis.call('GET', KEYS[2])
  local version_raw = redis.call('GET', KEYS[3])
  local count = count_raw and tonumber(count_raw)
  local version = version_raw and tonumber(version_raw)
  if count and count >= 0 and version and version >= 0 then
    redis.call('DEL', KEYS[7])
    return 0
  end
end

if redis.call('EXISTS', KEYS[1]) == 1 or redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 or
   redis.call('SISMEMBER', KEYS[4], ARGV[3]) == 1 or
   redis.call('HEXISTS', KEYS[6], ARGV[3]) == 1 then
  return redis.error_reply('LIKE_RECOVERY_UNSAFE')
end

redis.call('SET', KEYS[2], ARGV[1])
redis.call('SET', KEYS[3], ARGV[2])
redis.call('SADD', KEYS[4], ARGV[3])
redis.call('ZADD', KEYS[5], ARGV[4], ARGV[3])
redis.call('HDEL', KEYS[6], ARGV[3])
redis.call('SET', KEYS[1], '1')
redis.call('DEL', KEYS[7])
return 1
`)

var purgePostScript = redis.NewScript(`
-- Revoke stale rebuilds even when malformed live state prevents cleanup.
redis.call('DEL', KEYS[10])
local function type_matches(key, expected)
  local actual = redis.call('TYPE', key).ok
  return actual == 'none' or actual == expected
end

if not type_matches(KEYS[1], 'string') or
   not type_matches(KEYS[2], 'string') or
   not type_matches(KEYS[3], 'string') or
   not type_matches(KEYS[4], 'set') or
   not type_matches(KEYS[5], 'zset') or
   not type_matches(KEYS[6], 'hash') or
   not type_matches(KEYS[7], 'set') or
   not type_matches(KEYS[8], 'zset') or
   not type_matches(KEYS[9], 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end

if redis.call('GET', KEYS[1]) ~= 'deleted' then redis.call('DEL', KEYS[1]) end
redis.call('DEL', KEYS[2], KEYS[3])
redis.call('SREM', KEYS[4], ARGV[1])
redis.call('ZREM', KEYS[5], ARGV[1])
redis.call('HDEL', KEYS[6], ARGV[1])
redis.call('SREM', KEYS[7], ARGV[1])
redis.call('ZREM', KEYS[8], ARGV[1])
redis.call('HDEL', KEYS[9], ARGV[1])
if redis.call('GET', KEYS[1]) == 'deleted' and tonumber(ARGV[2]) > 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
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
