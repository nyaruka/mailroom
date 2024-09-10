local activeSetKey, pausedSetKey = KEYS[1], KEYS[2]
local queueBase = ARGV[1]

local active = redis.call("ZRANGE", activeSetKey, 0, -1)
local paused = redis.call("ZRANGE", pausedSetKey, 0, -1)

local count = 0

for i = 1, #active do
    local result = redis.call("ZCARD", queueBase .. ":" .. active[i])
    count = count + result
end

for i = 1, #paused do
    local result = redis.call("ZCARD", queueBase .. ":" .. paused[i])
    count = count + result
end

return count