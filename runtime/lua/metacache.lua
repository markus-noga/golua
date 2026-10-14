-- Metamethods are cached in their metatable: adding, changing and removing one after it was used must stay correct.

-- a metamethod that is added, changed and removed after it was used
local mt = {}
local obj = setmetatable({}, mt)
print(obj.x)
--> =nil
mt.__index = function(t, k) return "first " .. k end
print(obj.x)
--> =first x
mt.__index = function(t, k) return "second " .. k end
print(obj.x)
--> =second x
mt.__index = nil
print(obj.x)
--> =nil
mt.__index = {x = "from table"}
print(obj.x)
--> =from table
rawset(mt, "__index", {x = "rawset"})
print(obj.x)
--> =rawset

-- an arithmetic metamethod, defined later, then replaced
local am = {}
local a = setmetatable({}, am)
print(pcall(function() return a + 1 end))
--> =false	luatest:27: attempt to perform arithmetic on a table value
am.__add = function() return "one" end
print(a + 1)
--> =one
am.__add = function() return "two" end
print(a + 1)
--> =two
