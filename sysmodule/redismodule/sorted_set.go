package redismodule

import (
	"context"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

var integerZIncrByScript = redis.NewScript(`
local limit = '9007199254740992'

local function normalize(value)
    local sign = 1
    if string.sub(value, 1, 1) == '-' then
        sign = -1
        value = string.sub(value, 2)
    elseif string.sub(value, 1, 1) == '+' then
        value = string.sub(value, 2)
    end
    if value == '' or string.find(value, '[^0-9]') then return nil end
    value = string.gsub(value, '^0+', '')
    if value == '' then return 1, '0' end
    if #value > #limit or (#value == #limit and value > limit) then return nil end
    return sign, value
end

local function compare_abs(left, right)
    if #left ~= #right then return #left < #right and -1 or 1 end
    if left == right then return 0 end
    return left < right and -1 or 1
end

local function add_abs(left, right)
    local carry = 0
    local result = ''
    local li, ri = #left, #right
    while li > 0 or ri > 0 or carry > 0 do
        local ld = li > 0 and tonumber(string.sub(left, li, li)) or 0
        local rd = ri > 0 and tonumber(string.sub(right, ri, ri)) or 0
        local sum = ld + rd + carry
        result = tostring(sum % 10) .. result
        carry = math.floor(sum / 10)
        li = li - 1
        ri = ri - 1
    end
    return result
end

local function subtract_abs(left, right)
    local borrow = 0
    local result = ''
    local li, ri = #left, #right
    while li > 0 do
        local ld = tonumber(string.sub(left, li, li)) - borrow
        local rd = ri > 0 and tonumber(string.sub(right, ri, ri)) or 0
        if ld < rd then ld = ld + 10; borrow = 1 else borrow = 0 end
        result = tostring(ld - rd) .. result
        li = li - 1
        ri = ri - 1
    end
    result = string.gsub(result, '^0+', '')
    return result == '' and '0' or result
end

local current = redis.call('ZSCORE', KEYS[1], ARGV[2]) or '0'
local current_sign, current_abs = normalize(current)
local increment_sign, increment_abs = normalize(ARGV[1])
if not current_sign or not increment_sign then
    return redis.error_reply('origin: invalid integer score')
end

local result_sign, result_abs
if current_sign == increment_sign then
    result_sign = current_sign
    result_abs = add_abs(current_abs, increment_abs)
else
    local comparison = compare_abs(current_abs, increment_abs)
    if comparison == 0 then
        result_sign, result_abs = 1, '0'
    elseif comparison > 0 then
        result_sign, result_abs = current_sign, subtract_abs(current_abs, increment_abs)
    else
        result_sign, result_abs = increment_sign, subtract_abs(increment_abs, current_abs)
    end
end

if #result_abs > #limit or (#result_abs == #limit and result_abs > limit) then
    return redis.error_reply('origin: invalid integer score')
end
local result = result_sign < 0 and result_abs ~= '0' and ('-' .. result_abs) or result_abs
redis.call('ZADD', KEYS[1], result, ARGV[2])
return result
`)

// ZAdd 添加或更新整数分数成员，返回实际新增成员数；members 不能为空。
//
// Score 必须位于 MinExactScore 到 MaxExactScore。复合排行和同分先到规则由业务层实现。
func (module *Module) ZAdd(ctx context.Context, key string, members ...ScoredMember) (int64, error) {
	values, err := redisMembers(members)
	if err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZAdd(ctx, key, values...).Result()
}

// ZAddNX 仅添加不存在的成员，返回实际新增成员数；members 不能为空。
func (module *Module) ZAddNX(ctx context.Context, key string, members ...ScoredMember) (int64, error) {
	values, err := redisMembers(members)
	if err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZAddArgs(ctx, key, redis.ZAddArgs{NX: true, Members: values}).Result()
}

// ZAddXX 仅更新已存在成员，返回 Score 实际发生变化的成员数；members 不能为空。
func (module *Module) ZAddXX(ctx context.Context, key string, members ...ScoredMember) (int64, error) {
	values, err := redisMembers(members)
	if err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZAddArgs(ctx, key, redis.ZAddArgs{XX: true, Ch: true, Members: values}).Result()
}

// ZIncrBy 原子增加 member 的整数 Score 并返回新 Score。
//
// increment 与结果必须位于精确整数范围；已有小数或越界 Score 返回 ErrInvalidScore 且不会写入。
func (module *Module) ZIncrBy(ctx context.Context, key string, increment int64, member string) (int64, error) {
	if err := validateScore(increment); err != nil {
		return 0, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	value, err := integerZIncrByScript.Run(ctx, client, []string{key}, increment, member).Result()
	if err != nil {
		if strings.Contains(err.Error(), "origin: invalid integer score") {
			return 0, ErrInvalidScore
		}
		return 0, err
	}
	text, ok := value.(string)
	if !ok {
		return 0, ErrInvalidScore
	}
	result, err := strconv.ParseInt(text, 10, 64)
	if err != nil || validateScore(result) != nil {
		return 0, ErrInvalidScore
	}
	return result, nil
}

// ZRem 删除 members 并返回实际删除成员数；members 不能为空。
func (module *Module) ZRem(ctx context.Context, key string, members ...string) (int64, error) {
	if err := requireValues(len(members), "Sorted Set Members"); err != nil {
		return 0, err
	}
	values := make([]any, len(members))
	for index, member := range members {
		values[index] = member
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZRem(ctx, key, values...).Result()
}

// ZScore 返回 member 的精确整数 Score；成员不存在返回 ErrNil。
//
// 服务端值为小数或超出精确整数范围时返回 ErrInvalidScore，不进行截断。
func (module *Module) ZScore(ctx context.Context, key, member string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	value, err := client.ZScore(ctx, key, member).Result()
	if err != nil {
		return 0, err
	}
	return exactScore(value)
}

// ZRank 返回 member 按 Score 升序的零基排名；成员不存在返回 ErrNil。
func (module *Module) ZRank(ctx context.Context, key, member string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZRank(ctx, key, member).Result()
}

// ZRevRank 返回 member 按 Score 降序的零基排名；成员不存在返回 ErrNil。
func (module *Module) ZRevRank(ctx context.Context, key, member string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZRevRank(ctx, key, member).Result()
}

// ZRange 返回升序排名区间，start/stop 均包含且可使用负索引；Key 不存在返回空切片。
//
// 调用方必须限制区间，避免一次读取未知大的排行。
func (module *Module) ZRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.ZRange(ctx, key, start, stop).Result()
}

// ZRevRange 返回降序排名区间，start/stop 均包含且可使用负索引；Key 不存在返回空切片。
func (module *Module) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.ZRevRange(ctx, key, start, stop).Result()
}

// ZRangeWithScores 返回升序排名区间及精确整数 Score；start/stop 均包含。
//
// 任一服务端 Score 为小数或越界时整体返回 ErrInvalidScore。
func (module *Module) ZRangeWithScores(ctx context.Context, key string, start, stop int64) ([]ScoredMember, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.ZRangeWithScores(ctx, key, start, stop).Result()
	if err != nil {
		return nil, err
	}
	return scoredMembers(values)
}

// ZRevRangeWithScores 返回降序排名区间及精确整数 Score；start/stop 均包含。
func (module *Module) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) ([]ScoredMember, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.ZRevRangeWithScores(ctx, key, start, stop).Result()
	if err != nil {
		return nil, err
	}
	return scoredMembers(values)
}

// ZRangeByScore 返回包含 min/max 的整数 Score 升序区间；offset 非负、count 大于 0。
func (module *Module) ZRangeByScore(ctx context.Context, key string, min, max, offset, count int64) ([]string, error) {
	return module.zRangeByScore(ctx, key, min, max, offset, count, false)
}

// ZRevRangeByScore 返回包含 min/max 的整数 Score 降序区间；offset 非负、count 大于 0。
func (module *Module) ZRevRangeByScore(ctx context.Context, key string, min, max, offset, count int64) ([]string, error) {
	return module.zRangeByScore(ctx, key, min, max, offset, count, true)
}

// ZRangeByScoreWithScores 返回包含 min/max 的升序区间及精确整数 Score。
//
// offset 必须非负、count 必须大于 0；任一小数或越界 Score 返回 ErrInvalidScore。
func (module *Module) ZRangeByScoreWithScores(ctx context.Context, key string, min, max, offset, count int64) ([]ScoredMember, error) {
	return module.zRangeByScoreWithScores(ctx, key, min, max, offset, count, false)
}

// ZRevRangeByScoreWithScores 返回包含 min/max 的降序区间及精确整数 Score。
//
// offset 必须非负、count 必须大于 0；任一小数或越界 Score 返回 ErrInvalidScore。
func (module *Module) ZRevRangeByScoreWithScores(ctx context.Context, key string, min, max, offset, count int64) ([]ScoredMember, error) {
	return module.zRangeByScoreWithScores(ctx, key, min, max, offset, count, true)
}

func (module *Module) zRangeArgs(key string, min, max, offset, count int64, reverse bool) (redis.ZRangeArgs, error) {
	if validateScore(min) != nil || validateScore(max) != nil || min > max {
		return redis.ZRangeArgs{}, ErrInvalidScore
	}
	if offset < 0 || count <= 0 {
		return redis.ZRangeArgs{}, invalidArgument("redismodule Score Range Offset/Count 无效")
	}
	start, stop := any(strconv.FormatInt(min, 10)), any(strconv.FormatInt(max, 10))
	if reverse {
		start, stop = stop, start
	}
	return redis.ZRangeArgs{Key: key, Start: start, Stop: stop, ByScore: true, Rev: reverse, Offset: offset, Count: count}, nil
}

func (module *Module) zRangeByScore(ctx context.Context, key string, min, max, offset, count int64, reverse bool) ([]string, error) {
	args, err := module.zRangeArgs(key, min, max, offset, count, reverse)
	if err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.ZRangeArgs(ctx, args).Result()
}

func (module *Module) zRangeByScoreWithScores(ctx context.Context, key string, min, max, offset, count int64, reverse bool) ([]ScoredMember, error) {
	args, err := module.zRangeArgs(key, min, max, offset, count, reverse)
	if err != nil {
		return nil, err
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.ZRangeArgsWithScores(ctx, args).Result()
	if err != nil {
		return nil, err
	}
	return scoredMembers(values)
}

// ZCount 返回包含 min/max 的整数 Score 区间成员数。
func (module *Module) ZCount(ctx context.Context, key string, min, max int64) (int64, error) {
	if validateScore(min) != nil || validateScore(max) != nil || min > max {
		return 0, ErrInvalidScore
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZCount(ctx, key, strconv.FormatInt(min, 10), strconv.FormatInt(max, 10)).Result()
}

// ZCard 返回 Sorted Set 成员数；Key 不存在返回 0、nil。
func (module *Module) ZCard(ctx context.Context, key string) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZCard(ctx, key).Result()
}

// ZRemRangeByRank 删除包含 start/stop 的排名区间并返回删除数；负索引按 Redis 语义计算。
func (module *Module) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error) {
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZRemRangeByRank(ctx, key, start, stop).Result()
}

// ZRemRangeByScore 删除包含 min/max 的整数 Score 区间并返回删除数。
func (module *Module) ZRemRangeByScore(ctx context.Context, key string, min, max int64) (int64, error) {
	if validateScore(min) != nil || validateScore(max) != nil || min > max {
		return 0, ErrInvalidScore
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return 0, err
	}
	return client.ZRemRangeByScore(ctx, key, strconv.FormatInt(min, 10), strconv.FormatInt(max, 10)).Result()
}

// ZPopMin 原子移除并返回最多 count 个最低分成员；count 必须大于 0。
//
// Key 不存在返回空切片、nil；任一小数或越界 Score 返回 ErrInvalidScore。
func (module *Module) ZPopMin(ctx context.Context, key string, count int64) ([]ScoredMember, error) {
	if count <= 0 {
		return nil, invalidArgument("redismodule ZPopMin Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.ZPopMin(ctx, key, count).Result()
	if err != nil {
		return nil, err
	}
	return scoredMembers(values)
}

// ZPopMax 原子移除并返回最多 count 个最高分成员；count 必须大于 0。
//
// Key 不存在返回空切片、nil；任一小数或越界 Score 返回 ErrInvalidScore。
func (module *Module) ZPopMax(ctx context.Context, key string, count int64) ([]ScoredMember, error) {
	if count <= 0 {
		return nil, invalidArgument("redismodule ZPopMax Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, err
	}
	values, err := client.ZPopMax(ctx, key, count).Result()
	if err != nil {
		return nil, err
	}
	return scoredMembers(values)
}

// ZScan 从 cursor 开始按 pattern 增量扫描成员，count 是大于 0 的服务端提示数。
//
// 调用方必须循环到 nextCursor 为 0；顺序和单轮数量不保证。小数或越界 Score 返回 ErrInvalidScore。
func (module *Module) ZScan(ctx context.Context, key string, cursor uint64, pattern string, count int64) ([]ScoredMember, uint64, error) {
	if count <= 0 {
		return nil, 0, invalidArgument("redismodule ZScan Count 必须大于 0")
	}
	client, err := module.requireClient(ctx)
	if err != nil {
		return nil, 0, err
	}
	values, next, err := client.ZScan(ctx, key, cursor, pattern, count).Result()
	if err != nil {
		return nil, 0, err
	}
	members, err := scanScoredMembers(values)
	return members, next, err
}
