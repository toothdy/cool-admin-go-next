package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

// 身份种类
type Kind string

const (
	AdminKind Kind = "admin" // 管理端身份
	AppKind   Kind = "app"   // 应用端身份
)

var (
	// 无效凭证
	ErrInvalidCredential = errors.New("登录失效~")
	// Session 不存在
	ErrSessionNotFound = errors.New("session 不存在")
	// Refresh Token 重放
	ErrRefreshReplay = errors.New("refresh token 重放")
)

// JWT 签发所需身份快照
type TokenSubject struct {
	SessionID string   // Session ID
	Subject   Kind     // 身份种类
	UserID    uint64   // 用户 ID
	Username  string   // 管理端用户名
	RoleIDs   []uint64 // 管理端角色 ID
	PasswordV int      // 管理端密码版本
}

// 已验证 JWT 内容
type Claims struct {
	TokenSubject           // 身份快照
	JTI          string    // Token 唯一标识
	IsRefresh    bool      // 是否为 Refresh Token
	IssuedAt     time.Time // 签发时间
	NotBefore    time.Time // 生效时间
	ExpiresAt    time.Time // 过期时间
}

// Access 与 Refresh Token 对
type Pair struct {
	AccessToken     string    // Access Token
	RefreshToken    string    // Refresh Token
	AccessJTI       string    // Access Token 唯一标识
	RefreshJTI      string    // Refresh Token 唯一标识
	AccessExpiresAt time.Time // Access Token 过期时间
	ExpiresAt       time.Time // Refresh Token 及 Session 过期时间
}

// JWT 签发与验证端口
type Codec interface {
	IssuePair(TokenSubject) (Pair, error)
	Parse(string, bool) (Claims, error)
}

// 鉴权 Session 只读快照
type Snapshot struct {
	TokenSubject           // 身份快照
	AccessJTI    string    // 当前 Access JTI
	RefreshJTI   string    // 当前 Refresh JTI
	ExpiresAt    time.Time // Refresh Session 过期时间
}

// 鉴权专用 Session 存储
type Store interface {
	Get(context.Context, string) (Snapshot, bool, error)
	Save(context.Context, Snapshot) error
	RotateRefresh(context.Context, string, string, Snapshot) error
	Revoke(context.Context, string) error
	RevokeUsers(context.Context, Kind, []uint64) error
}

// 新登录态身份
type Principal struct {
	Subject   Kind     // 身份种类
	UserID    uint64   // 用户 ID
	Username  string   // 管理端用户名
	RoleIDs   []uint64 // 管理端角色 ID
	PasswordV int      // 管理端密码版本
}

// 请求鉴权规则
type Rule struct {
	IgnoreToken bool   // 是否忽略 Token
	Permission  string // 权限标识
	Resource    string // 协议资源
}

// 管理端已验证身份
type AdminIdentity struct {
	UserID    uint64 // 用户 ID
	Username  string // 用户名
	PasswordV int    // 密码版本
	roleIDs   []uint64
}

// 返回角色 ID 副本
func (identity AdminIdentity) RoleIDs() []uint64 {
	return append([]uint64(nil), identity.roleIDs...)
}

// 应用端已验证身份
type AppIdentity struct {
	ID uint64 // 应用用户 ID
}

// 构造管理端已验证身份
func newAdminIdentity(userID uint64, username string, passwordV int, roleIDs []uint64) AdminIdentity {
	return AdminIdentity{
		UserID:    userID,
		Username:  username,
		PasswordV: passwordV,
		roleIDs:   append([]uint64(nil), roleIDs...),
	}
}

type identityContextKey struct{}
type sessionContextKey struct{}

// 已验证 Session 状态
type SessionState struct {
	ID        string    // Session ID
	Subject   Kind      // 身份种类
	AccessJTI string    // 当前 Access JTI
	ExpiresAt time.Time // Session 过期时间
}

// 返回管理端已验证身份
func Admin(ctx context.Context) (AdminIdentity, error) {
	identity, exists := identityFromContext(ctx)
	if !exists || identity.subject != AdminKind {
		return AdminIdentity{}, unauthErr()
	}

	identity.admin.roleIDs = identity.admin.RoleIDs()
	return identity.admin, nil
}

// 返回应用端已验证身份
func App(ctx context.Context) (AppIdentity, error) {
	identity, exists := identityFromContext(ctx)
	if !exists || identity.subject != AppKind {
		return AppIdentity{}, unauthErr()
	}

	return identity.app, nil
}

// 返回已验证 Session 状态
func Session(ctx context.Context) (SessionState, bool) {
	if ctx == nil {
		return SessionState{}, false
	}
	state, exists := ctx.Value(sessionContextKey{}).(SessionState)
	return state, exists
}

// 写入管理端已验证身份
func withAdmin(ctx context.Context, userID uint64, username string, passwordV int, roleIDs []uint64) context.Context {
	return context.WithValue(ctx, identityContextKey{}, verifiedIdentity{
		subject: AdminKind,
		admin:   newAdminIdentity(userID, username, passwordV, roleIDs),
	})
}

// 写入应用端已验证身份
func withApp(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, identityContextKey{}, verifiedIdentity{
		subject: AppKind,
		app:     AppIdentity{ID: id},
	})
}

// 写入已验证 Session 状态
func withSession(ctx context.Context, state SessionState) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, state)
}

type verifiedIdentity struct {
	subject Kind
	admin   AdminIdentity
	app     AppIdentity
}

// 读取 Context 中的已验证身份
func identityFromContext(ctx context.Context) (verifiedIdentity, bool) {
	if ctx == nil {
		return verifiedIdentity{}, false
	}

	identity, exists := ctx.Value(identityContextKey{}).(verifiedIdentity)
	return identity, exists
}

// 未验证身份异常
func unauthErr() error {
	return exception.Comm("身份未验证", http.StatusUnauthorized)
}

// 认证与刷新内核
type Service struct {
	tokens     Codec
	sessions   Store
	authorizer Authorizer
	now        func() time.Time
	newID      func() (string, error)
}

// 创建认证服务
func NewService(tokens Codec, sessions Store, authorizer Authorizer) (*Service, error) {
	return newService(tokens, sessions, authorizer, time.Now, randomID)
}

// 创建新登录态
func (service *Service) Create(ctx context.Context, principal Principal) (Pair, error) {
	if err := validatePrincipal(principal); err != nil {
		return Pair{}, err
	}
	sessionID, err := service.newID()
	if err != nil {
		return Pair{}, exception.WrapCore(err, "生成 Session ID 失败")
	}
	subject := tokenSubject(sessionID, principal)
	pair, err := service.tokens.IssuePair(subject)
	if err != nil {
		return Pair{}, exception.WrapCore(err, "签发 Token 失败")
	}
	if err = service.sessions.Save(ctx, snapshotFromPair(subject, pair)); err != nil {
		return Pair{}, exception.WrapCore(err, "保存登录 Session 失败")
	}

	return pair, nil
}

// 验证 Access Token 并写入可信身份
func (service *Service) Access(ctx context.Context, token string) (context.Context, error) {
	claims, current, err := service.verify(ctx, token, false)
	if err != nil {
		return ctx, err
	}

	return contextWithSession(ctx, claims, current), nil
}

// 为 Refresh 提供身份重新解析钩子
func (service *Service) RefreshWith(
	ctx context.Context,
	token string,
	resolve func(context.Context, Principal) (Principal, error),
) (Pair, error) {
	if resolve == nil {
		return Pair{}, exception.Core("刷新身份解析器不能为空")
	}
	claims, current, err := service.verify(ctx, token, true)
	if err != nil {
		return Pair{}, err
	}
	principal, err := resolve(ctx, toPrincipal(current))
	if err != nil {
		return Pair{}, err
	}
	if err = validatePrincipal(principal); err != nil {
		return Pair{}, err
	}
	if principal.Subject != current.Subject || principal.UserID != current.UserID {
		return Pair{}, exception.Core("刷新不能改变 Session 身份")
	}
	subject := tokenSubject(current.SessionID, principal)
	pair, err := service.tokens.IssuePair(subject)
	if err != nil {
		return Pair{}, exception.WrapCore(err, "签发刷新 Token 失败")
	}
	next := snapshotFromPair(subject, pair)
	if err = service.sessions.RotateRefresh(ctx, current.SessionID, claims.JTI, next); err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return Pair{}, unauthErr()
		case errors.Is(err, ErrRefreshReplay):
			return Pair{}, service.revokeReplay(ctx, current.SessionID)
		default:
			return Pair{}, exception.WrapCore(err, "轮换刷新 Session 失败")
		}
	}

	return pair, nil
}

// 按路由规则执行认证和授权
func (service *Service) Authenticate(ctx context.Context, token string, rule Rule) (context.Context, error) {
	if rule.IgnoreToken {
		return ctx, nil
	}
	verified, err := service.Access(ctx, token)
	if err != nil {
		return ctx, err
	}
	if strings.TrimSpace(rule.Permission) == "" {
		return verified, nil
	}
	if err = Authorize(verified, service.authorizer, rule.Permission, rule.Resource); err != nil {
		return ctx, err
	}

	return verified, nil
}

// 验证 Token 和服务端 Session
func (service *Service) verify(
	ctx context.Context,
	token string,
	isRefresh bool,
) (Claims, Snapshot, error) {
	claims, err := service.tokens.Parse(token, isRefresh)
	if err != nil {
		if errors.Is(err, ErrInvalidCredential) {
			return Claims{}, Snapshot{}, credentialErr()
		}
		return Claims{}, Snapshot{}, exception.WrapCore(err, "验证 Token 失败")
	}
	current, exists, err := service.sessions.Get(ctx, claims.SessionID)
	if err != nil {
		return Claims{}, Snapshot{}, exception.WrapCore(err, "读取鉴权 Session 失败")
	}
	if !exists || !current.ExpiresAt.After(service.now()) {
		return Claims{}, Snapshot{}, unauthErr()
	}
	if err = checkClaims(claims, current); err != nil {
		if isRefresh && claims.SessionID != "" && claims.JTI != current.RefreshJTI {
			return Claims{}, Snapshot{}, service.revokeReplay(ctx, claims.SessionID)
		}
		return Claims{}, Snapshot{}, unauthErr()
	}

	return claims, current, nil
}

// 撤销发生 Refresh 重放的 Session
func (service *Service) revokeReplay(ctx context.Context, sessionID string) error {
	if err := service.sessions.Revoke(ctx, sessionID); err != nil {
		return exception.WrapCore(err, "撤销重放 Session 失败")
	}

	return unauthErr()
}

// 创建认证服务
func newService(
	tokens Codec,
	sessions Store,
	authorizer Authorizer,
	now func() time.Time,
	newID func() (string, error),
) (*Service, error) {
	if tokens == nil || sessions == nil || now == nil || newID == nil {
		return nil, exception.Core("认证服务未初始化")
	}

	return &Service{tokens: tokens, sessions: sessions, authorizer: authorizer, now: now, newID: newID}, nil
}

// 校验新登录态身份
func validatePrincipal(principal Principal) error {
	if principal.UserID == 0 {
		return exception.Core("登录身份 ID 不能为空")
	}
	switch principal.Subject {
	case AdminKind:
		if strings.TrimSpace(principal.Username) == "" || principal.PasswordV <= 0 {
			return exception.Core("管理端登录身份无效")
		}
		for _, roleID := range principal.RoleIDs {
			if roleID == 0 {
				return exception.Core("管理端角色 ID 必须为正数")
			}
		}
	case AppKind:
		if principal.Username != "" || principal.RoleIDs != nil || principal.PasswordV != 0 {
			return exception.Core("应用端登录身份不能携带管理端字段")
		}
	default:
		return exception.Core("登录身份种类无效")
	}

	return nil
}

// 从身份构造 Token 快照
func tokenSubject(sessionID string, principal Principal) TokenSubject {
	return TokenSubject{
		SessionID: sessionID,
		Subject:   principal.Subject,
		UserID:    principal.UserID,
		Username:  principal.Username,
		RoleIDs:   append([]uint64(nil), principal.RoleIDs...),
		PasswordV: principal.PasswordV,
	}
}

// 从 Session 快照构造身份
func toPrincipal(snapshot Snapshot) Principal {
	return Principal{
		Subject:   snapshot.Subject,
		UserID:    snapshot.UserID,
		Username:  snapshot.Username,
		RoleIDs:   append([]uint64(nil), snapshot.RoleIDs...),
		PasswordV: snapshot.PasswordV,
	}
}

// 核对 Token 与服务端 Session
func checkClaims(claims Claims, current Snapshot) error {
	if claims.SessionID != current.SessionID || claims.Subject != current.Subject || claims.UserID != current.UserID {
		return ErrInvalidCredential
	}
	if claims.Subject == AdminKind && claims.PasswordV != current.PasswordV {
		return ErrInvalidCredential
	}
	wantJTI := current.AccessJTI
	if claims.IsRefresh {
		wantJTI = current.RefreshJTI
	}
	if claims.JTI != wantJTI {
		return ErrInvalidCredential
	}

	return nil
}

// 从 Token 对构造 Session 快照
func snapshotFromPair(subject TokenSubject, pair Pair) Snapshot {
	return Snapshot{
		TokenSubject: subject,
		AccessJTI:    pair.AccessJTI,
		RefreshJTI:   pair.RefreshJTI,
		ExpiresAt:    pair.ExpiresAt,
	}
}

// 从验证结果构造可信身份 Context
func contextWithSession(ctx context.Context, claims Claims, current Snapshot) context.Context {
	ctx = withSession(ctx, SessionState{
		ID:        current.SessionID,
		Subject:   current.Subject,
		AccessJTI: current.AccessJTI,
		ExpiresAt: current.ExpiresAt,
	})
	if claims.Subject == AdminKind {
		return withAdmin(ctx, current.UserID, current.Username, current.PasswordV, current.RoleIDs)
	}

	return withApp(ctx, current.UserID)
}

// 生成密码学随机标识符
func randomID() (string, error) {
	content := make([]byte, 32)
	rand.Read(content)

	return hex.EncodeToString(content), nil
}

// 无效凭证异常
func credentialErr() error {
	return exception.Comm("登录失效~", http.StatusUnauthorized)
}
