-- Metamethod calls reuse one termination per nesting depth: nested calls, sequential calls,
-- errors inside a metamethod and deep nesting must give the right results and messages.

local mt = {}
mt.__add = function(a, b) return a.v + b.v end
mt.__index = function(t, k) return k .. "!" end
local function new(v) return setmetatable({v = v}, mt) end

-- sequential calls at one depth
local x, y = new(1), new(2)
print(x + y, x + x, y + y, x.foo, y.bar)
--> =3	2	4	foo!	bar!

-- nested: the metamethod does arithmetic and indexing of its own
local nmt = {}
nmt.__add = function(a, b) return (new(a.v) + new(b.v)) * 10 + #(new(1).abc) end
nmt.__len = function(a) return a.v end
local p, q = setmetatable({v = 1}, nmt), setmetatable({v = 2}, nmt)
print(p + q, #p, #q)
--> =34	1	2

-- an error in a metamethod keeps its message, and later calls are unaffected
local emt = {__add = function(a, b) error("boom") end}
local ok, err = pcall(function() return setmetatable({}, emt) + 1 end)
print(ok, err)
--> =false	luatest:23: boom
print(x + y)
--> =3

-- deeper than any preallocated slot would be: every level adds in a metamethod of the next
local depth = 60
local dmt = {}
dmt.__add = function(a, b)
    if a.n == 0 then return 0 end
    return 1 + (setmetatable({n = a.n - 1}, dmt) + b)
end
print(setmetatable({n = depth}, dmt) + 0)
--> =60
