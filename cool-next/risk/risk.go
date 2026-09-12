package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 验证码风控配置
type CaptchaConfig struct {
	TTL              time.Duration `json:"ttl"`
	Capacity         int           `json:"capacity"`
	OutstandingPerIP int           `json:"outstandingPerIP"`
	RateLimit        int           `json:"rateLimit"`
	RateWindow       time.Duration `json:"rateWindow"`
}

// 登录风控配置
type LoginConfig struct {
	IPRateLimit   int           `json:"ipRateLimit"`
	IPRateWindow  time.Duration `json:"ipRateWindow"`
	FailureLimit  int           `json:"failureLimit"`
	FailureWindow time.Duration `json:"failureWindow"`
	LockBase      time.Duration `json:"lockBase"`
	LockMax       time.Duration `json:"lockMax"`
}

// 风控配置
type Config struct {
	Type              string        `json:"type"`
	Group             string        `json:"group"`
	Prefix            string        `json:"prefix"`
	TrustedProxyCIDRs []string      `json:"trustedProxyCIDRs"`
	ClientCapacity    int           `json:"clientCapacity"`
	AccountCapacity   int           `json:"accountCapacity"`
	Captcha           CaptchaConfig `json:"captcha"`
	Login             LoginConfig   `json:"login"`
}

// 返回默认风控配置
func DefaultConfig() Config {
	return Config{
		Type:            MemoryType,
		Group:           DefaultGroup,
		Prefix:          "cool:risk:",
		ClientCapacity:  100_000,
		AccountCapacity: 100_000,
		Captcha: CaptchaConfig{
			TTL:              5 * time.Minute,
			Capacity:         10_000,
			OutstandingPerIP: 5,
			RateLimit:        10,
			RateWindow:       time.Minute,
		},
		Login: LoginConfig{
			IPRateLimit:   20,
			IPRateWindow:  time.Minute,
			FailureLimit:  5,
			FailureWindow: 15 * time.Minute,
			LockBase:      time.Minute,
			LockMax:       30 * time.Minute,
		},
	}
}

// 校验风控配置
func (config Config) Validate() error {
	if config.Type != MemoryType && config.Type != RedisType {
		return gerror.New("risk config: Type 只支持 memory 或 redis")
	}
	if strings.TrimSpace(config.Group) == "" || strings.TrimSpace(config.Group) != config.Group {
		return gerror.New("risk config: Redis Group 无效")
	}
	if strings.TrimSpace(config.Prefix) == "" || strings.TrimSpace(config.Prefix) != config.Prefix ||
		!strings.HasSuffix(config.Prefix, ":") {
		return gerror.New("risk config: Redis Prefix 必须是以冒号结尾的非空字符串")
	}
	if strings.IndexFunc(config.Prefix, unicode.IsControl) >= 0 {
		return gerror.New("risk config: Redis Prefix 不能包含控制字符")
	}
	if config.ClientCapacity <= 0 || config.AccountCapacity <= 0 || config.Captcha.TTL <= 0 ||
		config.Captcha.Capacity <= 0 || config.Captcha.OutstandingPerIP <= 0 || config.Captcha.RateLimit <= 0 ||
		config.Captcha.RateWindow <= 0 || config.Login.IPRateLimit <= 0 || config.Login.IPRateWindow <= 0 ||
		config.Login.FailureLimit <= 0 || config.Login.FailureWindow <= 0 || config.Login.LockBase <= 0 ||
		config.Login.LockMax <= 0 {
		return gerror.New("risk config: 容量、次数和时间参数必须为正数")
	}
	if config.Captcha.Capacity < config.Captcha.OutstandingPerIP {
		return gerror.New("risk config: Captcha Capacity 不能小于 Outstanding Per IP")
	}
	if config.Login.LockBase > config.Login.LockMax {
		return gerror.New("risk config: Login Lock Base 不能大于 Lock Max")
	}
	seen := make(map[netip.Prefix]struct{}, len(config.TrustedProxyCIDRs))
	for _, value := range config.TrustedProxyCIDRs {
		if strings.TrimSpace(value) != value {
			return gerror.New("risk config: Trusted Proxy CIDR 无效")
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return gerror.Wrap(err, "risk config: Trusted Proxy CIDR 无效")
		}
		prefix = prefix.Masked()
		if _, exists := seen[prefix]; exists {
			return gerror.New("risk config: Trusted Proxy CIDR 重复")
		}
		seen[prefix] = struct{}{}
	}

	return nil
}

const (
	MemoryType = "memory"
	RedisType  = "redis"

	DefaultGroup = "default"

	riskRedisCooldown  = 5 * time.Second
	riskRedisPingLimit = 5 * time.Second
)

type riskResult uint8

const (
	riskAllowed riskResult = iota
	riskLimited
	riskMatched
	riskNotMatched
	riskConflict
)

type riskBackend interface {
	allowCaptcha(context.Context, string) (riskResult, error)
	saveCaptcha(context.Context, string, string, string) (riskResult, error)
	consumeCaptcha(context.Context, string, string) (riskResult, error)
	allowLogin(context.Context, string, string) (riskResult, error)
	recordLoginFailure(context.Context, string) (riskResult, error)
	clearLoginFailure(context.Context, string) (riskResult, error)
}

// 统一风控组件
type Service struct {
	backend        riskBackend
	fallback       *memoryRiskBackend
	isRedis        bool
	now            func() time.Time
	trustedProxies []netip.Prefix

	stateMu       sync.Mutex
	degradedUntil time.Time
	isProbing     bool
}

// 创建并校验风控组件
func New(config Config) (*Service, error) {
	if config.Type == "" {
		config = DefaultConfig()
	}
	ctx, cancel := context.WithTimeout(context.Background(), riskRedisPingLimit)
	defer cancel()

	return newService(ctx, config, nil, time.Now)
}

func newService(ctx context.Context, config Config, client redisClient, now func() time.Time) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, exception.WrapCore(err, "Risk 配置无效")
	}
	if now == nil {
		return nil, exception.Core("Risk 时钟不能为空")
	}
	trustedProxies := make([]netip.Prefix, len(config.TrustedProxyCIDRs))
	for index, value := range config.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, exception.WrapCore(err, "Risk 可信代理配置无效")
		}
		trustedProxies[index] = prefix.Masked()
	}
	fallback := newMemoryBackend(config, now)
	service := &Service{backend: fallback, fallback: fallback, now: now, trustedProxies: trustedProxies}
	if config.Type == MemoryType {
		return service, nil
	}
	backend, err := newRedisRiskBackend(ctx, config, client)
	if err != nil {
		return nil, err
	}
	service.backend = backend
	service.isRedis = true

	return service, nil
}

// 从直连地址或可信代理链解析客户端 IP
func (service *Service) ResolveClientIP(ctx context.Context) (string, error) {
	if service == nil {
		return "", exception.Core("Risk 服务未初始化", http.StatusServiceUnavailable)
	}
	request := ghttp.RequestFromCtx(ctx)
	if request == nil || request.Request == nil {
		return "", exception.Core("HTTP 请求上下文无效", http.StatusServiceUnavailable)
	}
	remote, err := parseRiskAddress(request.RemoteAddr)
	if err != nil {
		return "", exception.WrapCore(err, "客户端连接源地址无效", http.StatusServiceUnavailable)
	}
	if !service.isTrustedProxy(remote) {
		return remote.String(), nil
	}
	forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		return remote.String(), nil
	}
	parts := strings.Split(forwarded, ",")
	chain := make([]netip.Addr, len(parts))
	for index, part := range parts {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(part))
		if parseErr != nil {
			return remote.String(), nil
		}
		chain[index] = address.Unmap()
	}
	for index := len(chain) - 1; index >= 0; index-- {
		if !service.isTrustedProxy(chain[index]) {
			return chain[index].String(), nil
		}
	}

	return chain[0].String(), nil
}

// 检查验证码生成频率和容量
func (service *Service) AllowCaptcha(ctx context.Context, clientIP string) error {
	if err := service.validateIdentity(clientIP, "客户端 IP"); err != nil {
		return err
	}
	result, err := service.execute(ctx, true, func(backend riskBackend) (riskResult, error) {
		return backend.allowCaptcha(ctx, riskDigest(clientIP))
	})
	if err != nil {
		return err
	}
	if result == riskLimited {
		return riskLimitError()
	}

	return nil
}

// 保存一次性验证码
func (service *Service) SaveCaptcha(ctx context.Context, clientIP, captchaID, answer string) error {
	if err := service.validateIdentity(clientIP, "客户端 IP"); err != nil {
		return err
	}
	if captchaID == "" || answer == "" {
		return exception.Core("验证码保存参数无效")
	}
	result, err := service.execute(ctx, false, func(backend riskBackend) (riskResult, error) {
		return backend.saveCaptcha(ctx, riskDigest(clientIP), captchaID, strings.ToLower(answer))
	})
	if err != nil {
		return err
	}
	switch result {
	case riskLimited:
		return riskLimitError()
	case riskConflict:
		return exception.Core("验证码标识冲突")
	default:
		return nil
	}
}

// 原子消费并校验验证码
func (service *Service) ConsumeCaptcha(ctx context.Context, captchaID, answer string) (bool, error) {
	if service == nil {
		return false, exception.Core("Risk 服务未初始化")
	}
	if captchaID == "" || answer == "" {
		return false, nil
	}
	result, err := service.execute(ctx, false, func(backend riskBackend) (riskResult, error) {
		return backend.consumeCaptcha(ctx, captchaID, strings.ToLower(answer))
	})
	if err != nil {
		return false, err
	}

	return result == riskMatched, nil
}

// 检查登录 IP 频率和账号封禁
func (service *Service) AllowLogin(ctx context.Context, clientIP, account string) error {
	if err := service.validateIdentity(clientIP, "客户端 IP"); err != nil {
		return err
	}
	account = normAccount(account)
	if account == "" {
		return exception.Core("Risk 账号不能为空")
	}
	result, err := service.execute(ctx, true, func(backend riskBackend) (riskResult, error) {
		return backend.allowLogin(ctx, riskDigest(clientIP), riskDigest(account))
	})
	if err != nil {
		return err
	}
	if result == riskLimited {
		return riskLimitError()
	}

	return nil
}

// 记录账号凭据失败
func (service *Service) RecordLoginFailure(ctx context.Context, account string) error {
	if service == nil {
		return exception.Core("Risk 服务未初始化")
	}
	account = normAccount(account)
	if account == "" {
		return exception.Core("Risk 账号不能为空")
	}
	result, err := service.execute(ctx, true, func(backend riskBackend) (riskResult, error) {
		return backend.recordLoginFailure(ctx, riskDigest(account))
	})
	if err != nil {
		return err
	}
	if result == riskLimited {
		return riskLimitError()
	}

	return nil
}

// 清除账号凭据失败状态
func (service *Service) ClearLoginFailure(ctx context.Context, account string) error {
	if service == nil {
		return exception.Core("Risk 服务未初始化")
	}
	account = normAccount(account)
	if account == "" {
		return exception.Core("Risk 账号不能为空")
	}
	_, err := service.execute(ctx, false, func(backend riskBackend) (riskResult, error) {
		return backend.clearLoginFailure(ctx, riskDigest(account))
	})

	return err
}

func (service *Service) execute(
	ctx context.Context,
	canFallback bool,
	operation func(riskBackend) (riskResult, error),
) (riskResult, error) {
	if service.backend == nil || service.now == nil {
		return riskAllowed, exception.Core("Risk 服务未初始化")
	}
	if !service.isRedis {
		return operation(service.backend)
	}
	shouldAttempt, isProbe := service.beginRedisAttempt()
	if shouldAttempt {
		result, err := operation(service.backend)
		if err == nil {
			service.markRedisSuccess(ctx, isProbe)
			return result, nil
		}
		if ctx != nil && ctx.Err() != nil {
			service.cancelRedisProbe(isProbe)
			return riskAllowed, ctx.Err()
		}
		service.markRedisFailure(ctx, err)
	}
	if canFallback {
		return operation(service.fallback)
	}

	return riskAllowed, exception.Core("Risk 状态服务不可用", http.StatusServiceUnavailable)
}

func (service *Service) beginRedisAttempt() (bool, bool) {
	service.stateMu.Lock()
	defer service.stateMu.Unlock()

	if service.degradedUntil.IsZero() {
		return true, false
	}
	if service.now().Before(service.degradedUntil) || service.isProbing {
		return false, false
	}
	service.isProbing = true

	return true, true
}

func (service *Service) cancelRedisProbe(isProbe bool) {
	if !isProbe {
		return
	}
	service.stateMu.Lock()
	service.isProbing = false
	service.stateMu.Unlock()
}

func (service *Service) markRedisFailure(ctx context.Context, cause error) {
	service.stateMu.Lock()
	shouldLog := service.degradedUntil.IsZero()
	service.degradedUntil = service.now().Add(riskRedisCooldown)
	service.isProbing = false
	service.stateMu.Unlock()
	if shouldLog {
		g.Log().Warning(logContext(ctx), "Risk Redis 不可用，进入本机降级", exception.LogText(cause))
	}
}

func (service *Service) markRedisSuccess(ctx context.Context, isProbe bool) {
	if !isProbe {
		return
	}
	service.stateMu.Lock()
	service.degradedUntil = time.Time{}
	service.isProbing = false
	service.stateMu.Unlock()
	g.Log().Info(logContext(ctx), "Risk Redis 已恢复")
}

func (service *Service) validateIdentity(value, label string) error {
	if service == nil {
		return exception.Core("Risk 服务未初始化")
	}
	if strings.TrimSpace(value) == "" {
		return exception.Core("Risk " + label + "不能为空")
	}

	return nil
}

func (service *Service) isTrustedProxy(address netip.Addr) bool {
	for _, prefix := range service.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}

	return false
}

func parseRiskAddress(value string) (netip.Addr, error) {
	if addressPort, err := netip.ParseAddrPort(value); err == nil {
		return addressPort.Addr().Unmap(), nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, err
	}

	return address.Unmap(), nil
}

func normAccount(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func riskDigest(value string) string {
	digest := sha256.Sum256([]byte(value))

	return hex.EncodeToString(digest[:])
}

func riskLimitError() error {
	return exception.Comm("请求过于频繁，请稍后重试", http.StatusTooManyRequests)
}

func logContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

type riskWindow struct {
	count     int
	expiresAt time.Time
}

type memoryRiskClient struct {
	captcha riskWindow
	login   riskWindow
}

type memoryRiskCaptcha struct {
	answer    string
	client    string
	expiresAt time.Time
}

type memoryRiskAccount struct {
	failures    int
	lastFailure time.Time
	lockUntil   time.Time
	expiresAt   time.Time
}

type memoryRiskBackend struct {
	config           Config
	now              func() time.Time
	mutex            sync.Mutex
	clients          map[string]*memoryRiskClient
	accounts         map[string]*memoryRiskAccount
	captchas         map[string]memoryRiskCaptcha
	captchasByClient map[string]map[string]time.Time
}

func newMemoryBackend(config Config, now func() time.Time) *memoryRiskBackend {
	return &memoryRiskBackend{
		config: config, now: now,
		clients: make(map[string]*memoryRiskClient), accounts: make(map[string]*memoryRiskAccount),
		captchas: make(map[string]memoryRiskCaptcha), captchasByClient: make(map[string]map[string]time.Time),
	}
}

func (backend *memoryRiskBackend) allowCaptcha(_ context.Context, client string) (riskResult, error) {
	backend.lock()
	defer backend.unlock()

	now := backend.now()
	state, exists := backend.clientLocked(client, now)
	if !exists {
		return riskLimited, nil
	}
	if increaseRiskWindow(&state.captcha, now, backend.config.Captcha.RateWindow) > backend.config.Captcha.RateLimit {
		return riskLimited, nil
	}
	backend.cleanCaptchasLocked(client, now)
	if len(backend.captchasByClient[client]) >= backend.config.Captcha.OutstandingPerIP {
		return riskLimited, nil
	}
	if len(backend.captchas) >= backend.config.Captcha.Capacity {
		backend.cleanAllLocked(now)
		if len(backend.captchas) >= backend.config.Captcha.Capacity {
			return riskLimited, nil
		}
	}

	return riskAllowed, nil
}

func (backend *memoryRiskBackend) saveCaptcha(_ context.Context, client, captchaID, answer string) (riskResult, error) {
	backend.lock()
	defer backend.unlock()

	if _, exists := backend.captchas[captchaID]; exists {
		return riskConflict, nil
	}
	now := backend.now()
	state, exists := backend.clientLocked(client, now)
	if !exists {
		return riskLimited, nil
	}
	backend.cleanCaptchasLocked(client, now)
	if len(backend.captchasByClient[client]) >= backend.config.Captcha.OutstandingPerIP {
		return riskLimited, nil
	}
	if len(backend.captchas) >= backend.config.Captcha.Capacity {
		backend.cleanAllLocked(now)
		if len(backend.captchas) >= backend.config.Captcha.Capacity {
			return riskLimited, nil
		}
	}
	expiresAt := now.Add(backend.config.Captcha.TTL)
	backend.captchas[captchaID] = memoryRiskCaptcha{answer: answer, client: client, expiresAt: expiresAt}
	if backend.captchasByClient[client] == nil {
		backend.captchasByClient[client] = make(map[string]time.Time)
	}
	backend.captchasByClient[client][captchaID] = expiresAt
	_ = state

	return riskAllowed, nil
}

func (backend *memoryRiskBackend) consumeCaptcha(_ context.Context, captchaID, answer string) (riskResult, error) {
	backend.lock()
	defer backend.unlock()

	record, exists := backend.captchas[captchaID]
	if !exists {
		return riskNotMatched, nil
	}
	backend.removeCaptchaLocked(captchaID, record.client)
	if !record.expiresAt.After(backend.now()) || record.answer != answer {
		return riskNotMatched, nil
	}

	return riskMatched, nil
}

func (backend *memoryRiskBackend) allowLogin(_ context.Context, client, account string) (riskResult, error) {
	backend.lock()
	defer backend.unlock()

	now := backend.now()
	state, exists := backend.clientLocked(client, now)
	if !exists {
		return riskLimited, nil
	}
	if increaseRiskWindow(&state.login, now, backend.config.Login.IPRateWindow) > backend.config.Login.IPRateLimit {
		return riskLimited, nil
	}
	accountState := backend.accounts[account]
	if accountState == nil {
		return riskAllowed, nil
	}
	if !accountState.expiresAt.After(now) {
		delete(backend.accounts, account)
		return riskAllowed, nil
	}
	if accountState.lockUntil.After(now) {
		return riskLimited, nil
	}

	return riskAllowed, nil
}

func (backend *memoryRiskBackend) recordLoginFailure(_ context.Context, account string) (riskResult, error) {
	backend.lock()
	defer backend.unlock()

	now := backend.now()
	state := backend.accounts[account]
	if state != nil && !state.expiresAt.After(now) {
		delete(backend.accounts, account)
		state = nil
	}
	if state == nil {
		if len(backend.accounts) >= backend.config.AccountCapacity {
			backend.cleanAccountsLocked(now)
			if len(backend.accounts) >= backend.config.AccountCapacity {
				return riskLimited, nil
			}
		}
		state = &memoryRiskAccount{}
		backend.accounts[account] = state
	}
	if state.lockUntil.After(now) {
		return riskAllowed, nil
	}
	if !state.lastFailure.IsZero() && !state.lastFailure.Add(backend.config.Login.FailureWindow).After(now) {
		state.failures = 0
	}
	state.failures++
	state.lastFailure = now
	if state.failures >= backend.config.Login.FailureLimit {
		state.lockUntil = now.Add(riskLockDuration(backend.config, state.failures))
	}
	state.expiresAt = laterTime(now.Add(backend.config.Login.FailureWindow), state.lockUntil)

	return riskAllowed, nil
}

func (backend *memoryRiskBackend) clearLoginFailure(_ context.Context, account string) (riskResult, error) {
	backend.lock()
	delete(backend.accounts, account)
	backend.unlock()

	return riskAllowed, nil
}

func (backend *memoryRiskBackend) clientLocked(client string, now time.Time) (*memoryRiskClient, bool) {
	backend.cleanClientLocked(client, now)
	if state := backend.clients[client]; state != nil {
		return state, true
	}
	if len(backend.clients) >= backend.config.ClientCapacity {
		backend.cleanClientsLocked(now)
		if len(backend.clients) >= backend.config.ClientCapacity {
			return nil, false
		}
	}
	state := &memoryRiskClient{}
	backend.clients[client] = state

	return state, true
}

func (backend *memoryRiskBackend) cleanClientsLocked(now time.Time) {
	backend.cleanAllLocked(now)
	for client := range backend.clients {
		backend.cleanClientLocked(client, now)
	}
}

func (backend *memoryRiskBackend) cleanClientLocked(client string, now time.Time) {
	state := backend.clients[client]
	if state == nil {
		return
	}
	resetRiskWindow(&state.captcha, now)
	resetRiskWindow(&state.login, now)
	backend.cleanCaptchasLocked(client, now)
	if state.captcha.count == 0 && state.login.count == 0 && len(backend.captchasByClient[client]) == 0 {
		delete(backend.clients, client)
	}
}

func (backend *memoryRiskBackend) cleanCaptchasLocked(client string, now time.Time) {
	for captchaID, expiresAt := range backend.captchasByClient[client] {
		if !expiresAt.After(now) {
			backend.removeCaptchaLocked(captchaID, client)
		}
	}
}

func (backend *memoryRiskBackend) cleanAllLocked(now time.Time) {
	for captchaID, record := range backend.captchas {
		if !record.expiresAt.After(now) {
			backend.removeCaptchaLocked(captchaID, record.client)
		}
	}
}

func (backend *memoryRiskBackend) removeCaptchaLocked(captchaID, client string) {
	delete(backend.captchas, captchaID)
	delete(backend.captchasByClient[client], captchaID)
	if len(backend.captchasByClient[client]) == 0 {
		delete(backend.captchasByClient, client)
	}
}

func (backend *memoryRiskBackend) cleanAccountsLocked(now time.Time) {
	for account, state := range backend.accounts {
		if !state.expiresAt.After(now) {
			delete(backend.accounts, account)
		}
	}
}

func (backend *memoryRiskBackend) lock() {
	backend.mutex.Lock()
}

func (backend *memoryRiskBackend) unlock() {
	backend.mutex.Unlock()
}

func increaseRiskWindow(window *riskWindow, now time.Time, duration time.Duration) int {
	if !window.expiresAt.After(now) {
		window.count = 0
		window.expiresAt = now.Add(duration)
	}
	window.count++

	return window.count
}

func resetRiskWindow(window *riskWindow, now time.Time) {
	if !window.expiresAt.After(now) {
		*window = riskWindow{}
	}
}

func riskLockDuration(config Config, failures int) time.Duration {
	duration := config.Login.LockBase
	for remaining := failures - config.Login.FailureLimit; remaining > 0; remaining-- {
		if duration >= config.Login.LockMax || duration > config.Login.LockMax/2 {
			return config.Login.LockMax
		}
		duration *= 2
	}
	if duration > config.Login.LockMax {
		return config.Login.LockMax
	}

	return duration
}

func laterTime(first, second time.Time) time.Time {
	if second.After(first) {
		return second
	}

	return first
}
