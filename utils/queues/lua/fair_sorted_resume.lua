local activeSetKey, pausedSetKey = KEYS[1], KEYS[2]
local ownerID = ARGV[1]

local score = redis.call("ZSCORE", pausedSetKey, ownerID)
if score ~= false then
    redis.call("ZREM", pausedSetKey, ownerID)
else
    score = 0
end

redis.call("ZINCRBY", activeSetKey, score, ownerID)
