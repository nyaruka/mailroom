local activeKey = KEYS[1]
local leasesKey = KEYS[2]
local expiresKey = KEYS[3]
local taskID = ARGV[1]

local owner = redis.call("HGET", leasesKey, taskID)
if not owner then
    -- lease already expired and the task's slot was released
    return 0
end

redis.call("HDEL", leasesKey, taskID)
redis.call("ZREM", expiresKey, taskID)

-- decrement our active task count for this owner, removing if zero (or somehow negative)
local activeCount = tonumber(redis.call("ZINCRBY", activeKey, -1, owner))
if activeCount <= 0 then
    redis.call("ZREM", activeKey, owner)
end

return 1
