local activeSetKey, pausedSetKey = KEYS[1], KEYS[2]
local ownerID = ARGV[1]

local isPaused = redis.call("ZSCORE", pausedSetKey, ownerID) ~= false
local setKey = activeSetKey
if isPaused then
    setKey = pausedSetKey
end

-- decrement our workers for this task owner
local active = tonumber(redis.call("ZINCRBY", setKey, -1, ownerID))

-- reset to zero if we somehow go below
if active < 0 then
    redis.call("ZADD", setKey, 0, ownerID)
end
