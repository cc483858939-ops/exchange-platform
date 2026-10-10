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
   not type_matches(KEYS[10], 'hash') or
   not type_matches(KEYS[11], 'hash') or
   not type_matches(KEYS[12], 'zset') then
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
local active_relations = redis.call('SCARD', KEYS[4]) - 1
if active_relations > tonumber(ARGV[8]) then return redis.error_reply('LIKE_USER_OVER_CAP') end
local order_exists = redis.call('EXISTS', KEYS[12]) == 1
if active_relations > 0 and not order_exists then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_exists and redis.call('ZCARD', KEYS[12]) ~= active_relations) or (active_relations == 0 and order_exists) then
  return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT')
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
local ordered_current = redis.call('ZSCORE', KEYS[12], post_id)
if (current == 1) ~= (ordered_current ~= false) then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local desired = ARGV[3] == '1' and 1 or 0
local changed = (desired == 1 and current == 0) or (desired == 0 and current == 1)
local candidate_id = ''
local candidate_ready = nil
local candidate_count = nil
local candidate_version = nil
local stale_candidate = false
local will_evict = desired == 1 and current == 0 and active_relations >= tonumber(ARGV[8])
if will_evict then
  local candidate = redis.call('ZRANGE', KEYS[12], 0, 0)
  if #candidate ~= 1 then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  candidate_id = candidate[1]
  if not string.match(candidate_id, '^%d+$') or candidate_id == '0' then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  if redis.call('SISMEMBER', KEYS[4], candidate_id) ~= 1 then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  local candidate_ready_key = 'post:like:' .. candidate_id .. ':ready'
  local candidate_ready_type = redis.call('TYPE', candidate_ready_key).ok
  if candidate_ready_type ~= 'none' and candidate_ready_type ~= 'string' then return redis.error_reply('LIKE_USER_EVICTION_POST_TYPE') end
  candidate_ready = redis.call('GET', candidate_ready_key)
  if candidate_ready == 'deleted' then
    stale_candidate = true
  elseif candidate_ready ~= '1' then
    return redis.error_reply('LIKE_USER_EVICTION_POST_NOT_READY')
  else
    local candidate_count_key = 'post:like:' .. candidate_id .. ':count'
    local candidate_version_key = 'post:like:' .. candidate_id .. ':version'
    local candidate_count_type = redis.call('TYPE', candidate_count_key).ok
    local candidate_version_type = redis.call('TYPE', candidate_version_key).ok
    if (candidate_count_type ~= 'none' and candidate_count_type ~= 'string') or
       (candidate_version_type ~= 'none' and candidate_version_type ~= 'string') then
      return redis.error_reply('LIKE_USER_EVICTION_POST_TYPE')
    end
    candidate_count = redis.call('GET', candidate_count_key)
    candidate_version = redis.call('GET', candidate_version_key)
    if not candidate_count or not candidate_version or not string.match(candidate_count, '^%d+$') or not string.match(candidate_version, '^%d+$') then
      return redis.error_reply('LIKE_USER_EVICTION_POST_NOT_READY')
    end
    candidate_count = tonumber(candidate_count)
    candidate_version = tonumber(candidate_version)
    if not candidate_count or candidate_count <= 0 or not candidate_version or candidate_version < 0 then
      return redis.error_reply('LIKE_USER_EVICTION_COUNT_INCONSISTENT')
    end
  end
end
if changed then
  if desired == 0 and count == 0 then
    return redis.error_reply('LIKE_COUNT_INCONSISTENT')
  end
  if desired == 0 and current == 1 and not ordered_current then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end

  local server_time = redis.call('TIME')
  local order_score = tonumber(server_time[1]) * 1000000 + tonumber(server_time[2])

  if will_evict then
    redis.call('SREM', KEYS[4], candidate_id)
    redis.call('ZREM', KEYS[12], candidate_id)
    active_relations = active_relations - 1
    if not stale_candidate then
      local next_count = candidate_count - 1
      local next_version = candidate_version + 1
      redis.call('PERSIST', 'post:like:' .. candidate_id .. ':ready')
      redis.call('PERSIST', 'post:like:' .. candidate_id .. ':count')
      redis.call('PERSIST', 'post:like:' .. candidate_id .. ':version')
      redis.call('HDEL', KEYS[10], candidate_id)
      redis.call('SADD', KEYS[8], candidate_id)
      redis.call('SET', 'post:like:' .. candidate_id .. ':count', next_count)
      redis.call('SET', 'post:like:' .. candidate_id .. ':version', next_version)
      redis.call('SADD', KEYS[5], candidate_id)
      local candidate_pair = user_id .. ':' .. candidate_id
      redis.call('HSET', KEYS[7], candidate_pair, '0|' .. next_version .. '|' .. ARGV[4])
      redis.call('SADD', KEYS[6], candidate_pair)
      redis.call('ZADD', KEYS[9], ARGV[5], candidate_id)
      candidate_count = next_count
      candidate_version = next_version
    end
  end

  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[2])
  redis.call('PERSIST', KEYS[3])
  redis.call('HDEL', KEYS[10], post_id)
  redis.call('SADD', KEYS[8], post_id)

  if desired == 1 then
    redis.call('SADD', KEYS[4], post_id)
    redis.call('ZADD', KEYS[12], order_score, post_id)
    count = count + 1
  else
    redis.call('SREM', KEYS[4], post_id)
    redis.call('ZREM', KEYS[12], post_id)
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
  active_relations = active_relations + (desired == 1 and 1 or -1)
  current = desired
end
local ttl_state = 0
if ARGV[7] == '1' then
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  local expires_at = now_ms + tonumber(ARGV[6])
  redis.call('PEXPIREAT', KEYS[4], expires_at)
  if active_relations > 0 then redis.call('PEXPIREAT', KEYS[12], expires_at) end
  redis.call('HSET', KEYS[11], user_id, tostring(expires_at))
  ttl_state = 1
else
  redis.call('PERSIST', KEYS[4])
  redis.call('PERSIST', KEYS[12])
  redis.call('HDEL', KEYS[11], user_id)
end
return {count, current, changed, version, ttl_state, candidate_id, active_relations, will_evict and 1 or 0, stale_candidate and 1 or 0}
`)

var scanUserLikesScript = redis.NewScript(`
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if actual == 'none' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_NOT_READY')
end
local order_type = redis.call('TYPE', KEYS[2]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local active = redis.call('SCARD', KEYS[1]) - 1
if (active > 0 and order_type == 'none') then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_type == 'zset' and redis.call('ZCARD', KEYS[2]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local result = redis.call('SSCAN', KEYS[1], ARGV[1], 'COUNT', ARGV[2])
local values = {result[1]}
for _, member in ipairs(result[2]) do
  if member ~= '0' and redis.call('ZSCORE', KEYS[2], member) == false then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  table.insert(values, member)
end
return values
`)

var initializeUserEmptyScript = redis.NewScript(`
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then
  return redis.error_reply('LIKE_USER_TYPE')
end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local user_id = ARGV[1]
if actual == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local active = redis.call('SCARD', KEYS[1]) - 1
  if (active > 0 and order_type == 'none') then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
  if (order_type == 'zset' and redis.call('ZCARD', KEYS[3]) ~= active) or (active == 0 and order_type == 'zset') then
    return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT')
  end
else
  if order_type ~= 'none' or redis.call('HEXISTS', KEYS[2], user_id) == 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  redis.call('SADD', KEYS[1], '0')
end
if ARGV[2] == '1' then
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  local expires_at = now_ms + tonumber(ARGV[3])
  redis.call('PEXPIREAT', KEYS[1], expires_at)
  if order_type == 'zset' then redis.call('PEXPIREAT', KEYS[3], expires_at) end
  redis.call('HSET', KEYS[2], user_id, tostring(expires_at))
else
  redis.call('PERSIST', KEYS[1])
  redis.call('PERSIST', KEYS[3])
  redis.call('HDEL', KEYS[2], user_id)
end
if actual == 'none' then return 1 end
return 0
`)

var removeDeletedUserPostRelationsScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if user_type == 'none' then return redis.error_reply('LIKE_USER_NOT_READY') end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
local order_type = redis.call('TYPE', KEYS[2]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local active = redis.call('SCARD', KEYS[1]) - 1
if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_type == 'zset' and redis.call('ZCARD', KEYS[2]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local removed = 0
local result = {0}
local removals = {}
for i = 1, #ARGV do
  local post_id = ARGV[i]
  if post_id ~= '0' then
    local in_set = redis.call('SISMEMBER', KEYS[1], post_id) == 1
    local in_order = redis.call('ZSCORE', KEYS[2], post_id) ~= false
    if in_set ~= in_order then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
    local ready_type = redis.call('TYPE', KEYS[i + 2]).ok
    if ready_type ~= 'none' and ready_type ~= 'string' then
      table.insert(result, post_id)
      table.insert(result, 'post_ready_type_error')
    else
      local ready = redis.call('GET', KEYS[i + 2])
      if not ready or ready == 'deleted' then
        if in_set then table.insert(removals, post_id) end
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
for _, post_id in ipairs(removals) do
  removed = removed + redis.call('SREM', KEYS[1], post_id)
  redis.call('ZREM', KEYS[2], post_id)
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
local result = {}
local limit = tonumber(ARGV[1])
if not limit or limit ~= math.floor(limit) or limit < 1 or limit > 100 or
   not tonumber(ARGV[2]) or ARGV[3] == '' then
  return redis.error_reply('LIKE_CLAIM_ARGUMENT_INVALID')
end
if (redis.call('TYPE', KEYS[1]).ok ~= 'none' and redis.call('TYPE', KEYS[1]).ok ~= 'set') or
   (redis.call('TYPE', KEYS[2]).ok ~= 'none' and redis.call('TYPE', KEYS[2]).ok ~= 'zset') or
   (redis.call('TYPE', KEYS[3]).ok ~= 'none' and redis.call('TYPE', KEYS[3]).ok ~= 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
-- Random sampling is bounded. Occupied IDs remain Dirty for a later claim.
local candidates = redis.call('SRANDMEMBER', KEYS[1], limit * 4)
for _, post_id in ipairs(candidates) do
  if #result >= limit * 2 then break end
  if redis.call('HEXISTS', KEYS[3], post_id) == 0 then
    local claim_id = ARGV[3] .. ':' .. post_id
    redis.call('ZADD', KEYS[2], ARGV[2], post_id)
    redis.call('HSET', KEYS[3], post_id, claim_id)
    redis.call('SREM', KEYS[1], post_id)
    table.insert(result, post_id)
    table.insert(result, claim_id)
  end
end
return result
`)

var ackClaimScript = redis.NewScript(`
if (redis.call('TYPE', KEYS[1]).ok ~= 'none' and redis.call('TYPE', KEYS[1]).ok ~= 'zset') or
   (redis.call('TYPE', KEYS[2]).ok ~= 'none' and redis.call('TYPE', KEYS[2]).ok ~= 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if redis.call('HGET', KEYS[2], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[2], ARGV[1])
redis.call('ZREM', KEYS[1], ARGV[1])
return 1
`)

var requeueClaimScript = redis.NewScript(`
if (redis.call('TYPE', KEYS[1]).ok ~= 'none' and redis.call('TYPE', KEYS[1]).ok ~= 'set') or
   (redis.call('TYPE', KEYS[2]).ok ~= 'none' and redis.call('TYPE', KEYS[2]).ok ~= 'zset') or
   (redis.call('TYPE', KEYS[3]).ok ~= 'none' and redis.call('TYPE', KEYS[3]).ok ~= 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
if redis.call('HGET', KEYS[3], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call('HDEL', KEYS[3], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
redis.call('SADD', KEYS[1], ARGV[1])
return 1
`)

var reapExpiredScript = redis.NewScript(`
local limit = tonumber(ARGV[2])
if not limit or limit ~= math.floor(limit) or limit < 1 or limit > 100 then
  return redis.error_reply('LIKE_CLAIM_ARGUMENT_INVALID')
end
if (redis.call('TYPE', KEYS[1]).ok ~= 'none' and redis.call('TYPE', KEYS[1]).ok ~= 'set') or
   (redis.call('TYPE', KEYS[2]).ok ~= 'none' and redis.call('TYPE', KEYS[2]).ok ~= 'zset') or
   (redis.call('TYPE', KEYS[3]).ok ~= 'none' and redis.call('TYPE', KEYS[3]).ok ~= 'hash') then
  return redis.error_reply('LIKE_TYPE_PRECHECK')
end
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
