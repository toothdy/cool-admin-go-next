package risk

import (
	"context"
	"strings"

	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/database/gredis"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const allowCaptchaScript = `
local currentTime = redis.call('TIME')
local now = currentTime[1] * 1000 + math.floor(currentTime[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local currentClient = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not currentClient and redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[2]) then return 1 end
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[4]) end
local expiresAt = now + math.max(redis.call('PTTL', KEYS[1]), 0)
if not currentClient or expiresAt > tonumber(currentClient) then
  redis.call('ZADD', KEYS[2], expiresAt, ARGV[1])
end
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now)
if count > tonumber(ARGV[3]) then return 1 end
if redis.call('ZCARD', KEYS[3]) >= tonumber(ARGV[5]) then return 1 end
if redis.call('ZCARD', KEYS[4]) >= tonumber(ARGV[6]) then return 1 end
return 0
`

const saveCaptchaScript = `
local currentTime = redis.call('TIME')
local now = currentTime[1] * 1000 + math.floor(currentTime[2] / 1000)
if redis.call('EXISTS', KEYS[1]) == 1 then return 2 end
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now)
local currentClient = redis.call('ZSCORE', KEYS[4], ARGV[1])
if not currentClient and redis.call('ZCARD', KEYS[4]) >= tonumber(ARGV[2]) then return 1 end
if redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[3]) then return 1 end
if redis.call('ZCARD', KEYS[3]) >= tonumber(ARGV[4]) then return 1 end
local expiresAt = now + tonumber(ARGV[5])
redis.call('HSET', KEYS[1], 'answer', ARGV[7], 'owner', ARGV[1])
redis.call('PEXPIREAT', KEYS[1], expiresAt)
redis.call('ZADD', KEYS[2], expiresAt, ARGV[6])
redis.call('ZADD', KEYS[3], expiresAt, ARGV[6])
redis.call('PEXPIREAT', KEYS[3], expiresAt)
if not currentClient or expiresAt > tonumber(currentClient) then
  redis.call('ZADD', KEYS[4], expiresAt, ARGV[1])
end
return 0
`

const useCaptchaScript = `
local stored = redis.call('HGET', KEYS[1], 'answer')
local owner = redis.call('HGET', KEYS[1], 'owner')
if not stored or not owner then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[1])
  return 0
end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
local ownerIndex = ARGV[3] .. 'captcha:client:' .. owner
redis.call('ZREM', ownerIndex, ARGV[1])
if redis.call('ZCARD', ownerIndex) == 0 then redis.call('DEL', ownerIndex) end
if stored == ARGV[2] then return 1 end
return 0
`

const allowLoginScript = `
local currentTime = redis.call('TIME')
local now = currentTime[1] * 1000 + math.floor(currentTime[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local currentClient = redis.call('ZSCORE', KEYS[2], ARGV[1])
if not currentClient and redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[2]) then return 1 end
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[4]) end
local expiresAt = now + math.max(redis.call('PTTL', KEYS[1]), 0)
if not currentClient or expiresAt > tonumber(currentClient) then
  redis.call('ZADD', KEYS[2], expiresAt, ARGV[1])
end
local lockUntil = tonumber(redis.call('HGET', KEYS[3], 'lockUntil') or '0')
if count > tonumber(ARGV[3]) or lockUntil > now then return 1 end
return 0
`

const recordLoginScript = `
local currentTime = redis.call('TIME')
local now = currentTime[1] * 1000 + math.floor(currentTime[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local exists = redis.call('EXISTS', KEYS[1])
if exists == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  if redis.call('ZCARD', KEYS[2]) >= tonumber(ARGV[2]) then return 1 end
end
local lockUntil = tonumber(redis.call('HGET', KEYS[1], 'lockUntil') or '0')
if lockUntil > now then return 0 end
local failures = tonumber(redis.call('HGET', KEYS[1], 'failures') or '0')
local lastFailure = tonumber(redis.call('HGET', KEYS[1], 'lastFailure') or '0')
if lastFailure == 0 or now - lastFailure >= tonumber(ARGV[4]) then failures = 0 end
failures = failures + 1
lockUntil = 0
if failures >= tonumber(ARGV[3]) then
  local duration = tonumber(ARGV[5])
  local steps = failures - tonumber(ARGV[3])
  if steps > 63 then
    duration = tonumber(ARGV[6])
  else
    while steps > 0 and duration < tonumber(ARGV[6]) do
      duration = math.min(duration * 2, tonumber(ARGV[6]))
      steps = steps - 1
    end
  end
  lockUntil = now + duration
end
local expiresAt = math.max(now + tonumber(ARGV[4]), lockUntil)
redis.call('HSET', KEYS[1], 'failures', failures, 'lastFailure', now, 'lockUntil', lockUntil)
redis.call('PEXPIREAT', KEYS[1], expiresAt)
redis.call('ZADD', KEYS[2], expiresAt, ARGV[1])
return 0
`

const clearLoginScript = `
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
return 0
`

type redisClient interface {
	Do(context.Context, string, ...any) (*gvar.Var, error)
	Eval(context.Context, string, int64, []string, []any) (*gvar.Var, error)
}

type redisRiskBackend struct {
	client redisClient
	config Config
	prefix string
}

func newRedisRiskBackend(ctx context.Context, config Config, client redisClient) (*redisRiskBackend, error) {
	if client == nil {
		if _, exists := gredis.GetConfig(config.Group); !exists {
			return nil, exception.Core("Risk Redis Group 不存在: " + config.Group)
		}
		client = gredis.Instance(config.Group)
	}
	if client == nil {
		return nil, exception.Core("创建 Risk Redis 连接失败: " + config.Group)
	}
	result, err := client.Do(ctx, "PING")
	if err != nil {
		return nil, exception.WrapCore(err, "Risk Redis PING 失败")
	}
	if result == nil || !strings.EqualFold(result.String(), "PONG") {
		return nil, exception.Core("Risk Redis PING 响应无效")
	}

	return &redisRiskBackend{client: client, config: config, prefix: config.Prefix}, nil
}

func (backend *redisRiskBackend) allowCaptcha(ctx context.Context, client string) (riskResult, error) {
	code, err := backend.evaluate(ctx, allowCaptchaScript, []string{
		backend.prefix + "limit:captcha:" + client,
		backend.prefix + "state:clients",
		backend.prefix + "captcha:index",
		backend.prefix + "captcha:client:" + client,
	}, []any{
		client, backend.config.ClientCapacity, backend.config.Captcha.RateLimit,
		backend.config.Captcha.RateWindow.Milliseconds(), backend.config.Captcha.Capacity,
		backend.config.Captcha.OutstandingPerIP,
	})

	return allowRiskResult(code, err)
}

func (backend *redisRiskBackend) saveCaptcha(
	ctx context.Context,
	client string,
	captchaID string,
	answer string,
) (riskResult, error) {
	code, err := backend.evaluate(ctx, saveCaptchaScript, []string{
		backend.prefix + "captcha:" + captchaID,
		backend.prefix + "captcha:index",
		backend.prefix + "captcha:client:" + client,
		backend.prefix + "state:clients",
	}, []any{
		client, backend.config.ClientCapacity, backend.config.Captcha.Capacity,
		backend.config.Captcha.OutstandingPerIP, backend.config.Captcha.TTL.Milliseconds(), captchaID, answer,
	})
	if err != nil {
		return riskAllowed, err
	}
	switch code {
	case 0:
		return riskAllowed, nil
	case 1:
		return riskLimited, nil
	case 2:
		return riskConflict, nil
	default:
		return riskAllowed, invalidRedisResult()
	}
}

func (backend *redisRiskBackend) consumeCaptcha(ctx context.Context, captchaID, answer string) (riskResult, error) {
	code, err := backend.evaluate(ctx, useCaptchaScript, []string{
		backend.prefix + "captcha:" + captchaID,
		backend.prefix + "captcha:index",
	}, []any{captchaID, answer, backend.prefix})
	if err != nil {
		return riskNotMatched, err
	}
	switch code {
	case 0:
		return riskNotMatched, nil
	case 1:
		return riskMatched, nil
	default:
		return riskNotMatched, invalidRedisResult()
	}
}

func (backend *redisRiskBackend) allowLogin(ctx context.Context, client, account string) (riskResult, error) {
	code, err := backend.evaluate(ctx, allowLoginScript, []string{
		backend.prefix + "limit:login:" + client,
		backend.prefix + "state:clients",
		backend.prefix + "account:" + account,
	}, []any{
		client, backend.config.ClientCapacity, backend.config.Login.IPRateLimit,
		backend.config.Login.IPRateWindow.Milliseconds(),
	})

	return allowRiskResult(code, err)
}

func (backend *redisRiskBackend) recordLoginFailure(ctx context.Context, account string) (riskResult, error) {
	code, err := backend.evaluate(ctx, recordLoginScript, []string{
		backend.prefix + "account:" + account,
		backend.prefix + "state:accounts",
	}, []any{
		account, backend.config.AccountCapacity, backend.config.Login.FailureLimit,
		backend.config.Login.FailureWindow.Milliseconds(), backend.config.Login.LockBase.Milliseconds(),
		backend.config.Login.LockMax.Milliseconds(),
	})

	return allowRiskResult(code, err)
}

func (backend *redisRiskBackend) clearLoginFailure(ctx context.Context, account string) (riskResult, error) {
	code, err := backend.evaluate(ctx, clearLoginScript, []string{
		backend.prefix + "account:" + account,
		backend.prefix + "state:accounts",
	}, []any{account})
	if err != nil {
		return riskAllowed, err
	}
	if code != 0 {
		return riskAllowed, invalidRedisResult()
	}

	return riskAllowed, nil
}

func (backend *redisRiskBackend) evaluate(
	ctx context.Context,
	script string,
	keys []string,
	arguments []any,
) (int, error) {
	result, err := backend.client.Eval(ctx, script, int64(len(keys)), keys, arguments)
	if err != nil {
		return 0, exception.WrapCore(err, "执行 Risk Redis 脚本失败")
	}
	if result == nil {
		return 0, invalidRedisResult()
	}

	return result.Int(), nil
}

func allowRiskResult(code int, err error) (riskResult, error) {
	if err != nil {
		return riskAllowed, err
	}
	switch code {
	case 0:
		return riskAllowed, nil
	case 1:
		return riskLimited, nil
	default:
		return riskAllowed, invalidRedisResult()
	}
}

func invalidRedisResult() error {
	return exception.Core("Risk Redis 脚本结果无效")
}

var _ redisClient = (*gredis.Redis)(nil)
