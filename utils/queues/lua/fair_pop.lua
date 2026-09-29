local queuedKey = KEYS[1]
local activeKey = KEYS[2]
local pausedKey = KEYS[3]
local tempKey = KEYS[4]
local leasesKey = KEYS[5]
local expiresKey = KEYS[6]
local keyBase = ARGV[1]
local maxActivePerOwner = ARGV[2]
local leaseMillis = tonumber(ARGV[3])
local reapLimit = tonumber(ARGV[4])

-- use the server's clock so that leases don't depend on the clocks of the hosts popping tasks
local time = redis.call("TIME")
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)

local function decrActive(owner)
    local activeCount = tonumber(redis.call("ZINCRBY", activeKey, -1, owner))
    if activeCount <= 0 then
        redis.call("ZREM", activeKey, owner)
    end
end

-- release the slots of tasks whose leases have expired, i.e. whose consumers died without marking them done or have
-- run for longer than the lease
local reaped = {}
for _, taskID in ipairs(redis.call("ZRANGEBYSCORE", expiresKey, "-inf", now, "LIMIT", 0, reapLimit)) do
    local owner = redis.call("HGET", leasesKey, taskID)
    if owner then
        redis.call("HDEL", leasesKey, taskID)
        decrActive(owner)
        table.insert(reaped, taskID)
        table.insert(reaped, owner)
    end
    redis.call("ZREM", expiresKey, taskID)
end

-- create a new set which is union of queued and active owners, with scores from active
redis.call("ZUNIONSTORE", tempKey, 2, queuedKey, activeKey, "WEIGHTS", 0, 1)

-- intersect with queued owners again to remove any active owners that have no queued tasks
redis.call("ZINTERSTORE", tempKey, 2, tempKey, queuedKey, "WEIGHTS", 1, 0)

-- substract paused owners from this set
redis.call("ZDIFFSTORE", tempKey, 2, tempKey, pausedKey)

-- never leave anything without an expiry...
redis.call("EXPIRE", tempKey, 60)

-- get the owner with the least active tasks
local result = redis.call("ZRANGEBYSCORE", tempKey, "-inf", "(" .. maxActivePerOwner, "LIMIT", 0, 1)
local owner = result[1]
if not owner then
    return {"none", "", "", "", reaped}
end

-- owner queues share our hash tag so are safe to construct here even in cluster mode
local queue0Key = "{" .. keyBase .. "}:o:" .. owner .. "/0"
local queue1Key = "{" .. keyBase .. "}:o:" .. owner .. "/1"

-- pop off their queues (priority first)
local payload = redis.call("LPOP", queue1Key)
if not payload then
    payload = redis.call("LPOP", queue0Key)
end

-- set their queued score from their actual queue sizes
local size = redis.call("LLEN", queue0Key) + redis.call("LLEN", queue1Key)
if size > 0 then
    redis.call("ZADD", queuedKey, size, owner)
else
    redis.call("ZREM", queuedKey, owner)
end

if not payload then
    -- owner had no queued tasks after all.. caller should try again
    return {"retry", "", owner, "", reaped}
end

local sep = string.find(payload, "|", 1, true)
if not sep then
    return {"invalid", "", owner, payload, reaped}
end

local taskID = string.sub(payload, 1, sep - 1)

-- task holds a slot for its owner until it's marked done or its lease expires
redis.call("ZINCRBY", activeKey, 1, owner)
redis.call("HSET", leasesKey, taskID, owner)
redis.call("ZADD", expiresKey, now + leaseMillis, taskID)

return {"task", taskID, owner, string.sub(payload, sep + 1), reaped}
