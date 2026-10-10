package likes

import "github.com/go-redis/redis/v7"

var readUserLikeMembersScript = redis.NewScript(`
if #ARGV > 100 then return redis.error_reply('LIKE_USER_READ_BATCH_LIMIT') end
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if actual == 'none' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_NOT_READY')
end
local order_type = redis.call('TYPE', KEYS[2]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local active = redis.call('SCARD', KEYS[1]) - 1
if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_type == 'zset' and redis.call('ZCARD', KEYS[2]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local result = {}
for i = 1, #ARGV do
  local in_set = redis.call('SISMEMBER', KEYS[1], ARGV[i]) == 1
  local in_order = redis.call('ZSCORE', KEYS[2], ARGV[i]) ~= false
  if in_set ~= in_order then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  result[i] = in_set and 1 or 0
end
return result
`)

var inspectUserLikeStateScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local lock_type = redis.call('TYPE', KEYS[3]).ok
if lock_type ~= 'none' and lock_type ~= 'string' then return redis.error_reply('LIKE_USER_RESTORE_LOCK_TYPE') end
local order_type = redis.call('TYPE', KEYS[4]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local active = redis.call('SCARD', KEYS[1]) - 1
  if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
  if (order_type == 'zset' and redis.call('ZCARD', KEYS[4]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  if active > 0 and math.abs(redis.call('PTTL', KEYS[1]) - redis.call('PTTL', KEYS[4])) > 1 then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  local ledger = redis.call('HGET', KEYS[2], ARGV[1]) or ''
  return {'ready', ledger, tostring(redis.call('PTTL', KEYS[1]))}
end
if order_type ~= 'none' then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local expiry = redis.call('HGET', KEYS[2], ARGV[1])
if not expiry then return {'unexpected_missing', '', '-2'} end
if not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then
  return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE')
end
local server_time = redis.call('TIME')
local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
if lock_type == 'string' then return {'busy', expiry, '-2'} end
return {'cold', expiry, '-2'}
`)

var beginUserLikeRestoreScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local lock_type = redis.call('TYPE', KEYS[3]).ok
if lock_type ~= 'none' and lock_type ~= 'string' then return redis.error_reply('LIKE_USER_RESTORE_LOCK_TYPE') end
local order_type = redis.call('TYPE', KEYS[4]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local active = redis.call('SCARD', KEYS[1]) - 1
  if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
  if (order_type == 'zset' and redis.call('ZCARD', KEYS[4]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  return {'ready', ''}
end
if order_type ~= 'none' then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local expiry = redis.call('HGET', KEYS[2], ARGV[1])
if not expiry or not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then
  return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE')
end
local server_time = redis.call('TIME')
local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
if lock_type == 'string' then return {'busy', expiry} end
if not redis.call('SET', KEYS[3], ARGV[2], 'PX', ARGV[3], 'NX') then return {'busy', expiry} end
return {'acquired', expiry}
`)

var createUserLikeRestoreTempScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' then return redis.error_reply('LIKE_USER_RESTORE_TARGET_EXISTS') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local lock_type = redis.call('TYPE', KEYS[3]).ok
if lock_type ~= 'string' or redis.call('GET', KEYS[3]) ~= ARGV[2] then
  return redis.error_reply('LIKE_USER_RECOVERY_LOCK_LOST')
end
local expiry = redis.call('HGET', KEYS[2], ARGV[1])
if expiry ~= ARGV[3] or not string.match(expiry or '', '^%d+$') or tonumber(expiry) <= 0 then
  return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE')
end
local server_time = redis.call('TIME')
local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
local temp_type = redis.call('TYPE', KEYS[4]).ok
if temp_type ~= 'none' then return redis.error_reply('LIKE_USER_RESTORE_TEMP_EXISTS') end
if redis.call('EXISTS', KEYS[5]) ~= 0 then return redis.error_reply('LIKE_USER_RESTORE_TARGET_EXISTS') end
if redis.call('EXISTS', KEYS[6]) ~= 0 then return redis.error_reply('LIKE_USER_RESTORE_TEMP_EXISTS') end
redis.call('SADD', KEYS[4], '0')
redis.call('PEXPIRE', KEYS[4], ARGV[4])
return 1
`)

var appendUserLikeRestoreTempScript = redis.NewScript(`
local set_type = redis.call('TYPE', KEYS[1]).ok
if set_type ~= 'set' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
local temp_ttl = redis.call('PTTL', KEYS[1])
if temp_ttl <= 0 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
local order_type = redis.call('TYPE', KEYS[2]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if #ARGV % 2 ~= 0 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
for i = 1, #ARGV, 2 do
  local post_id = ARGV[i]
  local score = tonumber(ARGV[i + 1])
  if not string.match(post_id, '^%d+$') or post_id == '0' or not score or score <= 0 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
end
for i = 1, #ARGV, 2 do
  redis.call('SADD', KEYS[1], ARGV[i])
  redis.call('ZADD', KEYS[2], ARGV[i + 1], ARGV[i])
end
redis.call('PEXPIRE', KEYS[2], temp_ttl)
return #ARGV / 2
`)

var finishUserLikeRestoreScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local order_type = redis.call('TYPE', KEYS[5]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local active = redis.call('SCARD', KEYS[1]) - 1
  if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
  if (order_type == 'zset' and redis.call('ZCARD', KEYS[5]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  local lock_type = redis.call('TYPE', KEYS[3]).ok
  if lock_type == 'string' and redis.call('GET', KEYS[3]) == ARGV[2] then redis.call('DEL', KEYS[3]) end
  return {'ready', ''}
end
if order_type ~= 'none' then return redis.error_reply('LIKE_USER_RESTORE_TARGET_EXISTS') end
if redis.call('EXISTS', KEYS[1]) ~= 0 then return redis.error_reply('LIKE_USER_RESTORE_TARGET_EXISTS') end
if ARGV[8] ~= '1' then return redis.error_reply('LIKE_USER_RECOVERY_DISABLED') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
if redis.call('HGET', KEYS[2], ARGV[1]) ~= ARGV[3] then return redis.error_reply('LIKE_USER_RECOVERY_LOCK_LOST') end
local lock_type = redis.call('TYPE', KEYS[3]).ok
if lock_type ~= 'string' or redis.call('GET', KEYS[3]) ~= ARGV[2] then
  return redis.error_reply('LIKE_USER_RECOVERY_LOCK_LOST')
end
local temp_type = redis.call('TYPE', KEYS[4]).ok
if temp_type ~= 'set' or redis.call('SISMEMBER', KEYS[4], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE')
end
local temp_order_type = redis.call('TYPE', KEYS[6]).ok
if temp_order_type ~= 'none' and temp_order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local relation_count = tonumber(ARGV[4])
local max_relations = tonumber(ARGV[5])
if not relation_count or relation_count < 0 then
  return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE')
end
if relation_count > max_relations then return redis.error_reply('LIKE_USER_RECOVERY_TOO_LARGE') end
if redis.call('SCARD', KEYS[4]) ~= relation_count + 1 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
if (relation_count == 0 and temp_order_type ~= 'none') or
   (relation_count > 0 and (temp_order_type ~= 'zset' or redis.call('ZCARD', KEYS[6]) ~= relation_count)) then
  return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE')
end
local members = redis.call('SMEMBERS', KEYS[4])
for _, post_id in ipairs(members) do
  if post_id ~= '0' then
    if not string.match(post_id, '^%d+$') then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
    if redis.call('ZSCORE', KEYS[6], post_id) == false then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
    local ready_key = 'post:like:' .. post_id .. ':ready'
    local ready_type = redis.call('TYPE', ready_key).ok
    if ready_type ~= 'none' and ready_type ~= 'string' then return redis.error_reply('LIKE_USER_RESTORE_POST_STATE') end
    if redis.call('GET', ready_key) == 'deleted' then return redis.error_reply('LIKE_USER_RECOVERY_POST_DELETED') end
  end
end
local expires_at = 0
if ARGV[6] == '1' then
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  expires_at = now_ms + tonumber(ARGV[7])
  redis.call('PEXPIREAT', KEYS[4], expires_at)
  if relation_count > 0 then redis.call('PEXPIREAT', KEYS[6], expires_at) end
  redis.call('HSET', KEYS[2], ARGV[1], tostring(expires_at))
else
  redis.call('PERSIST', KEYS[4])
  redis.call('PERSIST', KEYS[6])
  redis.call('HDEL', KEYS[2], ARGV[1])
end
redis.call('RENAME', KEYS[4], KEYS[1])
if relation_count > 0 then redis.call('RENAME', KEYS[6], KEYS[5]) end
redis.call('DEL', KEYS[3])
return {'installed', tostring(expires_at)}
`)

var releaseUserLikeRestoreLockScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok == 'string' and redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var migrateUserLikeTTLScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
local user_id = ARGV[1]
if user_type == 'none' then
  if order_type ~= 'none' then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  local expiry = redis.call('HGET', KEYS[2], user_id)
  if not expiry then return {'missing', ''} end
  if not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  return {'cold', expiry}
end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
local active = redis.call('SCARD', KEYS[1]) - 1
if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_type == 'zset' and redis.call('ZCARD', KEYS[3]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
local server_time = redis.call('TIME')
local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
local expires_at = now_ms + tonumber(ARGV[2])
local prior = redis.call('HGET', KEYS[2], user_id)
if prior then
  if not string.match(prior, '^%d+$') or tonumber(prior) <= 0 then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  if tonumber(prior) > expires_at then expires_at = tonumber(prior) end
end
local pttl = redis.call('PTTL', KEYS[1])
if pttl > 0 then
  local current_expiry = now_ms + pttl
  if current_expiry > expires_at then expires_at = current_expiry end
end
if active > 0 then
  local order_pttl = redis.call('PTTL', KEYS[3])
  if math.abs(pttl - order_pttl) > 1 then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  if order_pttl > 0 then
    local current_expiry = now_ms + order_pttl
    if current_expiry > expires_at then expires_at = current_expiry end
  end
end
redis.call('PEXPIREAT', KEYS[1], expires_at)
if active > 0 then redis.call('PEXPIREAT', KEYS[3], expires_at) end
redis.call('HSET', KEYS[2], user_id, tostring(expires_at))
return {'armed', tostring(expires_at)}
`)

var rollbackUserLikeTTLScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if user_type == 'none' then
  if order_type ~= 'none' then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
  local expiry = redis.call('HGET', KEYS[2], ARGV[1])
  if not expiry then return {'missing', ''} end
  if not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  return {'cold', expiry}
end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
local active = redis.call('SCARD', KEYS[1]) - 1
if active > 0 and order_type == 'none' then return redis.error_reply('LIKE_USER_ORDER_MISSING') end
if (order_type == 'zset' and redis.call('ZCARD', KEYS[3]) ~= active) or (active == 0 and order_type == 'zset') then return redis.error_reply('LIKE_USER_ORDER_INCONSISTENT') end
redis.call('PERSIST', KEYS[1])
redis.call('PERSIST', KEYS[3])
redis.call('HDEL', KEYS[2], ARGV[1])
return {'persisted', ''}
`)

var cleanupOrphanUserLikeLedgerScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' and order_type ~= 'zset' then return redis.error_reply('LIKE_USER_ORDER_TYPE') end
if user_type ~= 'none' or order_type ~= 'none' then return redis.error_reply('LIKE_USER_ORPHAN_SET_PRESENT') end
if redis.call('HGET', KEYS[2], ARGV[1]) ~= ARGV[2] then return redis.error_reply('LIKE_USER_RECOVERY_LOCK_LOST') end
return redis.call('HDEL', KEYS[2], ARGV[1])
`)

var cleanupUncommittedUserLikeInitScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
local order_type = redis.call('TYPE', KEYS[3]).ok
if order_type ~= 'none' then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
if user_type == 'none' then return 0 end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 or redis.call('SCARD', KEYS[1]) ~= 1 then
  return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE')
end
redis.call('DEL', KEYS[1])
if ledger_type == 'hash' then redis.call('HDEL', KEYS[2], ARGV[1]) end
return 1
`)
