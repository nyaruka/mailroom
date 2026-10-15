local leasesKey = KEYS[1]
local expiresKey = KEYS[2]
local taskID = ARGV[1]
local leaseMillis = tonumber(ARGV[2])

if redis.call("HEXISTS", leasesKey, taskID) == 0 then
    -- lease already expired and the task's slot was released
    return 0
end

local time = redis.call("TIME")
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)

redis.call("ZADD", expiresKey, now + leaseMillis, taskID)
return 1
