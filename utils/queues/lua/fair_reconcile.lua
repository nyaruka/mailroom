local activeKey = KEYS[1]
local leasesKey = KEYS[2]

-- every active slot should be held by a leased task, so count leases per owner
local leased = {}
for _, owner in ipairs(redis.call("HVALS", leasesKey)) do
    leased[owner] = (leased[owner] or 0) + 1
end

-- correct any owners whose active counts differ, returning owner, old count, new count for each
local changes = {}
local active = redis.call("ZRANGE", activeKey, 0, -1, "WITHSCORES")
for i = 1, #active, 2 do
    local owner = active[i]
    local count = tonumber(active[i + 1])
    local expected = leased[owner] or 0

    if count ~= expected then
        if expected > 0 then
            redis.call("ZADD", activeKey, expected, owner)
        else
            redis.call("ZREM", activeKey, owner)
        end
        table.insert(changes, owner)
        table.insert(changes, count)
        table.insert(changes, expected)
    end
    leased[owner] = nil
end

-- and any owners with leases but no active count
for owner, expected in pairs(leased) do
    redis.call("ZADD", activeKey, expected, owner)
    table.insert(changes, owner)
    table.insert(changes, 0)
    table.insert(changes, expected)
end

return changes
