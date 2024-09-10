local activeSetKey, pausedSetKey = KEYS[1], KEYS[2]
local ownerID = ARGV[1]

local score = redis.call("ZSCORE", activeSetKey, ownerID)
if score ~= false then
    redis.call("ZREM", activeSetKey, ownerID)
else
    score = 0
end

redis.call("ZINCRBY", pausedSetKey, score, ownerID)
