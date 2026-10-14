-- Table reads keep a slot hint per instruction: reading tables of different shapes, and removed and re-added keys, must stay correct.

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
