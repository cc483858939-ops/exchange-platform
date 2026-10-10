package likes

import "github.com/go-redis/redis/v7"

var readUserLikeMembersScript = redis.NewScript(`
if #ARGV > 100 then return redis.error_reply('LIKE_USER_READ_BATCH_LIMIT') end
local actual = redis.call('TYPE', KEYS[1]).ok
if actual ~= 'none' and actual ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if actual == 'none' or redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then
  return redis.error_reply('LIKE_USER_NOT_READY')
end
local result = {}
for i = 1, #ARGV do
  result[i] = redis.call('SISMEMBER', KEYS[1], ARGV[i])
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
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local ledger = redis.call('HGET', KEYS[2], ARGV[1]) or ''
  return {'ready', ledger, tostring(redis.call('PTTL', KEYS[1]))}
end
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
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  return {'ready', ''}
end
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
redis.call('SADD', KEYS[4], '0')
redis.call('PEXPIRE', KEYS[4], ARGV[4])
return 1
`)

var finishUserLikeRestoreScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
if user_type == 'set' then
  if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
  local lock_type = redis.call('TYPE', KEYS[3]).ok
  if lock_type == 'string' and redis.call('GET', KEYS[3]) == ARGV[2] then redis.call('DEL', KEYS[3]) end
  return {'ready', ''}
end
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
local relation_count = tonumber(ARGV[4])
local max_relations = tonumber(ARGV[5])
if not relation_count or relation_count < 0 then
  return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE')
end
if relation_count > max_relations then return redis.error_reply('LIKE_USER_RECOVERY_TOO_LARGE') end
if redis.call('SCARD', KEYS[4]) ~= relation_count + 1 then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
local members = redis.call('SMEMBERS', KEYS[4])
for _, post_id in ipairs(members) do
  if post_id ~= '0' then
    if not string.match(post_id, '^%d+$') then return redis.error_reply('LIKE_USER_RESTORE_INCOMPLETE') end
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
  redis.call('HSET', KEYS[2], ARGV[1], tostring(expires_at))
else
  redis.call('PERSIST', KEYS[4])
  redis.call('HDEL', KEYS[2], ARGV[1])
end
redis.call('RENAME', KEYS[4], KEYS[1])
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
local user_id = ARGV[1]
if user_type == 'none' then
  local expiry = redis.call('HGET', KEYS[2], user_id)
  if not expiry then return {'missing', ''} end
  if not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  return {'cold', expiry}
end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
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
redis.call('PEXPIREAT', KEYS[1], expires_at)
redis.call('HSET', KEYS[2], user_id, tostring(expires_at))
return {'armed', tostring(expires_at)}
`)

var rollbackUserLikeTTLScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
if user_type == 'none' then
  local expiry = redis.call('HGET', KEYS[2], ARGV[1])
  if not expiry then return {'missing', ''} end
  if not string.match(expiry, '^%d+$') or tonumber(expiry) <= 0 then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  local server_time = redis.call('TIME')
  local now_ms = tonumber(server_time[1]) * 1000 + math.floor(tonumber(server_time[2]) / 1000)
  if now_ms < tonumber(expiry) then return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE') end
  return {'cold', expiry}
end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 then return redis.error_reply('LIKE_USER_NOT_READY') end
redis.call('PERSIST', KEYS[1])
redis.call('HDEL', KEYS[2], ARGV[1])
return {'persisted', ''}
`)

var cleanupOrphanUserLikeLedgerScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
if user_type ~= 'none' then return redis.error_reply('LIKE_USER_ORPHAN_SET_PRESENT') end
if redis.call('HGET', KEYS[2], ARGV[1]) ~= ARGV[2] then return redis.error_reply('LIKE_USER_RECOVERY_LOCK_LOST') end
return redis.call('HDEL', KEYS[2], ARGV[1])
`)

var cleanupUncommittedUserLikeInitScript = redis.NewScript(`
local user_type = redis.call('TYPE', KEYS[1]).ok
if user_type ~= 'none' and user_type ~= 'set' then return redis.error_reply('LIKE_USER_TYPE') end
local ledger_type = redis.call('TYPE', KEYS[2]).ok
if ledger_type ~= 'none' and ledger_type ~= 'hash' then return redis.error_reply('LIKE_USER_LEDGER_TYPE') end
if user_type == 'none' then return 0 end
if redis.call('SISMEMBER', KEYS[1], '0') ~= 1 or redis.call('SCARD', KEYS[1]) ~= 1 then
  return redis.error_reply('LIKE_USER_RECOVERY_UNSAFE')
end
redis.call('DEL', KEYS[1])
if ledger_type == 'hash' then redis.call('HDEL', KEYS[2], ARGV[1]) end
return 1
`)
