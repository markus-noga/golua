-- Metamethods are cached in their metatable and field reads keep a slot hint per instruction:
-- changing a metatable, removing a field and reading tables of different shapes must stay correct.

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
--> =false	luatest:28: attempt to perform arithmetic on a table value
am.__add = function() return "one" end
print(a + 1)
--> =one
am.__add = function() return "two" end
print(a + 1)
--> =two

-- one place in the code reads tables of different shapes, then a removed key and a re-added one
local function get(t) return t.k end
local shapes = {{k = 1}, {a = 0, k = 2}, {a = 0, b = 0, c = 0, k = 3}, {k = 4, a = 0}, {}, {z = 1}}
for _, t in ipairs(shapes) do print(get(t)) end
--> =1
--> =2
--> =3
--> =4
--> =nil
--> =nil
local t = {k = "first", other = 1}
print(get(t), get(t))
--> =first	first
t.k = nil
print(get(t))
--> =nil
t.k = "again"
print(get(t))
--> =again

-- many reads in a loop with the shape changing under the hint
local sum = 0
for i = 1, 200 do
    local r = (i % 3 == 0) and {k = i} or ((i % 3 == 1) and {x = 1, y = 2, k = i} or {k = i, x = 1})
    sum = sum + get(r)
end
print(sum)
--> =20100
