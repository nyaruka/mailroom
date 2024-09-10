local activeSetKey, pausedSetKey, queueKey = KEYS[1], KEYS[2], KEYS[3]
local ownerID, payload, score = ARGV[1], ARGV[2], ARGV[3]

redis.call("ZADD", queueKey, score, payload)

local isPaused = redis.call("ZSCORE", pausedSetKey, ownerID) ~= false
local setKey = activeSetKey
if isPaused then
    setKey = pausedSetKey
end

redis.call("ZINCRBY", setKey, 0, ownerID)
